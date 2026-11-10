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
import type { TFunction } from 'i18next'
import { expect, it, vi } from 'vitest'

import { getAdminPlans } from '@/features/subscriptions/api'
import { getUsers } from '@/features/users/api'
import { api } from '@/lib/api'

import { getAppProducts } from '../api'
import { getAppProductFormSchema } from '../lib/product-form'

const t = ((key: string) => key) as unknown as TFunction

it('requests admin plans for the selected app', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: [] } })

  await getAdminPlans(7)
  expect(get).toHaveBeenCalledWith('/api/subscription/admin/plans', {
    params: { app_id: 7 },
  })
})

it('requests the paginated app product list for the selected app', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [], total: 0, page: 2, page_size: 10 },
    },
  })

  const res = await getAppProducts({ appId: 7, page: 2, pageSize: 10 })

  expect(get).toHaveBeenCalledWith('/api/app-products', {
    params: { app_id: 7, p: 2, page_size: 10 },
  })
  expect(res.data?.items).toEqual([])
  expect(res.data?.total).toBe(0)
})

it('requests users with the selected app id', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })

  await getUsers({ p: 1, page_size: 10, app_id: 7 })
  expect(get).toHaveBeenCalledWith('/api/user/', {
    params: expect.objectContaining({ app_id: 7 }),
  })
})

it('requires a product name and a positive quota', () => {
  const schema = getAppProductFormSchema(t)

  expect(
    schema.safeParse({
      name: ' ',
      price_amount: 0,
      currency: 'USD',
      quota_amount: 100,
      max_purchase_per_user: 0,
      sort_order: 0,
      enabled: true,
    }).success
  ).toBe(false)

  expect(
    schema.safeParse({
      name: 'Premium Pack',
      price_amount: 0,
      currency: 'USD',
      quota_amount: 0,
      max_purchase_per_user: 0,
      sort_order: 0,
      enabled: true,
    }).success
  ).toBe(false)

  expect(
    schema.safeParse({
      name: 'Premium Pack',
      price_amount: 0,
      currency: 'USD',
      quota_amount: 100,
      max_purchase_per_user: 0,
      sort_order: 0,
      enabled: true,
    }).success
  ).toBe(true)
})
