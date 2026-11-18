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
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  createScopedCheckout,
  readPendingCheckout,
  refreshScopedOrder,
  type AppWalletData,
} from '../app-wallet-api'
import { WalletStatsCard } from '../components/wallet-stats-card'

beforeEach(() => {
  sessionStorage.clear()
  vi.restoreAllMocks()
})

const purchase = {
  app_id: 7,
  purchase_type: 'topup' as const,
  payment_method: 'waffo_pancake',
  amount: 10,
}
const order = {
  order_id: 'APP-REQUEST-once',
  app_id: 7,
  user_id: 42,
  status: 'pending',
}

test('a lost checkout response reuses the request ID and reload restores its exact App order', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('connection lost'))
    .mockResolvedValue({ data: order })
  await expect(createScopedCheckout(42, purchase)).rejects.toThrow(
    'connection lost'
  )
  await expect(createScopedCheckout(42, purchase)).resolves.toEqual(order)
  expect(post.mock.calls[0][2]?.headers).toEqual(post.mock.calls[1][2]?.headers)
  expect(readPendingCheckout(42, 7)).toEqual(order)
  expect(readPendingCheckout(42, 8)).toBeNull()
  expect(readPendingCheckout(43, 7)).toBeNull()
})

test('only the exact terminal order releases its purchase request ID', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: order })
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: order } })
  await createScopedCheckout(42, purchase)
  const firstId = post.mock.calls[0][2]?.headers?.['Idempotency-Key']
  await refreshScopedOrder(42, order)
  await createScopedCheckout(42, purchase)
  expect(post.mock.calls[1][2]?.headers?.['Idempotency-Key']).toBe(firstId)
  get.mockResolvedValueOnce({
    data: { success: true, data: { ...order, app_id: 8, status: 'success' } },
  })
  await expect(refreshScopedOrder(42, order)).rejects.toThrow(
    'Invalid order response'
  )
  get.mockResolvedValueOnce({
    data: { success: true, data: { ...order, status: 'success' } },
  })
  await refreshScopedOrder(42, order)
  await createScopedCheckout(42, purchase)
  expect(post.mock.calls[2][2]?.headers?.['Idempotency-Key']).not.toBe(firstId)
})

test('business failure never creates a successful receipt', async () => {
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'rejected' },
  })
  await expect(createScopedCheckout(42, purchase)).rejects.toThrow()
  expect(readPendingCheckout(42, 7)).toBeNull()
})

test('wallet displays remaining credits without subtracting usage and preserves tiny debt', () => {
  const wallet: AppWalletData = {
    user_id: 42,
    app_id: 7,
    identity_source: 'proxy',
    quota: 1000,
    used_quota: 200,
    quota_per_credit: 100,
    credits: '10',
    used_credits: '2',
    request_count: null,
    statistics_window: null,
    capabilities: { subscription_supported: false },
  }
  const markup = renderToStaticMarkup(
    createElement(WalletStatsCard, { user: null, wallet })
  )
  expect(markup).toContain('10 Credits')
  expect(markup).toContain('2 Credits')
  expect(markup).toContain('No Data')
  const debt = renderToStaticMarkup(
    createElement(WalletStatsCard, {
      user: null,
      wallet: { ...wallet, quota: -1, credits: '0' },
    })
  )
  expect(debt).toContain('Debt below 0.000001 credits')
})
