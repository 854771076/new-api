export interface OAuthApp {
  id: number
  name: string
  client_id: string
  redirect_uris: string[]
  post_logout_redirect_uris: string[]
  scopes: string[]
  status: number
  created_by: number
  created_at: number
  updated_at: number
}

export interface CreateOAuthAppRequest {
  name: string
  redirect_uris: string[]
  post_logout_redirect_uris: string[]
  scopes: string[]
}

export interface CreateOAuthAppResponse {
  success: boolean
  data: OAuthApp
  client_secret: string
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
