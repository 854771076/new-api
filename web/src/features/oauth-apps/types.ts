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
export interface OAuthApp {
  client_secret_configured?: boolean
  proxy_issuer?: string
  api_key_enabled?: boolean
  oauth_enabled?: boolean
  quota_per_credit?: number | null
  marketplace_visible?: boolean
  marketplace_description?: string
  marketplace_url?: string
  marketplace_sort?: number

  id: number
  name: string
  logo_url: string
  client_id: string
  redirect_uris: string[]
  post_logout_redirect_uris: string[]
  scopes: string[]
  status: number
  public_client: boolean
  waffo_pancake_return_url?: string
  created_by: number
  created_at: number
  updated_at: number
}

export interface CreateOAuthAppRequest {
  proxy_issuer?: string
  api_key_enabled?: boolean
  oauth_enabled?: boolean
  quota_per_credit?: number | null
  marketplace_visible?: boolean
  marketplace_description?: string
  marketplace_url?: string
  marketplace_sort?: number

  name: string
  logo_url: string
  redirect_uris: string[]
  post_logout_redirect_uris: string[]
  scopes: string[]
  public_client: boolean
  waffo_pancake_return_url: string
}

export interface CreateOAuthAppResponse {
  success: boolean
  data: OAuthApp
  client_secret?: string
}

export interface OAuthConsentContext {
  name: string
  logo_url: string
  csrf_token: string
}

export const OAUTH_SCOPES = [
  { value: 'openid', label: 'Verify your identity' },
  { value: 'profile', label: 'View username and display name' },
  { value: 'email', label: 'View email address' },
  { value: 'api:read', label: 'Read AI models and usage data' },
  { value: 'api:write', label: 'Make AI API requests' },
] as const

export const OAUTH_APP_STATUS = {
  ENABLED: 1,
  DISABLED: 2,
} as const
