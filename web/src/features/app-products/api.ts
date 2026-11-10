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
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  ApiResponse,
  AppProduct,
  AppProductList,
  AppProductOrderList,
  AppProductPayload,
} from './types'

export async function getAppProducts(params: {
  appId: number
  page: number
  pageSize: number
}): Promise<ApiResponse<AppProductList>> {
  const res = await api.get<ApiResponse<AppProductList>>('/api/app-products', {
    params: {
      app_id: params.appId,
      p: params.page,
      page_size: params.pageSize,
    },
  })
  return requireServerSuccess(res.data)
}

export async function createAppProduct(
  data: AppProductPayload
): Promise<ApiResponse<AppProduct>> {
  const res = await api.post<ApiResponse<AppProduct>>('/api/app-products', data)
  return requireServerSuccess(res.data)
}

export async function updateAppProduct(
  id: number,
  data: AppProductPayload
): Promise<ApiResponse<AppProduct>> {
  const res = await api.put<ApiResponse<AppProduct>>(
    `/api/app-products/${id}`,
    data
  )
  return requireServerSuccess(res.data)
}

export async function patchAppProductEnabled(
  id: number,
  enabled: boolean
): Promise<ApiResponse> {
  const res = await api.patch<ApiResponse>(`/api/app-products/${id}`, {
    enabled,
  })
  return requireServerSuccess(res.data)
}

export async function deleteAppProduct(id: number): Promise<ApiResponse> {
  const res = await api.delete<ApiResponse>(`/api/app-products/${id}`)
  return requireServerSuccess(res.data)
}

export async function getAppProductOrders(params: {
  appId: number
  page: number
  pageSize: number
}): Promise<ApiResponse<AppProductOrderList>> {
  const res = await api.get<ApiResponse<AppProductOrderList>>(
    '/api/app-products/orders',
    {
      params: {
        app_id: params.appId,
        p: params.page,
        page_size: params.pageSize,
      },
    }
  )
  return requireServerSuccess(res.data)
}
