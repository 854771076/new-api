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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { DateTimePicker } from '@/components/datetime-picker'
import { Dialog } from '@/components/dialog'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { getCurrencyLabel } from '@/lib/currency'
import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import {
  quotaRuleQueryKey,
  saveQuotaRule,
  type QuotaRule,
  type QuotaRuleTarget,
} from './api'

const weekdays = [
  'Monday',
  'Tuesday',
  'Wednesday',
  'Thursday',
  'Friday',
  'Saturday',
  'Sunday',
] as const

export function QuotaRuleDialog(props: {
  rule: QuotaRule | null
  onClose: () => void
  target?: QuotaRuleTarget
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const schema = z
    .object({
      name: z.string().trim().min(1, t('Required')).max(128),
      description: z.string().max(255),
      enabled: z.boolean(),
      quota_limit_enabled: z.boolean(),
      quota_period: z.enum(['daily', 'weekly', 'monthly']),
      amount: z.number().finite().nonnegative(),
      time_window_enabled: z.boolean(),
      time_window_timezone: z.enum(['UTC', 'Asia/Shanghai']),
      days: z.array(z.string()),
      valid_from: z.number().int().min(1).max(253402300799).nullable(),
      valid_until: z.number().int().min(1).max(253402300799).nullable(),
    })
    .superRefine((value, context) => {
      if (
        value.valid_from !== null &&
        value.valid_until !== null &&
        value.valid_from >= value.valid_until
      ) {
        context.addIssue({
          code: 'custom',
          path: ['valid_until'],
          message: t('Quota rule start must be before its end'),
        })
      }
      const quota = parseQuotaFromDollars(value.amount)
      if (
        value.quota_limit_enabled &&
        (!Number.isSafeInteger(quota) || quota <= 0)
      ) {
        context.addIssue({
          code: 'custom',
          path: ['amount'],
          message: t('Quota rule limit must be a positive safe integer'),
        })
      }
      if (value.time_window_enabled && !value.days.length) {
        context.addIssue({
          code: 'custom',
          path: ['days'],
          message: t('Select valid weekdays for the quota rule'),
        })
      }
    })
  const rule = props.rule
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: rule?.name ?? '',
      valid_from: rule?.valid_from ?? null,
      valid_until: rule?.valid_until ?? null,
      description: rule?.description ?? '',
      enabled: rule?.enabled ?? true,
      quota_limit_enabled: rule?.quota_limit_enabled ?? true,
      quota_period: rule?.quota_period ?? 'daily',
      amount: rule ? quotaUnitsToDollars(rule.quota_limit) : 0,
      time_window_enabled: rule?.time_window_enabled ?? false,
      time_window_timezone: rule?.time_window_timezone ?? 'Asia/Shanghai',
      days: rule
        ? (JSON.parse(rule.allowed_weekdays) as number[]).map(String)
        : ['1', '2', '3', '4', '5', '6', '7'],
    },
  })
  const mutation = useMutation({
    mutationFn: (values: z.infer<typeof schema>) =>
      saveQuotaRule(
        {
          id: rule?.id ?? 0,
          name: values.name,
          description: values.description,
          enabled: values.enabled,
          valid_from: values.valid_from,
          valid_until: values.valid_until,
          quota_limit_enabled: values.quota_limit_enabled,
          quota_period: values.quota_period,
          quota_limit: values.quota_limit_enabled
            ? parseQuotaFromDollars(values.amount)
            : 0,
          time_window_enabled: values.time_window_enabled,
          time_window_timezone: values.time_window_timezone,
          allowed_weekdays: JSON.stringify(values.days.map(Number)),
        },
        props.target
      ),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: quotaRuleQueryKey })
      void client.invalidateQueries({ queryKey: ['keys'] })
      void client.invalidateQueries({ queryKey: ['admin-app-keys'] })
      toast.success(t('Quota rule saved'))
      props.onClose()
    },
  })
  const limited = form.watch('quota_limit_enabled')
  const windowed = form.watch('time_window_enabled')
  return (
    <Dialog
      open
      onOpenChange={(open) => !open && !mutation.isPending && props.onClose()}
      title={rule ? t('Edit quota rule') : t('Create quota rule')}
      description={t(
        'Each bound API key has its own budget. Rules do not add wallet or subscription quota.'
      )}
      footer={
        <>
          <Button
            variant='outline'
            disabled={mutation.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='quota-rule-form'
            disabled={mutation.isPending}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='quota-rule-form'
          className='space-y-4'
          onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
        >
          {!!rule?.bound_token_count && (
            <p role='status'>
              {t('Editing this rule affects {{count}} bound API keys.', {
                count: rule.bound_token_count,
              })}
            </p>
          )}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Disabling or unbinding a rule removes its restrictions. To cancel membership, set its end time or revoke the key.'
            )}
          </p>
          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Name')}</FormLabel>
                <FormControl>
                  <Input {...field} maxLength={128} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='description'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Description')}</FormLabel>
                <FormControl>
                  <Textarea {...field} maxLength={255} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between'>
                <FormLabel>{t('Enabled')}</FormLabel>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='quota_limit_enabled'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between'>
                <FormLabel>{t('Periodic quota limit')}</FormLabel>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />
          {limited && (
            <>
              <FormField
                control={form.control}
                name='quota_period'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Quota period')}</FormLabel>
                    <FormControl>
                      <NativeSelect {...field}>
                        <NativeSelectOption value='daily'>
                          {t('Daily')}
                        </NativeSelectOption>
                        <NativeSelectOption value='weekly'>
                          {t('Weekly')}
                        </NativeSelectOption>
                        <NativeSelectOption value='monthly'>
                          {t('Monthly')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='amount'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Quota ({{currency}})', {
                        currency: getCurrencyLabel(),
                      })}
                    </FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='number'
                        min={0}
                        step='any'
                        onChange={(event) =>
                          field.onChange(event.target.valueAsNumber)
                        }
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </>
          )}
          <FormField
            control={form.control}
            name='time_window_timezone'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Timezone')}</FormLabel>
                <FormControl>
                  <NativeSelect {...field}>
                    <NativeSelectOption value='UTC'>UTC</NativeSelectOption>
                    <NativeSelectOption value='Asia/Shanghai'>
                      {t('Beijing time (UTC+8)')}
                    </NativeSelectOption>
                  </NativeSelect>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='time_window_enabled'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between'>
                <FormLabel>{t('Restrict allowed weekdays')}</FormLabel>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </FormItem>
            )}
          />
          {(['valid_from', 'valid_until'] as const).map((name) => (
            <FormField
              key={name}
              control={form.control}
              name={name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {name === 'valid_from'
                      ? t('Valid from (inclusive)')
                      : t('Valid until (exclusive)')}
                  </FormLabel>
                  <FormControl>
                    <DateTimePicker
                      timeZone={form.watch('time_window_timezone')}
                      value={
                        field.value === null
                          ? undefined
                          : new Date(field.value * 1000)
                      }
                      onChange={(date) =>
                        field.onChange(
                          date ? Math.floor(date.getTime() / 1000) : null
                        )
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Empty dates are unbounded. Calendar periods reset in the selected timezone; changing period or timezone may select a different budget.'
            )}
          </p>
          {windowed && (
            <FormField
              control={form.control}
              name='days'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Allowed weekdays')}</FormLabel>
                  <FormControl>
                    <MultiSelect
                      id='quota-rule-days'
                      options={weekdays.map((label, index) => ({
                        label: t(label),
                        value: String(index + 1),
                      }))}
                      selected={field.value}
                      onChange={field.onChange}
                      placeholder={t('Allowed weekdays')}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
        </form>
      </Form>
    </Dialog>
  )
}
