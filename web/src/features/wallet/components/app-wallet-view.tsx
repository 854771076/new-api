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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { TitledCard } from '@/components/ui/titled-card'
import { api } from '@/lib/api'
import { formatCurrencyFromUSD } from '@/lib/currency'
import { formatQuota, formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  createScopedCheckout,
  getAppCatalog,
  getAppWallet,
  readPendingCheckout,
  refreshScopedOrder,
  type ScopedCheckout,
  type ScopedPurchase,
} from '../app-wallet-api'
import { useTopupInfo } from '../hooks/use-topup-info'
import { submitPaymentForm } from '../lib/payment'
import { BillingHistoryDialog } from './dialogs/billing-history-dialog'
import { RechargeFormCard } from './recharge-form-card'
import { SubscriptionPlansCard } from './subscription-plans-card'
import { WalletStatsCard } from './wallet-stats-card'

export function AppWalletView(props: { appId: number; readOnly?: boolean }) {
  const { t } = useTranslation()
  const userId = useAuthStore((s) => s.auth.user?.id)
  const [amount, setAmount] = useState(10)
  const [confirm, setConfirm] = useState<ScopedPurchase | null>(null)
  const [busy, setBusy] = useState(false)
  const [order, setOrder] = useState<ScopedCheckout | null>(() =>
    readPendingCheckout(userId, props.appId)
  )
  const [history, setHistory] = useState(false)
  const { topupInfo } = useTopupInfo()
  const wallet = useQuery({
    queryKey: ['wallet', userId, props.appId],
    queryFn: () => getAppWallet(props.appId),
  })
  const catalog = useQuery({
    queryKey: ['app-catalog', userId, props.appId],
    queryFn: () => getAppCatalog(props.appId),
  })
  const preview = useQuery({
    queryKey: ['app-topup-preview', userId, props.appId, amount],
    queryFn: async () =>
      requireServerSuccess(
        (
          await api.post('/api/user/self/app-topup-preview', {
            app_id: props.appId,
            amount,
            payment_method: 'waffo_pancake',
          })
        ).data
      ).data as { money: number; credits: string | null; quota: number },
    enabled:
      !props.readOnly && !!topupInfo?.enable_waffo_pancake_topup && amount > 0,
    retry: false,
    meta: { errorToast: false },
  })
  const purchase = async (body: ScopedPurchase) => {
    if (!userId || props.readOnly) return
    setBusy(true)
    try {
      const result = await createScopedCheckout(userId, body)
      setOrder(result)
      setConfirm(null)
      const link = result.checkout_url || result.pay_link
      if (link) {
        const url = new URL(link)
        if (url.protocol !== 'https:' || url.username || url.password) {
          throw new Error('Invalid payment URL')
        }
        window.location.assign(url.href)
      } else if (result.url && result.params) {
        const url = new URL(result.url)
        if (url.protocol !== 'https:' || url.username || url.password) {
          throw new Error('Invalid payment URL')
        }
        submitPaymentForm(url.href, result.params)
      } else toast.info(t('Order confirmation is pending'))
      await wallet.refetch()
      await catalog.refetch()
    } finally {
      setBusy(false)
    }
  }
  const checkOrder = async () => {
    if (!order || !userId) return
    try {
      setOrder(await refreshScopedOrder(userId, order))
      await wallet.refetch()
      await catalog.refetch()
    } catch (error) {
      handleServerError(error)
    }
  }
  if (wallet.isError) {
    return <ErrorState onRetry={() => void wallet.refetch()} />
  }
  const appTopupInfo = topupInfo
    ? {
        ...topupInfo,
        enable_online_topup: false,
        enable_stripe_topup: false,
        enable_creem_topup: false,
        pay_methods: topupInfo.enable_waffo_pancake_topup
          ? [
              {
                type: 'waffo_pancake',
                name: 'Waffo Pancake',
                min_topup: topupInfo.waffo_pancake_min_topup,
              },
            ]
          : [],
      }
    : null
  return (
    <div className='space-y-4'>
      {props.readOnly && (
        <p role='status'>{t('Historical App assets are read-only')}</p>
      )}
      <WalletStatsCard
        user={null}
        wallet={wallet.data}
        loading={wallet.isPending}
      />
      <Button variant='outline' onClick={() => setHistory(true)}>
        {t('Order History')}
      </Button>
      {order && (
        <TitledCard title={t('Order status')}>
          <p className='break-all'>
            {order.order_id} · {order.status}
          </p>
          <Button onClick={() => void checkOrder()}>{t('Refresh')}</Button>
        </TitledCard>
      )}
      {!props.readOnly && (
        <>
          <RechargeFormCard
            hideRedemption
            topupInfo={appTopupInfo}
            presetAmounts={[]}
            selectedPreset={null}
            onSelectPreset={(p) => setAmount(p.value)}
            topupAmount={amount}
            onTopupAmountChange={setAmount}
            paymentAmount={preview.data?.money ?? 0}
            calculating={
              preview.isPending || preview.isFetching || !preview.data
            }
            onPaymentMethodSelect={() => {
              if (preview.data && !preview.isFetching) {
                setConfirm({
                  app_id: props.appId,
                  purchase_type: 'topup',
                  amount,
                  payment_method: 'waffo_pancake',
                })
              }
            }}
            paymentLoading={busy ? 'waffo_pancake' : null}
            redemptionCode=''
            onRedemptionCodeChange={() => {}}
            onRedeem={() => {}}
            redeeming={false}
            enableWaffoPancakeTopup={topupInfo?.enable_waffo_pancake_topup}
          />
          {preview.isError && (
            <ErrorState onRetry={() => void preview.refetch()} />
          )}
          {preview.data && (
            <p>
              {t('Estimated credit')}:{' '}
              {preview.data.credits == null
                ? formatQuota(preview.data.quota)
                : `${preview.data.credits} ${t('Credits')}`}
            </p>
          )}
        </>
      )}
      {wallet.data?.capabilities.subscription_supported && (
        <SubscriptionPlansCard
          appId={props.appId}
          readOnly={props.readOnly}
          topupInfo={topupInfo}
          userQuota={wallet.data.quota}
          onScopedPay={(planId, method) =>
            purchase({
              app_id: props.appId,
              purchase_type: 'subscription',
              plan_id: planId,
              payment_method: method,
            })
          }
        />
      )}
      {catalog.isError ? (
        <ErrorState onRetry={() => void catalog.refetch()} />
      ) : (
        <TitledCard title={t('Products')}>
          {!catalog.isPending && !catalog.data?.products.length && (
            <EmptyState />
          )}
          <div className='grid gap-3 sm:grid-cols-2'>
            {catalog.data?.products.map((product) => (
              <div key={product.id} className='space-y-2 rounded-lg border p-3'>
                <p>{product.name}</p>
                <p>{product.description}</p>
                <p>{`${formatNumber(product.price_amount)} ${product.currency}`}</p>
                <p>
                  {product.credits == null
                    ? `${formatNumber(product.quota_amount)} quota`
                    : `${product.credits} ${t('Credits')}`}
                </p>
                {!props.readOnly && (
                  <Button
                    disabled={
                      busy ||
                      !topupInfo?.enable_waffo_pancake_topup ||
                      !product.waffo_pancake_product_id
                    }
                    onClick={() =>
                      setConfirm({
                        app_id: props.appId,
                        purchase_type: 'product',
                        product_id: product.id,
                        payment_method: 'waffo_pancake',
                      })
                    }
                  >
                    {t('Buy')}
                  </Button>
                )}
              </div>
            ))}
          </div>
        </TitledCard>
      )}
      <ConfirmDialog
        open={!!confirm}
        onOpenChange={(open) => {
          if (!open) setConfirm(null)
        }}
        title={t('Confirm Payment')}
        desc={
          <span>
            {t('App')} #{props.appId} ·{' '}
            {confirm?.purchase_type === 'topup' && preview.data
              ? formatCurrencyFromUSD(preview.data.money)
              : t('Products')}
          </span>
        }
        isLoading={busy}
        handleConfirm={() => {
          if (confirm) void purchase(confirm).catch(handleServerError)
        }}
      />
      <BillingHistoryDialog
        open={history}
        onOpenChange={setHistory}
        appId={props.appId}
        selfOnly
      />
    </div>
  )
}
