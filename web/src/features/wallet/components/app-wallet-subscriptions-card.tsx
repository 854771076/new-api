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
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { getSelfSubscriptionFull } from '@/features/subscriptions/api'
import type {
  UserAppWallet,
  UserSubscriptionRecord,
} from '@/features/subscriptions/types'
import { formatQuota } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

function getRemainingDays(sub: UserSubscriptionRecord): number {
  const endTime = sub?.subscription?.end_time || 0
  if (!endTime) return 0
  const now = Date.now() / 1000
  return Math.max(0, Math.ceil((endTime - now) / 86400))
}

export function AppWalletSubscriptionsCard() {
  const { t } = useTranslation()

  const [wallets, setWallets] = useState<UserAppWallet[]>([])
  const [subscriptions, setSubscriptions] = useState<UserSubscriptionRecord[]>(
    []
  )
  const [appNames, setAppNames] = useState<Map<number, string>>(new Map())
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const init = async () => {
      setLoading(true)
      try {
        const res = requireServerSuccess(await getSelfSubscriptionFull())
        if (res.success && res.data) {
          setWallets(res.data.app_wallets || [])
          setSubscriptions(res.data.app_subscriptions || [])
          const names = res.data.app_names || {}
          setAppNames(
            new Map(Object.entries(names).map(([id, name]) => [Number(id), name]))
          )
        }
      } catch (error) {
        handleServerError(error)
      }
      setLoading(false)
    }
    init()
  }, [])

  const appIds = useMemo(() => {
    const set = new Set<number>()
    for (const wallet of wallets) {
      if (wallet.app_id > 0) set.add(wallet.app_id)
    }
    for (const sub of subscriptions) {
      const appId = sub?.subscription?.app_id
      if (appId && appId > 0) set.add(appId)
    }
    return [...set].sort((a, b) => a - b)
  }, [wallets, subscriptions])

  if (loading) {
    return (
      <TitledCard
        title={t('Subscriptions & Quota by App')}
        disableHoverEffect
        contentClassName='space-y-4'
      >
        <Skeleton className='h-20 w-full' />
        <Skeleton className='h-20 w-full' />
      </TitledCard>
    )
  }

  if (appIds.length === 0) {
    return null
  }

  return (
    <TitledCard
      title={t('Subscriptions & Quota by App')}
      disableHoverEffect
      contentClassName='space-y-4'
    >
      {appIds.map((appId) => {
        const wallet = wallets.find((w) => w.app_id === appId)
        const subs = subscriptions.filter(
          (s) => s?.subscription?.app_id === appId
        )
        const name = appNames.get(appId) || `#${appId}`
        const quota = Number(wallet?.quota || 0)
        const usedQuota = Number(wallet?.used_quota || 0)
        const remaining = Math.max(0, quota - usedQuota)
        const usagePercent = quota > 0 ? Math.round((usedQuota / quota) * 100) : 0

        return (
          <div
            key={appId}
            className='rounded-xl border p-3 sm:p-4'
          >
            <div className='mb-3 flex items-center justify-between gap-2'>
              <span className='text-sm font-semibold'>{name}</span>
              {appNames.has(appId) && (
                <StatusBadge
                  label={`#${appId}`}
                  variant='neutral'
                  copyable={false}
                />
              )}
            </div>

            <div className='text-muted-foreground text-xs'>
              {t('Balance')}:{' '}
              {quota > 0 ? (
                <>
                  {formatQuota(usedQuota)}/{formatQuota(quota)} ·{' '}
                  {t('Remaining')} {formatQuota(remaining)}
                  <span className='ml-2'>
                    {t('Used')} {usagePercent}%
                  </span>
                </>
              ) : (
                '0'
              )}
            </div>
            {quota > 0 && <Progress value={usagePercent} className='mt-2 h-1.5' />}

            {subs.length > 0 && (
              <div className='mt-3 space-y-2'>
                {subs.map((sub) => {
                  const subscription = sub?.subscription
                  const totalAmount = Number(subscription?.amount_total || 0)
                  const usedAmount = Number(subscription?.amount_used || 0)
                  const remainAmount =
                    totalAmount > 0
                      ? Math.max(0, totalAmount - usedAmount)
                      : 0
                  const now = Date.now() / 1000
                  const isExpired = (subscription?.end_time || 0) < now
                  const isCancelled = subscription?.status === 'cancelled'
                  const isActive =
                    subscription?.status === 'active' && !isExpired
                  const remainDays = getRemainingDays(sub)

                  let statusBadge = (
                    <StatusBadge
                      label={t('Expired')}
                      variant='neutral'
                      copyable={false}
                    />
                  )
                  if (isActive) {
                    statusBadge = (
                      <StatusBadge
                        label={t('Active')}
                        variant='success'
                        copyable={false}
                      />
                    )
                  } else if (isCancelled) {
                    statusBadge = (
                      <StatusBadge
                        label={t('Cancelled')}
                        variant='neutral'
                        copyable={false}
                      />
                    )
                  }

                  let endTimeLabel = t('Expired at')
                  if (isActive) {
                    endTimeLabel = t('Until')
                  } else if (isCancelled) {
                    endTimeLabel = t('Cancelled at')
                  }

                  return (
                    <div
                      key={subscription?.id}
                      className='bg-background rounded-md border p-2.5 text-xs'
                    >
                      <div className='flex flex-wrap items-center justify-between gap-1.5'>
                        <div className='flex items-center gap-2'>
                          <span className='font-medium'>
                            {t('Subscription')} #{subscription?.id}
                          </span>
                          {statusBadge}
                        </div>
                        {isActive && (
                          <span className='text-muted-foreground'>
                            {t('{{count}} days remaining', { count: remainDays })}
                          </span>
                        )}
                      </div>
                      <div className='text-muted-foreground mt-1'>
                        {endTimeLabel}{' '}
                        {new Date(
                          (subscription?.end_time || 0) * 1000
                        ).toLocaleString()}
                      </div>
                      <div className='text-muted-foreground mt-1'>
                        {t('Total Quota')}:{' '}
                        {totalAmount > 0
                          ? `${formatQuota(usedAmount)}/${formatQuota(
                              totalAmount
                            )} · ${t('Remaining')} ${formatQuota(remainAmount)}`
                          : t('Unlimited')}
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        )
      })}
    </TitledCard>
  )
}
