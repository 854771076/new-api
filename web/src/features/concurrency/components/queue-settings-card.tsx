/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect, useMemo } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  getOptionValue,
  useSystemOptions,
} from '@/features/system-settings/hooks/use-system-options'
import { useUpdateOption } from '@/features/system-settings/hooks/use-update-option'

import { QueueSettingsForm, type QueueFormValues } from './queue-settings-form'

/** Flat option keys exactly as the backend contract stores them. */
type QueueOptionDefaults = {
  'concurrency.queue_enabled': boolean
  'concurrency.queue_depth_ratio': number
  'concurrency.queue_depth_cap': number
  'concurrency.queue_max_wait_seconds': number
}

const QUEUE_OPTION_DEFAULTS: QueueOptionDefaults = {
  'concurrency.queue_enabled': true,
  'concurrency.queue_depth_ratio': 4,
  'concurrency.queue_depth_cap': 128,
  'concurrency.queue_max_wait_seconds': 10,
}

const buildFormDefaults = (options: QueueOptionDefaults): QueueFormValues => ({
  concurrency: {
    queue_enabled: options['concurrency.queue_enabled'],
    queue_depth_ratio: options['concurrency.queue_depth_ratio'],
    queue_depth_cap: options['concurrency.queue_depth_cap'],
    queue_max_wait_seconds: options['concurrency.queue_max_wait_seconds'],
  },
})

const flattenQueueValues = (values: QueueFormValues): QueueOptionDefaults => ({
  'concurrency.queue_enabled': values.concurrency.queue_enabled,
  'concurrency.queue_depth_ratio': values.concurrency.queue_depth_ratio,
  'concurrency.queue_depth_cap': values.concurrency.queue_depth_cap,
  'concurrency.queue_max_wait_seconds':
    values.concurrency.queue_max_wait_seconds,
})

// Ranges mirror the backend contract: depth ratio 0-64, depth cap 1-1024,
// max wait 0-60 seconds. Literal keys (not templated) so the i18n scanner
// sees every message and FormMessage can translate them per language.
function createQueueSchema(t: (key: string) => string) {
  return z.object({
    concurrency: z.object({
      queue_enabled: z.boolean(),
      queue_depth_ratio: z
        .number()
        .int({ error: t('Enter a whole number') })
        .min(0, { error: t('Allowed values: 0 to 64') })
        .max(64, { error: t('Allowed values: 0 to 64') }),
      queue_depth_cap: z
        .number()
        .int({ error: t('Enter a whole number') })
        .min(1, { error: t('Allowed values: 1 to 1024') })
        .max(1024, { error: t('Allowed values: 1 to 1024') }),
      queue_max_wait_seconds: z
        .number()
        .int({ error: t('Enter a whole number') })
        .min(0, { error: t('Allowed values: 0 to 60') })
        .max(60, { error: t('Allowed values: 0 to 60') }),
    }),
  })
}

/**
 * Bounded wait-queue configuration for the concurrency limiter (option keys
 * `concurrency.*`, persisted through PUT /api/option/). Queue depth per
 * bucket = min(depth ratio × limit, depth cap); requests that out-wait the
 * max wait, or arrive at a full queue, get a 429.
 *
 * Only keys the operator actually edited are submitted; values round-trip as
 * option strings ("true" / "7") per the backend contract. Reuses the
 * system-settings option query and mutation so ['system-options'],
 * toasts and error handling stay identical across the two surfaces.
 */
export function QueueSettingsCard() {
  const { t } = useTranslation()
  const optionsQuery = useSystemOptions()
  const updateOption = useUpdateOption()

  const serverBaseline = useMemo(
    () => getOptionValue(optionsQuery.data?.data, QUEUE_OPTION_DEFAULTS),
    [optionsQuery.data]
  )
  const formDefaults = useMemo(
    () => buildFormDefaults(serverBaseline),
    [serverBaseline]
  )
  const form = useForm<QueueFormValues>({
    resolver: zodResolver(createQueueSchema(t)),
    mode: 'onChange',
    defaultValues: formDefaults,
  })

  // Re-seed from the server on first load and after every save-triggered
  // refetch so saved values become the new "unchanged" baseline.
  useEffect(() => {
    form.reset(formDefaults)
  }, [formDefaults, form])

  const busy = updateOption.isPending || !optionsQuery.isSuccess

  const onSubmit = async (values: QueueFormValues) => {
    const normalized = flattenQueueValues(values)
    const updates = (
      Object.keys(normalized) as Array<keyof QueueOptionDefaults>
    ).filter((key) => normalized[key] !== serverBaseline[key])
    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    for (const key of updates) {
      await updateOption.mutateAsync({ key, value: String(normalized[key]) })
    }
  }

  return (
    // h-full/min-h-0: fills one cell of the page's 2×2 grid at lg+, while
    // the body scrolls on its own when the cell is shorter than the form.
    <Card className='h-full min-h-0'>
      <CardHeader>
        <CardTitle>{t('Bounded wait queue')}</CardTitle>
        <CardDescription>
          {t(
            'When a concurrency bucket is full, requests wait in a bounded queue instead of failing immediately. Queue depth = min(depth ratio × concurrency limit, depth cap). A request that waits longer than the max wait, or finds the queue full, receives a 429 response.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='min-h-0 flex-1 overflow-y-auto'>
        <QueueSettingsForm form={form} busy={busy} onSubmit={onSubmit} />
      </CardContent>
    </Card>
  )
}
