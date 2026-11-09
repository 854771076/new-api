import { api } from '@/lib/api'

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
  const res = await api.get('/api/oauth-apps/')
  return res.data
}

export async function getOAuthApp(id: number): Promise<ApiResponse<OAuthApp>> {
  const res = await api.get(`/api/oauth-apps/${id}`)
  return res.data
}

export async function createOAuthApp(
  data: CreateOAuthAppRequest,
): Promise<CreateOAuthAppResponse> {
  const res = await api.post('/api/oauth-apps/', data)
  return res.data
}

export async function updateOAuthApp(
  id: number,
  data: Partial<CreateOAuthAppRequest> & { status?: number },
): Promise<ApiResponse<OAuthApp>> {
  const res = await api.put(`/api/oauth-apps/${id}`, data)
  return res.data
}

export async function rotateOAuthAppSecret(
  id: number,
): Promise<{ success: boolean; client_secret: string }> {
  const res = await api.post(`/api/oauth-apps/${id}/rotate-secret`)
  return res.data
}

export async function deleteOAuthApp(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/oauth-apps/${id}`)
  return res.data
}
