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
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatQuota } from '@/lib/format'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { useSystemConfig } from '@/hooks/use-system-config'
import { requireServerSuccess } from '@/lib/server-error-message'
import { formatDuration, formatResetPeriod } from '@/features/subscriptions/lib'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'
import { getDiscountLabel } from '@/features/wallet/lib'
import { useAuthStore } from '@/stores/auth-store'

import { getPublicPricingPlans } from '../api'

export function SubscriptionTiersSection() {
  const { t } = useTranslation()
  const { currency } = useSystemConfig()
  const { auth } = useAuthStore()
  const navigate = useNavigate()
  const isAuthenticated = !!auth.user

  const quotaPerUnit =
    currency?.quotaPerUnit && currency.quotaPerUnit > 0
      ? currency.quotaPerUnit
      : DEFAULT_CURRENCY_CONFIG.quotaPerUnit

  const { data, isLoading } = useQuery({
    queryKey: ['pricing-plans'],
    queryFn: async () => requireServerSuccess(await getPublicPricingPlans()),
  })

  const plans = data?.data?.plans ?? []
  const amountOptions = data?.data?.topup_amount_options ?? []
  const discountMap = data?.data?.topup_discount ?? {}

  const handlePurchase = () => {
    if (isAuthenticated) {
      void navigate({ to: '/wallet' })
    } else {
      void navigate({ to: '/sign-in', search: { redirect: '/pricing' } })
    }
  }

  if (isLoading) {
    return <Skeleton className='h-40 w-full' />
  }

  if (plans.length === 0 && amountOptions.length === 0) {
    return null
  }

  return (
    <section className='space-y-8'>
      {plans.length > 0 && (
        <div className='space-y-4'>
          <h2 className='text-xl font-semibold tracking-tight'>
            {t('Subscription Plans')}
          </h2>
          <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
            {plans.map(({ plan }) => {
              const quota =
                plan.total_amount > 0
                  ? formatQuota(plan.total_amount)
                  : t('Unlimited')
              const resetLabel = formatResetPeriod(plan, t)
              return (
                <Card
                  key={plan.id}
                  className='hover:ring-foreground/20 h-full transition-colors'
                >
                  <CardHeader>
                    <CardTitle className='text-base font-semibold'>
                      {plan.title}
                    </CardTitle>
                    {plan.subtitle ? (
                      <CardDescription>{plan.subtitle}</CardDescription>
                    ) : null}
                  </CardHeader>
                  <CardContent className='flex flex-1 flex-col gap-4'>
                    <div>
                      <div className='text-3xl font-bold tracking-tight'>
                        {formatBillingCurrencyFromUSD(plan.price_amount)}
                      </div>
                      <div className='text-muted-foreground mt-1 text-sm'>
                        {t('Billing period')}: {formatDuration(plan, t)}
                      </div>
                    </div>
                    <dl className='text-muted-foreground space-y-1.5 text-sm'>
                      <div className='flex items-center justify-between gap-2'>
                        <dt>{t('Included quota')}</dt>
                        <dd className='text-foreground font-medium'>
                          {quota}
                        </dd>
                      </div>
                      {resetLabel && resetLabel !== t('No Reset') ? (
                        <div>{resetLabel}</div>
                      ) : null}
                    </dl>
                    <p className='text-muted-foreground/80 text-xs'>
                      {t('One-time purchase, non-auto-renewing')}
                    </p>
                  </CardContent>
                  <CardFooter className='mt-auto border-0 bg-transparent pt-0'>
                    <Button className='w-full' onClick={handlePurchase}>
                      {t('Subscribe Now')}
                    </Button>
                  </CardFooter>
                </Card>
              )
            })}
          </div>
        </div>
      )}

      {amountOptions.length > 0 && (
        <div className='space-y-4'>
          <h2 className='text-xl font-semibold tracking-tight'>
            {t('Top-up')}
          </h2>
          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'>
            {amountOptions.map((amount) => {
              const discount = discountMap[amount] ?? 1
              const hasDiscount = discount < 1
              return (
                <Card
                  key={amount}
                  className='hover:ring-foreground/20 h-full transition-colors'
                >
                  <CardContent className='flex flex-col gap-1'>
                    <div className='flex items-center gap-2'>
                      <span className='text-lg font-bold'>
                        {formatBillingCurrencyFromUSD(amount)}
                      </span>
                      {hasDiscount ? (
                        <span className='text-xs font-medium text-green-600'>
                          {getDiscountLabel(discount, t)}
                        </span>
                      ) : null}
                    </div>
                    <div className='text-muted-foreground text-xs'>
                      {t('Included quota')}: {formatQuota(amount * quotaPerUnit)}
                    </div>
                  </CardContent>
                  <CardFooter className='mt-auto border-0 bg-transparent pt-0'>
                    <Button
                      variant='outline'
                      size='sm'
                      className='w-full'
                      onClick={handlePurchase}
                    >
                      {t('Top up now')}
                    </Button>
                  </CardFooter>
                </Card>
              )
            })}
          </div>
        </div>
      )}

      <p className='text-muted-foreground/70 text-xs'>
        {t(
          'Quota and prices shown are based on the default group ratio; exact amounts appear after login.'
        )}
      </p>
    </section>
  )
}
