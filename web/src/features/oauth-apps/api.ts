import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  CreateOAuthAppRequest,
  CreateOAuthAppResponse,
  OAuthApp,
} from './types'

interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export async function getOAuthApps(): Promise<ApiResponse<OAuthApp[]>> {
  const res = await api.get<ApiResponse<OAuthApp[]>>('/api/oauth-apps/')
  return requireServerSuccess(res.data)
}

export async function getOAuthApp(id: number): Promise<ApiResponse<OAuthApp>> {
  const res = await api.get<ApiResponse<OAuthApp>>(`/api/oauth-apps/${id}`)
  return requireServerSuccess(res.data)
}

export async function createOAuthApp(
  data: CreateOAuthAppRequest
): Promise<CreateOAuthAppResponse> {
  const res = await api.post<CreateOAuthAppResponse>('/api/oauth-apps/', data)
  return requireServerSuccess(res.data)
}

export async function updateOAuthApp(
  id: number,
  data: Partial<CreateOAuthAppRequest> & { status?: number }
): Promise<ApiResponse<OAuthApp>> {
  const res = await api.put<ApiResponse<OAuthApp>>(
    `/api/oauth-apps/${id}`,
    data
  )
  return requireServerSuccess(res.data)
}

export async function rotateOAuthAppSecret(
  id: number
): Promise<{ success: boolean; client_secret: string }> {
  const res = await api.post<{ success: boolean; client_secret: string }>(
    `/api/oauth-apps/${id}/rotate-secret`
  )
  return requireServerSuccess(res.data)
}

export async function deleteOAuthApp(id: number): Promise<ApiResponse> {
  const res = await api.delete<ApiResponse>(`/api/oauth-apps/${id}`)
  return requireServerSuccess(res.data)
}
