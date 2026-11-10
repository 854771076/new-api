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
import { z } from 'zod'

import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'

import type { AppProduct, AppProductPayload } from '../types'

export function getAppProductFormSchema(t: TFunction) {
  return z.object({
    name: z.string().trim().min(1, t('Product name is required')),
    description: z.string().optional(),
    price_amount: z.coerce.number().min(0, t('Please enter amount')),
    currency: z.string().min(1),
    quota_amount: z.coerce
      .number()
      .min(0.01, t('Product quota must be greater than zero')),
    max_purchase_per_user: z.coerce.number().int().min(0),
    sort_order: z.coerce.number().int(),
    enabled: z.boolean(),
    stripe_price_id: z.string().optional(),
    creem_product_id: z.string().optional(),
    waffo_pancake_product_id: z.string().optional(),
  })
}

export type AppProductFormValues = z.infer<
  ReturnType<typeof getAppProductFormSchema>
>

export const APP_PRODUCT_FORM_DEFAULTS: AppProductFormValues = {
  name: '',
  description: '',
  price_amount: 0,
  currency: 'USD',
  quota_amount: 0,
  max_purchase_per_user: 0,
  sort_order: 0,
  enabled: true,
  stripe_price_id: '',
  creem_product_id: '',
  waffo_pancake_product_id: '',
}

export function productToFormValues(product: AppProduct): AppProductFormValues {
  return {
    name: product.name || '',
    description: product.description || '',
    price_amount: Number(product.price_amount || 0),
    currency: product.currency || 'USD',
    quota_amount: quotaUnitsToDollars(Number(product.quota_amount || 0)),
    max_purchase_per_user: Number(product.max_purchase_per_user || 0),
    sort_order: Number(product.sort_order || 0),
    enabled: product.enabled !== false,
    stripe_price_id: product.stripe_price_id || '',
    creem_product_id: product.creem_product_id || '',
    waffo_pancake_product_id: product.waffo_pancake_product_id || '',
  }
}

export function formValuesToProductPayload(
  values: AppProductFormValues,
  appId: number
): AppProductPayload {
  return {
    app_id: appId,
    name: values.name.trim(),
    description: values.description || '',
    price_amount: Number(values.price_amount || 0),
    currency: values.currency || 'USD',
    quota_amount: parseQuotaFromDollars(Number(values.quota_amount || 0)),
    max_purchase_per_user: Number(values.max_purchase_per_user || 0),
    sort_order: Number(values.sort_order || 0),
    enabled: values.enabled,
    stripe_price_id: values.stripe_price_id || '',
    creem_product_id: values.creem_product_id || '',
    waffo_pancake_product_id: values.waffo_pancake_product_id || '',
  }
}
