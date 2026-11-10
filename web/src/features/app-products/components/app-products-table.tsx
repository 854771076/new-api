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
import type { OnChangeFn, PaginationState } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'

import type { AppProduct } from '../types'
import { useAppProductsColumns } from './app-products-columns'

interface Props {
  products: AppProduct[]
  total: number
  pagination: PaginationState
  onPaginationChange: OnChangeFn<PaginationState>
  isLoading: boolean
  isFetching: boolean
  onEdit: (product: AppProduct) => void
  onToggle: (product: AppProduct) => void
  onDelete: (product: AppProduct) => void
}

export function AppProductsTable({
  products,
  total,
  pagination,
  onPaginationChange,
  isLoading,
  isFetching,
  onEdit,
  onToggle,
  onDelete,
}: Props) {
  const { t } = useTranslation()
  const columns = useAppProductsColumns({ onEdit, onToggle, onDelete })

  const { table } = useDataTable({
    data: products,
    columns,
    manualPagination: true,
    pagination,
    onPaginationChange,
    totalCount: total,
    ensurePageInRange: (pageCount) => {
      if (pageCount > 0 && pagination.pageIndex >= pageCount) {
        onPaginationChange((prev) => ({
          ...prev,
          pageIndex: pageCount - 1,
        }))
      }
    },
    withFilteredRowModel: false,
    withFacetedRowModel: false,
  })

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No products yet')}
      emptyDescription={t(
        'Create your first product to start selling this app'
      )}
      skeletonKeyPrefix='app-products-skeleton'
      toolbarProps={null}
      applyHeaderSize
    />
  )
}
