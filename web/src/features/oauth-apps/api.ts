import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  CreateOAuthAppRequest,
  CreateOAuthAppResponse,
  OAuthApp,
  OAuthConsentContext,
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

// Loads the consent page metadata and a CSRF token bound to the current
// dashboard session. The page must not trust the client name from the URL.
export async function getOAuthConsentContext(
  clientId: string
): Promise<OAuthConsentContext> {
  const res = await api.get<ApiResponse<OAuthConsentContext>>(
    '/oauth/consent/context',
    {
      params: { client_id: clientId },
      skipErrorHandler: true,
    }
  )
  const data = requireServerSuccess(res.data).data
  if (!data) {
    throw new Error('Missing consent context')
  }
  return data
}
