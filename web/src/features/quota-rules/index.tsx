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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
} from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTablePage } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatQuotaWithCurrency } from '@/lib/currency'

import {
  deleteQuotaRule,
  getQuotaRules,
  quotaRuleQueryKey,
  type QuotaRule,
} from './api'
import { QuotaRuleDialog } from './rule-dialog'

export function QuotaRules() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 10 })
  const [keyword, setKeyword] = useState('')
  const [status, setStatus] = useState('')
  const [bound, setBound] = useState('')
  const [editing, setEditing] = useState<QuotaRule | null | undefined>()
  const [deleting, setDeleting] = useState<QuotaRule | null>(null)
  const query = useQuery({
    queryKey: [...quotaRuleQueryKey, pagination, keyword, status, bound],
    queryFn: () =>
      getQuotaRules({
        p: pagination.pageIndex + 1,
        size: pagination.pageSize,
        keyword,
        status,
        bind_status: bound,
      }),
  })
  const remove = useMutation({
    mutationFn: deleteQuotaRule,
    onSuccess: () => {
      setDeleting(null)
      setPagination((previous) => ({ ...previous, pageIndex: 0 }))
      void client.invalidateQueries({ queryKey: quotaRuleQueryKey })
      toast.success(t('Quota rule deleted'))
    },
  })
  const columns: ColumnDef<QuotaRule>[] = [
    {
      accessorKey: 'name',
      header: t('Name'),
      cell: ({ row }) => (
        <div className='max-w-64 break-words'>
          <div className='font-medium'>{row.original.name}</div>
          <div className='text-muted-foreground text-xs'>
            {row.original.description}
          </div>
        </div>
      ),
    },
    {
      accessorKey: 'enabled',
      header: t('Status'),
      cell: ({ row }) => (row.original.enabled ? t('Enabled') : t('Disabled')),
    },
    {
      id: 'budget',
      header: t('Periodic quota limit'),
      cell: ({ row }) => {
        const rule = row.original
        if (!rule.quota_limit_enabled) return t('Unlimited')
        const period = {
          daily: t('Daily'),
          weekly: t('Weekly'),
          monthly: t('Monthly'),
        }[rule.quota_period]
        return `${period} / ${formatQuotaWithCurrency(rule.quota_limit)}`
      },
    },
    {
      id: 'window',
      header: t('Allowed weekdays'),
      cell: ({ row }) => {
        const rule = row.original
        if (!rule.time_window_enabled) return t('Every day')
        const labels = [
          t('Monday'),
          t('Tuesday'),
          t('Wednesday'),
          t('Thursday'),
          t('Friday'),
          t('Saturday'),
          t('Sunday'),
        ]
        return (
          <div className='max-w-60 whitespace-normal'>
            {(JSON.parse(rule.allowed_weekdays) as number[])
              .map((day) => labels[day - 1])
              .join(', ')}
            <div className='text-muted-foreground text-xs'>
              {rule.time_window_timezone}
            </div>
          </div>
        )
      },
    },
    { accessorKey: 'bound_token_count', header: t('Bound API keys') },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex gap-2'>
          <Button
            variant='outline'
            size='sm'
            onClick={() => setEditing(row.original)}
          >
            {t('Edit')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={row.original.bound_token_count > 0}
            onClick={() => setDeleting(row.original)}
          >
            {t('Delete')}
          </Button>
        </div>
      ),
    },
  ]
  const table = useReactTable({
    data: query.data?.items ?? [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    rowCount: query.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
  })
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Quota management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button onClick={() => setEditing(null)}>
            {t('Create quota rule')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <p className='text-muted-foreground mb-4 text-sm'>
            {t(
              'Each bound API key has its own budget. Rules do not add wallet or subscription quota.'
            )}
          </p>
          {query.isError ? (
            <ErrorState onRetry={() => void query.refetch()} />
          ) : (
            <DataTablePage
              table={table}
              columns={columns}
              isLoading={query.isLoading}
              isFetching={query.isFetching}
              emptyTitle={t('No quota rules found')}
              toolbar={
                <div className='flex flex-wrap gap-2'>
                  <Input
                    className='max-w-xs'
                    aria-label={t('Search quota rules')}
                    placeholder={t('Search quota rules')}
                    value={keyword}
                    onChange={(event) => {
                      setKeyword(event.target.value)
                      setPagination((previous) => ({
                        ...previous,
                        pageIndex: 0,
                      }))
                    }}
                  />
                  <NativeSelect
                    aria-label={t('Status')}
                    value={status}
                    onChange={(event) => {
                      setStatus(event.target.value)
                      setPagination((previous) => ({
                        ...previous,
                        pageIndex: 0,
                      }))
                    }}
                  >
                    <NativeSelectOption value=''>
                      {t('All statuses')}
                    </NativeSelectOption>
                    <NativeSelectOption value='enabled'>
                      {t('Enabled')}
                    </NativeSelectOption>
                    <NativeSelectOption value='disabled'>
                      {t('Disabled')}
                    </NativeSelectOption>
                  </NativeSelect>
                  <NativeSelect
                    aria-label={t('Binding status')}
                    value={bound}
                    onChange={(event) => {
                      setBound(event.target.value)
                      setPagination((previous) => ({
                        ...previous,
                        pageIndex: 0,
                      }))
                    }}
                  >
                    <NativeSelectOption value=''>
                      {t('All bindings')}
                    </NativeSelectOption>
                    <NativeSelectOption value='bound'>
                      {t('Bound')}
                    </NativeSelectOption>
                    <NativeSelectOption value='unbound'>
                      {t('Unbound')}
                    </NativeSelectOption>
                  </NativeSelect>
                </div>
              }
            />
          )}
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {editing !== undefined && (
        <QuotaRuleDialog
          key={editing?.id ?? 'create'}
          rule={editing}
          onClose={() => setEditing(undefined)}
        />
      )}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && !remove.isPending && setDeleting(null)}
        title={t('Delete quota rule')}
        desc={deleting?.name ?? ''}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => deleting && remove.mutate(deleting.id)}
      />
    </>
  )
}
