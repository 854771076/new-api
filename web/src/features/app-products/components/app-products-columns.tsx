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
import type { ColumnDef } from '@tanstack/react-table'
import { Pencil, Power, PowerOff, Trash2 } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { BadgeCell } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatLocalCurrencyAmount } from '@/lib/currency'
import { formatQuota } from '@/lib/format'

import type { AppProduct } from '../types'

interface AppProductColumnHandlers {
  onEdit: (product: AppProduct) => void
  onToggle: (product: AppProduct) => void
  onDelete: (product: AppProduct) => void
}

export function useAppProductsColumns(
  handlers: AppProductColumnHandlers
): ColumnDef<AppProduct>[] {
  const { t } = useTranslation()
  const { onEdit, onToggle, onDelete } = handlers

  return useMemo<ColumnDef<AppProduct>[]>(
    () => [
      {
        accessorKey: 'id',
        header: t('ID'),
        meta: { mobileHidden: true },
        cell: ({ row }) => <TableId value={row.original.id} />,
        size: 60,
      },
      {
        accessorKey: 'name',
        header: t('Product Name'),
        meta: { mobileTitle: true },
        cell: ({ row }) => (
          <div className='max-w-full min-w-0'>
            <div className='truncate font-medium'>{row.original.name}</div>
            {row.original.description && (
              <div className='text-muted-foreground truncate text-xs'>
                {row.original.description}
              </div>
            )}
          </div>
        ),
        size: 220,
      },
      {
        accessorKey: 'price_amount',
        header: t('Price'),
        cell: ({ row }) => (
          <div className='flex flex-col'>
            <span className='font-semibold text-emerald-600'>
              {formatLocalCurrencyAmount(
                Number(row.original.price_amount || 0),
                { showSymbol: false }
              )}{' '}
              {row.original.currency}
            </span>
          </div>
        ),
        size: 110,
      },
      {
        accessorKey: 'quota_amount',
        header: t('Quota'),
        cell: ({ row }) => (
          <span className='text-muted-foreground tabular-nums'>
            {formatQuota(Number(row.original.quota_amount || 0))}
          </span>
        ),
        size: 140,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'max_purchase_per_user',
        header: t('Purchase Limit'),
        cell: ({ row }) =>
          row.original.max_purchase_per_user > 0 ? (
            <span className='tabular-nums'>
              {row.original.max_purchase_per_user}
            </span>
          ) : (
            <span className='text-muted-foreground'>{t('Unlimited')}</span>
          ),
        size: 110,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'sort_order',
        header: t('Sort Order'),
        cell: ({ row }) => (
          <span className='text-muted-foreground tabular-nums'>
            {row.original.sort_order}
          </span>
        ),
        size: 90,
        meta: { mobileHidden: true },
      },
      {
        accessorKey: 'enabled',
        header: t('Status'),
        meta: { mobileBadge: true },
        cell: ({ row }) =>
          row.original.enabled ? (
            <StatusBadge
              label={t('Enabled')}
              variant='success'
              copyable={false}
              className='-ml-1.5'
            />
          ) : (
            <StatusBadge
              label={t('Disabled')}
              variant='neutral'
              copyable={false}
              className='-ml-1.5'
            />
          ),
        size: 90,
      },
      {
        id: 'payment',
        header: t('Payment Channel'),
        meta: { mobileHidden: true },
        cell: ({ row }) => (
          <BadgeCell>
            {row.original.stripe_price_id && (
              <StatusBadge label='Stripe' variant='neutral' copyable={false} />
            )}
            {row.original.creem_product_id && (
              <StatusBadge label='Creem' variant='neutral' copyable={false} />
            )}
            {row.original.waffo_pancake_product_id && (
              <StatusBadge
                label='Waffo Pancake'
                variant='neutral'
                copyable={false}
              />
            )}
          </BadgeCell>
        ),
        size: 140,
      },
      {
        id: 'actions',
        header: () => t('Actions'),
        cell: ({ row }) => (
          <div className='-ml-1.5 flex items-center gap-1'>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => onEdit(row.original)}
                    aria-label={t('Edit')}
                  />
                }
              >
                <Pencil />
              </TooltipTrigger>
              <TooltipContent>{t('Edit')}</TooltipContent>
            </Tooltip>

            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => onToggle(row.original)}
                    aria-label={
                      row.original.enabled ? t('Disable') : t('Enable')
                    }
                    className={
                      row.original.enabled
                        ? 'text-destructive hover:text-destructive'
                        : 'text-success hover:text-success'
                    }
                  />
                }
              >
                {row.original.enabled ? <PowerOff /> : <Power />}
              </TooltipTrigger>
              <TooltipContent>
                {row.original.enabled ? t('Disable') : t('Enable')}
              </TooltipContent>
            </Tooltip>

            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => onDelete(row.original)}
                    aria-label={t('Delete')}
                    className='text-destructive hover:text-destructive'
                  />
                }
              >
                <Trash2 />
              </TooltipTrigger>
              <TooltipContent>{t('Delete')}</TooltipContent>
            </Tooltip>
          </div>
        ),
        meta: { pinned: 'right' as const },
      },
    ],
    [onDelete, onEdit, onToggle, t]
  )
}
