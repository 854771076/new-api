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
export interface AppProduct {
  id: number
  app_id: number
  name: string
  description: string
  price_amount: number
  currency: string
  quota_amount: number
  enabled: boolean
  sort_order: number
  max_purchase_per_user: number
  stripe_price_id: string
  creem_product_id: string
  waffo_pancake_product_id: string
  created_at: number
  updated_at: number
}

export interface AppProductOrder {
  id: number
  user_id: number
  app_id: number
  product_id: number
  money: number
  price_amount: number
  currency: string
  quota_amount: number
  trade_no: string
  payment_method: string
  payment_provider: string
  status: string
  create_time: number
  complete_time: number
}

export interface AppProductList {
  items: AppProduct[]
  total: number
  page: number
  page_size: number
}

export interface AppProductOrderList {
  items: AppProductOrder[]
  total: number
  page: number
  page_size: number
}

export interface AppProductPayload {
  app_id: number
  name: string
  description: string
  price_amount: number
  currency: string
  quota_amount: number
  enabled: boolean
  sort_order: number
  max_purchase_per_user: number
  stripe_price_id: string
  creem_product_id: string
  waffo_pancake_product_id: string
}

export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}
