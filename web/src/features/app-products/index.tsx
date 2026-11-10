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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { PaginationState } from '@tanstack/react-table'
import { Plus, ReceiptText } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { useActiveApp } from '@/hooks/use-active-app'
import { handleServerError } from '@/lib/handle-server-error'

import { deleteAppProduct, getAppProducts, patchAppProductEnabled } from './api'
import { AppProductMutateDrawer } from './components/app-product-mutate-drawer'
import { AppProductOrdersDialog } from './components/app-product-orders-dialog'
import { AppProductsTable } from './components/app-products-table'
import type { AppProduct } from './types'

const PRODUCTS_PAGE_SIZE = 20

export function AppProducts() {
  const { t } = useTranslation()
  const { appId, isPlatform } = useActiveApp()
  const queryClient = useQueryClient()

  const [mutateOpen, setMutateOpen] = useState(false)
  const [currentProduct, setCurrentProduct] = useState<AppProduct | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AppProduct | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [ordersOpen, setOrdersOpen] = useState(false)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: PRODUCTS_PAGE_SIZE,
  })

  useEffect(() => {
    setMutateOpen(false)
    setCurrentProduct(null)
    setDeleteTarget(null)
    setOrdersOpen(false)
    setPagination({ pageIndex: 0, pageSize: PRODUCTS_PAGE_SIZE })
  }, [appId])

  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      'app-products',
      appId,
      pagination.pageIndex + 1,
      pagination.pageSize,
    ],
    queryFn: async () => {
      const res = await getAppProducts({
        appId,
        page: pagination.pageIndex + 1,
        pageSize: pagination.pageSize,
      })
      return res.data
    },
    // Keep the previous page only while the active app stays the same; rows
    // from another app must never stay clickable after a switch.
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[1] === appId ? previousData : undefined,
  })

  const products = data?.items || []
  const total = data?.total || 0

  const invalidateProducts = () =>
    queryClient.invalidateQueries({ queryKey: ['app-products', appId] })

  const handleToggle = async (product: AppProduct) => {
    try {
      const res = await patchAppProductEnabled(product.id, !product.enabled)
      if (res.success) {
        toast.success(
          t(product.enabled ? 'Has been disabled' : 'Has been enabled')
        )
        void invalidateProducts()
      } else {
        handleServerError(res)
      }
    } catch (error) {
      handleServerError(error, t('Request failed'))
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      const res = await deleteAppProduct(deleteTarget.id)
      if (res.success) {
        toast.success(t('Deleted successfully'))
        setDeleteTarget(null)
        void invalidateProducts()
      } else {
        handleServerError(res)
      }
    } catch (error) {
      handleServerError(error, t('Request failed'))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Product Management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              onClick={() => setOrdersOpen(true)}
            >
              <ReceiptText className='h-4 w-4' />
              {t('Orders')}
            </Button>
            <Button
              size='sm'
              disabled={isPlatform}
              title={
                isPlatform
                  ? t('Switch to an app to create products')
                  : undefined
              }
              onClick={() => {
                setCurrentProduct(null)
                setMutateOpen(true)
              }}
            >
              <Plus className='h-4 w-4' />
              {t('Create Product')}
            </Button>
          </div>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='h-full min-h-0'>
            <AppProductsTable
              products={products}
              total={total}
              pagination={pagination}
              onPaginationChange={setPagination}
              isLoading={isLoading}
              isFetching={isFetching}
              onEdit={(product) => {
                setCurrentProduct(product)
                setMutateOpen(true)
              }}
              onToggle={handleToggle}
              onDelete={setDeleteTarget}
            />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <AppProductMutateDrawer
        open={mutateOpen}
        onOpenChange={setMutateOpen}
        product={currentProduct}
        appId={appId}
        onSuccess={() => void invalidateProducts()}
      />

      <AppProductOrdersDialog
        open={ordersOpen}
        onOpenChange={setOrdersOpen}
        appId={appId}
        products={products}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
        title={t('Delete Product')}
        desc={t('Are you sure you want to delete {{name}}?', {
          name: deleteTarget?.name ?? '',
        })}
        destructive
        isLoading={deleting}
        confirmText={t('Delete')}
        handleConfirm={handleDelete}
      />
    </>
  )
}
