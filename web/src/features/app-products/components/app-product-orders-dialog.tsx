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
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef, PaginationState } from '@tanstack/react-table'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  StaticDataTable,
  useDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { formatLocalCurrencyAmount } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'

import { getAppProductOrders, getAppProducts } from '../api'
import type { AppProduct, AppProductOrder } from '../types'

const PAGE_SIZE = 10
const EMPTY_COLUMNS: ColumnDef<AppProductOrder>[] = []

function getStatusVariant(status: string): StatusVariant {
  const normalized = status.toLowerCase()
  if (['success', 'completed', 'paid', 'succeeded'].includes(normalized)) {
    return 'success'
  }
  if (['pending', 'processing', 'created'].includes(normalized)) {
    return 'warning'
  }
  if (
    ['failed', 'cancelled', 'canceled', 'expired', 'refunded'].includes(
      normalized
    )
  ) {
    return 'danger'
  }
  return 'neutral'
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  appId: number
  products: AppProduct[]
}

export function AppProductOrdersDialog({
  open,
  onOpenChange,
  appId,
  products,
}: Props) {
  const { t } = useTranslation()
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: PAGE_SIZE,
  })

  useEffect(() => {
    setPagination({ pageIndex: 0, pageSize: PAGE_SIZE })
  }, [appId, open])

  const { data, isLoading } = useQuery({
    queryKey: [
      'app-product-orders',
      appId,
      pagination.pageIndex + 1,
      pagination.pageSize,
    ],
    queryFn: () =>
      getAppProductOrders({
        appId,
        page: pagination.pageIndex + 1,
        pageSize: pagination.pageSize,
      }),
    enabled: open,
    placeholderData: (prev) => prev,
  })

  // The products list is paginated, so load the full catalog (up to the API
  // cap) to name orders that reference products on other pages.
  const { data: productOptions } = useQuery({
    queryKey: ['app-products', appId, 'names'],
    queryFn: async () => {
      const res = await getAppProducts({ appId, page: 1, pageSize: 100 })
      return res.data
    },
    enabled: open,
    placeholderData: (prev) => prev,
  })

  const orders = data?.data?.items || []
  const total = data?.data?.total || 0
  const productNameById = useMemo(
    () =>
      new Map(
        [...(productOptions?.items ?? []), ...products].map((product) => [
          product.id,
          product.name,
        ])
      ),
    [productOptions, products]
  )

  const { table } = useDataTable({
    data: orders,
    columns: EMPTY_COLUMNS,
    manualPagination: true,
    pagination,
    onPaginationChange: setPagination,
    totalCount: total,
  })

  const columns: StaticDataTableColumn<AppProductOrder>[] = useMemo(
    () => [
      {
        id: 'id',
        header: t('ID'),
        cell: (order) => <TableId value={order.id} />,
      },
      {
        id: 'user_id',
        header: t('User ID'),
        cell: (order) => <TableId value={order.user_id} />,
      },
      {
        id: 'product',
        header: t('Product'),
        cell: (order) =>
          productNameById.get(order.product_id) || `#${order.product_id}`,
      },
      {
        id: 'money',
        header: t('Amount'),
        cell: (order) => (
          <span className='tabular-nums'>
            {formatLocalCurrencyAmount(Number(order.price_amount || 0), {
              showSymbol: false,
            })}{' '}
            {order.currency || 'USD'}
          </span>
        ),
      },
      {
        id: 'trade_no',
        header: t('Trade No'),
        cell: (order) => (
          <span className='font-mono text-xs'>{order.trade_no || '-'}</span>
        ),
      },
      {
        id: 'payment_method',
        header: t('Payment Method'),
        cell: (order) => order.payment_method || '-',
      },
      {
        id: 'payment_provider',
        header: t('Payment Provider'),
        cell: (order) => order.payment_provider || '-',
      },
      {
        id: 'status',
        header: t('Status'),
        cell: (order) => (
          <StatusBadge
            label={order.status || '-'}
            variant={getStatusVariant(order.status || '')}
            copyable={false}
          />
        ),
      },
      {
        id: 'create_time',
        header: t('Created At'),
        cell: (order) => formatTimestampToDate(order.create_time),
      },
      {
        id: 'complete_time',
        header: t('Completed At'),
        cell: (order) => formatTimestampToDate(order.complete_time),
      },
    ],
    [productNameById, t]
  )

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Orders')}
      description={t('App product orders')}
      contentHeight='min(70vh, 560px)'
      contentClassName='sm:max-w-5xl'
      bodyClassName='flex h-full min-h-0 flex-col gap-3'
    >
      <div className='min-h-0 flex-1 overflow-auto'>
        <StaticDataTable
          columns={columns}
          data={orders}
          getRowKey={(order) => order.id}
          empty={!isLoading && orders.length === 0}
          emptyContent={
            <span className='text-muted-foreground text-sm'>
              {isLoading ? t('Loading...') : t('No orders yet')}
            </span>
          }
        />
      </div>
      <div className='shrink-0'>
        <DataTablePagination table={table} />
      </div>
    </Dialog>
  )
}
