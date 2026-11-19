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
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  bindQuotaRule,
  getQuotaRules,
  quotaRuleQueryKey,
  type QuotaRule,
  type QuotaRuleTarget,
} from './api'

export function QuotaRuleBindingDialog(props: {
  apiKey: {
    id: number
    name: string
    quota_rule_id?: number
    quota_rule?: QuotaRule
  }
  target?: QuotaRuleTarget
  onClose: () => void
  onSuccess: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [selected, setSelected] = useState(props.apiKey.quota_rule_id ?? 0)
  const [keyword, setKeyword] = useState('')
  const query = useInfiniteQuery({
    queryKey: [...quotaRuleQueryKey, props.target, 'binding', keyword],
    initialPageParam: 1,
    queryFn: ({ pageParam }) =>
      getQuotaRules({ p: pageParam, size: 50, keyword }, props.target),
    getNextPageParam: (last, pages) =>
      pages.length * 50 < last.total ? pages.length + 1 : undefined,
  })
  const rules = query.data?.pages.flatMap((page) => page.items) ?? []
  const mutation = useMutation({
    mutationFn: () => bindQuotaRule(props.apiKey.id, selected, props.target),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: quotaRuleQueryKey })
      void client.invalidateQueries({ queryKey: ['api-key', props.apiKey.id] })
      props.onSuccess()
      toast.success(t('Quota rule binding saved'))
      props.onClose()
    },
  })
  return (
    <Dialog
      open
      title={t('Bind quota rule')}
      description={props.apiKey.name}
      onOpenChange={(open) => !open && !mutation.isPending && props.onClose()}
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
            disabled={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <Input
          aria-label={t('Search quota rules')}
          placeholder={t('Search quota rules')}
          value={keyword}
          onChange={(event) => setKeyword(event.target.value)}
        />
        {query.isError ? (
          <ErrorState onRetry={() => void query.refetch()} />
        ) : (
          <>
            <Label htmlFor='quota-rule-binding'>{t('Quota rule')}</Label>
            <NativeSelect
              id='quota-rule-binding'
              value={selected}
              disabled={query.isLoading}
              onChange={(event) => setSelected(Number(event.target.value))}
            >
              <NativeSelectOption value={0}>
                {t('No quota rule')}
              </NativeSelectOption>
              {selected > 0 && !rules.some((rule) => rule.id === selected) && (
                <NativeSelectOption value={selected}>
                  {selected === props.apiKey.quota_rule_id
                    ? (props.apiKey.quota_rule?.name ?? `#${selected}`)
                    : `#${selected}`}
                </NativeSelectOption>
              )}
              {rules.map((rule) => (
                <NativeSelectOption key={rule.id} value={rule.id}>
                  {rule.name}
                  {rule.enabled ? '' : ` (${t('Disabled')})`}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            {query.hasNextPage && (
              <Button
                variant='outline'
                disabled={query.isFetchingNextPage}
                onClick={() => void query.fetchNextPage()}
              >
                {t('Load more')}
              </Button>
            )}
          </>
        )}
        <p className='text-muted-foreground text-sm'>
          {t(
            'Disabling or unbinding a rule removes its restrictions. To cancel membership, set its end time or revoke the key.'
          )}
        </p>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Changing or unbinding a rule does not clear its existing period usage.'
          )}
        </p>
      </div>
    </Dialog>
  )
}
