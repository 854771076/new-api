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
import type { AppProduct } from '@/features/app-products/types'
import type {
  SubscriptionPlan,
  UserSubscriptionRecord,
} from '@/features/subscriptions/types'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export interface AppAccess {
  id: number
  name: string
  logo_url: string
  status: number
  oauth_enabled: boolean
  read_only: boolean
}
export interface AppWalletData {
  user_id: number
  app_id: number
  identity_source: string
  quota: number
  used_quota: number
  quota_per_credit: number | null
  credits: string | null
  used_credits: string | null
  request_count: number | null
  statistics_window: string | null
  capabilities: { subscription_supported: boolean }
}
export interface AppCatalog {
  app_id: number
  plans: SubscriptionPlan[]
  subscriptions: UserSubscriptionRecord[]
  products: (AppProduct & { credits: string | null })[]
}
export interface ScopedPurchase {
  app_id: number
  purchase_type: 'topup' | 'product' | 'subscription'
  payment_method: string
  amount?: number
  product_id?: number
  plan_id?: number
}
export interface ScopedCheckout {
  order_id: string
  app_id: number
  user_id: number
  status: string
  checkout_url?: string
  pay_link?: string
  url?: string
  params?: Record<string, string>
  success?: boolean
  error?: string
  error_description?: string
  quota_per_credit?: number | null
  credited_quota?: number
  credits?: string | null
  money?: string
  currency?: string
}
export interface ScopedOrder {
  order_id: string
  app_id: number
  status: string
  money: number
  currency: string
  purchase_type: string
  credits: string | null
  quota: number | null
  created_at: number
}
export async function getSelfApps(historical = false) {
  return (
    await api.get<{ success: boolean; data: AppAccess[] }>(
      '/api/user/self/apps',
      { params: { include_historical: historical } }
    )
  ).data
}
export async function getAppWallet(appId: number): Promise<AppWalletData> {
  return requireServerSuccess(
    (await api.get('/api/user/self/wallet', { params: { app_id: appId } })).data
  ).data
}
export async function getAppCatalog(appId: number): Promise<AppCatalog> {
  return requireServerSuccess(
    (await api.get('/api/user/self/app-catalog', { params: { app_id: appId } }))
      .data
  ).data
}
export async function getAppOrders(
  appId: number,
  page: number,
  keyword: string,
  pageSize = 10
) {
  return requireServerSuccess(
    (
      await api.get<{
        success: boolean
        data: { items: ScopedOrder[]; total: number }
      }>('/api/user/self/orders', {
        params: { app_id: appId, p: page, page_size: pageSize, keyword },
      })
    ).data
  ).data
}
export async function createScopedCheckout(
  userId: number,
  body: ScopedPurchase
): Promise<ScopedCheckout> {
  // Save the request key before sending. An uncertain request reuses this key.
  const storageKey = `app-checkout:${userId}:${JSON.stringify(body)}`
  let key = sessionStorage.getItem(storageKey)
  if (!key) {
    key = crypto.randomUUID()
    sessionStorage.setItem(storageKey, key)
  }
  const result = (
    await api.post<ScopedCheckout>('/api/user/self/app-checkout', body, {
      headers: { 'Idempotency-Key': key },
    })
  ).data
  if (result.error || result.success === false) {
    throw new Error(result.error_description || 'Payment request failed')
  }
  if (!result.order_id) throw new Error('Order confirmation is pending')
  // Remember the immutable association even if the active App later changes.
  sessionStorage.setItem(
    `app-order:${userId}:${result.order_id}`,
    JSON.stringify({
      app_id: body.app_id,
      order_id: result.order_id,
      storageKey,
    })
  )
  // Keep uncertain and payable orders attached to the same request across reloads.
  if (['success', 'failed', 'expired', 'cancelled'].includes(result.status)) {
    sessionStorage.removeItem(storageKey)
  }
  sessionStorage.setItem(
    `app-pending:${userId}:${body.app_id}`,
    JSON.stringify(result)
  )
  return result
}

export function readPendingCheckout(
  userId: number | undefined,
  appId: number
): ScopedCheckout | null {
  try {
    const value = JSON.parse(
      sessionStorage.getItem(`app-pending:${userId}:${appId}`) ?? 'null'
    )
    return value?.app_id === appId && typeof value.order_id === 'string'
      ? value
      : null
  } catch {
    return null
  }
}
export async function refreshScopedOrder(
  userId: number,
  order: ScopedCheckout
): Promise<ScopedCheckout> {
  const result = requireServerSuccess(
    (
      await api.get(
        `/api/user/self/orders/${encodeURIComponent(order.order_id)}`,
        { params: { app_id: order.app_id } }
      )
    ).data
  ).data
  if (
    result.order_id !== order.order_id ||
    result.app_id !== order.app_id ||
    result.user_id !== userId
  ) {
    throw new Error('Invalid order response')
  }
  const updated = { ...order, status: result.status }
  if (['success', 'failed', 'expired', 'cancelled'].includes(updated.status)) {
    const association = JSON.parse(
      sessionStorage.getItem(`app-order:${userId}:${order.order_id}`) ?? 'null'
    )
    if (association?.app_id === order.app_id && association?.storageKey) {
      sessionStorage.removeItem(association.storageKey)
    }
  }
  sessionStorage.setItem(
    `app-pending:${userId}:${order.app_id}`,
    JSON.stringify(updated)
  )
  return updated
}
