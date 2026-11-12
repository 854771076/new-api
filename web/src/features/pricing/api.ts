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
import { api } from '@/lib/api'

import type { PricingData } from './types'

// ----------------------------------------------------------------------------
// Pricing APIs
// ----------------------------------------------------------------------------

// Get model pricing data
export async function getPricing(): Promise<PricingData> {
  const res = await api.get('/api/pricing')
  return res.data
}

// Reduced subscription-plan view served by /api/pricing/plans to anonymous
// visitors. Mirrors the backend PublicPricingPlan (internal config fields such
// as payment-provider ids and admin-only groups are intentionally absent).
export type PublicPricingPlan = {
  id: number
  title: string
  subtitle?: string
  price_amount: number
  currency: string
  duration_unit: 'year' | 'month' | 'day' | 'hour' | 'custom'
  duration_value: number
  custom_seconds?: number
  total_amount: number
  quota_reset_period: 'never' | 'daily' | 'weekly' | 'monthly' | 'custom'
  quota_reset_custom_seconds?: number
}

export type PublicPricingPlanRecord = {
  plan: PublicPricingPlan
}

export type PublicPricingPlansData = {
  success: boolean
  message?: string
  data: {
    plans: PublicPricingPlanRecord[]
    topup_amount_options: number[]
    topup_discount: Record<string, number>
  }
}

// Get subscription plans + top-up tiers for public pricing disclosure
export async function getPublicPricingPlans(): Promise<PublicPricingPlansData> {
  const res = await api.get('/api/pricing/plans')
  return res.data
}
