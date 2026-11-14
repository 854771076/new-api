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
import { useTranslation } from 'react-i18next'

import type { UseFormReturn } from 'react-hook-form'

import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'
import { safeNumberFieldProps } from '@/features/system-settings/utils/numeric-field'

// Form values are NESTED because react-hook-form resolves dotted field names
// ('concurrency.queue_depth_ratio') as paths: a flat schema would silently
// validate stale top-level values while edits land in values.concurrency
// (same shape monitoring-settings-section uses for perf_metrics_setting.*).
export type QueueFormValues = {
  concurrency: {
    queue_enabled: boolean
    queue_depth_ratio: number
    queue_depth_cap: number
    queue_max_wait_seconds: number
  }
}

interface QueueSettingsFormProps {
  form: UseFormReturn<QueueFormValues>
  /** Disables every control while options load or a save is in flight. */
  busy: boolean
  onSubmit: (values: QueueFormValues) => void
}

/**
 * Form body of the queue settings card: the enable switch, the three
 * bounded-queue numeric fields and the save button. Validation schema and
 * submit logic stay in QueueSettingsCard; this component is presentation.
 */
export function QueueSettingsForm(props: QueueSettingsFormProps) {
  const { t } = useTranslation()
  return (
    <Form {...props.form}>
      <form
        onSubmit={props.form.handleSubmit(props.onSubmit)}
        className='flex flex-col gap-4'
      >
        <SettingsSwitchField
          controlId='queue-enabled'
          label={t('Enable queueing')}
          description={t(
            'When disabled, requests beyond the concurrency limit are rejected immediately with 429.'
          )}
          checked={props.form.watch('concurrency.queue_enabled')}
          onCheckedChange={(checked) =>
            props.form.setValue('concurrency.queue_enabled', checked)
          }
          disabled={props.busy}
        />

        <div className='grid gap-4 sm:grid-cols-3'>
          <FormField
            control={props.form.control}
            name='concurrency.queue_depth_ratio'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Depth ratio')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    max={64}
                    step={1}
                    disabled={props.busy}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Queue depth before capping: this multiple of the concurrency limit. Allowed values: 0 to 64.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={props.form.control}
            name='concurrency.queue_depth_cap'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Depth cap')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    max={1024}
                    step={1}
                    disabled={props.busy}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Upper bound on queued requests per bucket. Allowed values: 1 to 1024.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={props.form.control}
            name='concurrency.queue_max_wait_seconds'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Max wait')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    max={60}
                    step={1}
                    disabled={props.busy}
                    {...safeNumberFieldProps(field)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'How long a request may wait before it is rejected with 429. Allowed values: 0 to 60.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>

        <div className='flex justify-end'>
          <Button type='submit' disabled={props.busy}>
            {t('Save queue settings')}
          </Button>
        </div>
      </form>
    </Form>
  )
}
