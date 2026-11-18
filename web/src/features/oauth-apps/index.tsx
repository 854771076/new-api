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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { type ChangeEvent, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  createOAuthApp,
  deleteOAuthApp,
  getOAuthApps,
  rotateOAuthAppSecret,
  updateOAuthApp,
} from './api'
import { OAUTH_APP_STATUS, OAUTH_SCOPES, type OAuthApp } from './types'

export function OAuthAppsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [modeFilter, setModeFilter] = useState('all')
  const [createOpen, setCreateOpen] = useState(false)
  const [editApp, setEditApp] = useState<OAuthApp | null>(null)
  const [secretRecoveryApp, setSecretRecoveryApp] = useState<OAuthApp | null>(
    null
  )
  const [secretApp, setSecretApp] = useState<{
    name: string
    secret: string
    publicClient: boolean
  } | null>(null)

  const {
    data: apps,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ['oauth-apps'],
    queryFn: async () => {
      const res = await getOAuthApps()
      return res.data ?? []
    },
  })

  const createMutation = useMutation({
    mutationFn: createOAuthApp,
    onSuccess: (res) => {
      toast.success(t('App created'))
      if (res.data.oauth_enabled !== false) {
        setSecretApp({
          name: res.data.name,
          secret: res.client_secret ?? '',
          publicClient: res.data.public_client,
        })
      }
      setCreateOpen(false)
      queryClient.invalidateQueries({ queryKey: ['oauth-apps'] })
    },
    onError: (error) => handleServerError(error, t('Failed to create app')),
  })

  const rotateMutation = useMutation({
    mutationFn: async (variables: {
      app: OAuthApp
      finishUpdate?: boolean
    }) => {
      const res = await rotateOAuthAppSecret(variables.app.id)
      if (!res.client_secret) throw new Error(t('Failed to rotate secret'))
      return res
    },
    onSuccess: (res, variables) => {
      setSecretApp({
        name: variables.app.name,
        secret: res.client_secret,
        publicClient: false,
      })
      if (variables.finishUpdate) {
        toast.success(t('App updated'))
        setEditApp(null)
        setSecretRecoveryApp(null)
      }
    },
    onError: (error) => handleServerError(error, t('Failed to rotate secret')),
  })

  const updateMutation = useMutation({
    mutationFn: (variables: {
      app: OAuthApp
      data: Parameters<typeof updateOAuthApp>[1]
    }) => updateOAuthApp(variables.app.id, variables.data),
    onSuccess: async (res, variables) => {
      void queryClient.invalidateQueries({ queryKey: ['oauth-apps'] })
      if (
        (variables.app.public_client ||
          res.data?.client_secret_configured === false) &&
        variables.data.oauth_enabled !== false &&
        variables.data.public_client === false
      ) {
        const savedApp = res.data ?? { ...variables.app, ...variables.data }
        setEditApp(savedApp)
        try {
          await rotateMutation.mutateAsync({
            app: savedApp,
            finishUpdate: true,
          })
        } catch {
          // The update is already persisted. Do not claim full success or
          // roll back blindly; keep the saved client available for rotation.
          setSecretRecoveryApp(savedApp)
        }
        return
      }
      toast.success(t('App updated'))
      setEditApp(null)
    },
    onError: (error) => handleServerError(error, t('Failed to update app')),
  })

  const deleteMutation = useMutation({
    mutationFn: deleteOAuthApp,
    onSuccess: () => {
      toast.success(t('App deleted'))
      queryClient.invalidateQueries({ queryKey: ['oauth-apps'] })
    },
    onError: (error) => handleServerError(error, t('Failed to delete app')),
  })

  return (
    <div className='container mx-auto space-y-4 py-6'>
      <div className='flex items-center justify-between'>
        <h1 className='text-2xl font-bold'>{t('OAuth Applications')}</h1>
        <Button onClick={() => setCreateOpen(true)}>{t('Create App')}</Button>
      </div>

      <Select
        value={modeFilter}
        onValueChange={(value) => setModeFilter(value ?? 'all')}
        items={[
          { value: 'all', label: t('All integration modes') },
          { value: 'proxy', label: t('API key proxy') },
          { value: 'oauth', label: t('OAuth login') },
          { value: 'dual', label: t('Dual mode') },
        ]}
      >
        <SelectTrigger className='w-52' aria-label={t('Integration mode')}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value='all'>{t('All integration modes')}</SelectItem>
          <SelectItem value='proxy'>{t('API key proxy')}</SelectItem>
          <SelectItem value='oauth'>{t('OAuth login')}</SelectItem>
          <SelectItem value='dual'>{t('Dual mode')}</SelectItem>
        </SelectContent>
      </Select>
      <div className='overflow-x-auto rounded-md border'>
        {isError ? (
          <ErrorState
            title={t('Failed to load OAuth apps')}
            description={error instanceof Error ? error.message : undefined}
            onRetry={() => void refetch()}
          />
        ) : (
          <table className='w-full text-sm'>
            <thead className='bg-muted/50 border-b'>
              <tr>
                <th className='px-4 py-3 text-left'>{t('Name')}</th>
                <th className='px-4 py-3 text-left'>{t('Client ID')}</th>
                <th className='px-4 py-3 text-left'>{t('Scopes')}</th>
                <th className='px-4 py-3 text-left'>{t('Status')}</th>
                <th className='px-4 py-3 text-left'>{t('Actions')}</th>
              </tr>
            </thead>
            <tbody>
              {isLoading && (
                <tr>
                  <td
                    colSpan={5}
                    className='text-muted-foreground px-4 py-8 text-center'
                  >
                    {t('Loading...')}
                  </td>
                </tr>
              )}
              {apps?.length === 0 && (
                <tr>
                  <td
                    colSpan={5}
                    className='text-muted-foreground px-4 py-8 text-center'
                  >
                    {t('No OAuth apps yet')}
                  </td>
                </tr>
              )}
              {apps
                ?.filter((app) => {
                  if (modeFilter === 'proxy') {
                    return app.api_key_enabled && app.oauth_enabled === false
                  }
                  if (modeFilter === 'oauth') {
                    return !app.api_key_enabled && app.oauth_enabled !== false
                  }
                  if (modeFilter === 'dual') {
                    return app.api_key_enabled && app.oauth_enabled !== false
                  }
                  return true
                })
                .map((app) => (
                  <tr key={app.id} className='border-b last:border-0'>
                    <td className='px-4 py-3 font-medium'>
                      <div className='flex items-center gap-2'>
                        {app.logo_url ? (
                          <img
                            src={app.logo_url}
                            alt=''
                            className='size-6 shrink-0 rounded border object-contain'
                          />
                        ) : null}
                        {app.name}
                        {app.api_key_enabled && (
                          <Badge variant='outline'>{t('API key proxy')}</Badge>
                        )}
                        {app.oauth_enabled !== false && (
                          <Badge variant='outline'>{t('OAuth login')}</Badge>
                        )}
                        {app.oauth_enabled !== false && app.public_client && (
                          <Badge variant='secondary'>
                            {t('Public Client')}
                          </Badge>
                        )}
                      </div>
                    </td>
                    <td className='px-4 py-3'>
                      <code className='text-xs'>{app.client_id}</code>
                    </td>
                    <td className='px-4 py-3'>
                      <div className='flex flex-wrap gap-1'>
                        {app.scopes.map((s) => (
                          <span
                            key={s}
                            className='bg-muted rounded px-1.5 py-0.5 text-xs'
                          >
                            {s}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className='px-4 py-3'>
                      <span
                        className={
                          app.status === OAUTH_APP_STATUS.ENABLED
                            ? 'text-green-600'
                            : 'text-red-600'
                        }
                      >
                        {app.status === OAUTH_APP_STATUS.ENABLED
                          ? t('Enabled')
                          : t('Disabled')}
                      </span>
                    </td>
                    <td className='px-4 py-3'>
                      <div className='flex gap-2'>
                        <Button
                          variant='outline'
                          size='sm'
                          disabled={
                            updateMutation.isPending || rotateMutation.isPending
                          }
                          onClick={() => {
                            setSecretRecoveryApp(null)
                            setEditApp(app)
                          }}
                        >
                          {t('Edit')}
                        </Button>
                        {app.oauth_enabled !== false && !app.public_client && (
                          <Button
                            variant='outline'
                            size='sm'
                            disabled={
                              rotateMutation.isPending ||
                              updateMutation.isPending
                            }
                            onClick={() => rotateMutation.mutate({ app })}
                          >
                            {t('Rotate Secret')}
                          </Button>
                        )}
                        <Button
                          variant='destructive'
                          size='sm'
                          disabled={deleteMutation.isPending}
                          onClick={() => {
                            if (
                              confirm(
                                t(
                                  'Delete this app? All tokens will be revoked.'
                                )
                              )
                            ) {
                              deleteMutation.mutate(app.id)
                            }
                          }}
                        >
                          {t('Delete')}
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        )}
      </div>

      <CreateAppDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSubmit={(data) => createMutation.mutate(data)}
        loading={createMutation.isPending}
      />

      {editApp && (
        <EditAppDialog
          app={editApp}
          onOpenChange={(open) => {
            if (
              !open &&
              !updateMutation.isPending &&
              !rotateMutation.isPending
            ) {
              setEditApp(null)
              setSecretRecoveryApp(null)
            }
          }}
          onSubmit={(data) => updateMutation.mutate({ app: editApp, data })}
          loading={updateMutation.isPending || rotateMutation.isPending}
          secretRecovery={secretRecoveryApp?.id === editApp.id}
          onRetrySecret={() =>
            rotateMutation.mutate({ app: editApp, finishUpdate: true })
          }
        />
      )}

      <Dialog open={!!secretApp} onOpenChange={(o) => !o && setSecretApp(null)}>
        <DialogContent>
          {secretApp?.publicClient ? (
            <>
              <DialogHeader>
                <DialogTitle>{t('Public Client')}</DialogTitle>
                <DialogDescription>
                  {t('Public client does not need a client secret')}
                </DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button onClick={() => setSecretApp(null)}>{t('Done')}</Button>
              </DialogFooter>
            </>
          ) : (
            <>
              <DialogHeader>
                <DialogTitle>{t('Client Secret')}</DialogTitle>
                <DialogDescription>
                  {t('Copy this secret now. It will not be shown again.')}
                </DialogDescription>
              </DialogHeader>
              <div className='flex items-center gap-2'>
                <Input
                  readOnly
                  value={secretApp?.secret ?? ''}
                  className='font-mono text-sm'
                />
                <CopyButton value={secretApp?.secret ?? ''} />
              </div>
              <DialogFooter>
                <Button onClick={() => setSecretApp(null)}>{t('Done')}</Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}

interface AppFormData {
  proxy_issuer: string
  api_key_enabled: boolean
  oauth_enabled: boolean
  quota_per_credit: number | null
  marketplace_visible: boolean
  marketplace_description: string
  marketplace_url: string
  marketplace_sort: number

  name: string
  logo_url: string
  redirect_uris: string[]
  post_logout_redirect_uris: string[]
  scopes: string[]
  public_client: boolean
  waffo_pancake_return_url: string
}

// Logos are stored in a TEXT column, so the encoded data URI must stay under
// 64 KB. The browser downscales the image and re-encodes it before checking.
const LOGO_MAX_DATA_URL_LENGTH = 60 * 1024
const LOGO_MAX_FILE_BYTES = 2 * 1024 * 1024
const LOGO_MAX_DIMENSION = 256
const LOGO_TYPES = ['image/png', 'image/jpeg', 'image/webp']

async function encodeLogoDataUrl(file: File): Promise<string> {
  const bitmap = await createImageBitmap(file)
  const scale = Math.min(
    1,
    LOGO_MAX_DIMENSION / Math.max(bitmap.width, bitmap.height)
  )
  const canvas = document.createElement('canvas')
  canvas.width = Math.max(1, Math.round(bitmap.width * scale))
  canvas.height = Math.max(1, Math.round(bitmap.height * scale))
  const context = canvas.getContext('2d')
  if (!context) {
    throw new Error('Canvas is not available')
  }
  context.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
  bitmap.close()
  // WebP keeps logos small; browsers without canvas WebP encoding fall back to
  // PNG, which the size check below may reject for large images.
  const webp = canvas.toDataURL('image/webp', 0.85)
  return webp.startsWith('data:image/webp')
    ? webp
    : canvas.toDataURL('image/png')
}

function AppForm({
  defaultValues,
  onSubmit,
  submitLabel,
  loading,
}: {
  defaultValues?: Partial<AppFormData>
  onSubmit: (data: AppFormData) => void
  submitLabel: string
  loading: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [proxyIssuer, setProxyIssuer] = useState(
    defaultValues?.proxy_issuer ?? ''
  )
  const [apiKeyEnabled, setAPIKeyEnabled] = useState(
    defaultValues?.api_key_enabled ?? !defaultValues
  )
  const [oauthEnabled, setOAuthEnabled] = useState(
    defaultValues?.oauth_enabled ?? !!defaultValues
  )
  const [creditRatio, setCreditRatio] = useState(
    defaultValues?.quota_per_credit?.toString() ?? ''
  )
  const [marketplaceVisible, setMarketplaceVisible] = useState(
    defaultValues?.marketplace_visible ?? false
  )
  const [marketplaceDescription, setMarketplaceDescription] = useState(
    defaultValues?.marketplace_description ?? ''
  )
  const [marketplaceURL, setMarketplaceURL] = useState(
    defaultValues?.marketplace_url ?? ''
  )
  const [marketplaceSort, setMarketplaceSort] = useState(
    defaultValues?.marketplace_sort ?? 0
  )
  const [pendingData, setPendingData] = useState<AppFormData | null>(null)
  const fieldID = useId()
  const ratio = creditRatio.trim() === '' ? null : Number(creditRatio)
  const ratioValid =
    ratio === null || (Number.isSafeInteger(ratio) && ratio > 0)
  const [name, setName] = useState(defaultValues?.name ?? '')
  const [logoUrl, setLogoUrl] = useState(defaultValues?.logo_url ?? '')
  const [logoError, setLogoError] = useState('')
  const logoInputRef = useRef<HTMLInputElement>(null)
  const logoInputId = useId()
  const [redirectUris, setRedirectUris] = useState(
    defaultValues?.redirect_uris?.join('\n') ?? ''
  )
  const [postLogoutUris, setPostLogoutUris] = useState(
    defaultValues?.post_logout_redirect_uris?.join('\n') ?? ''
  )
  const [scopes, setScopes] = useState<string[]>(defaultValues?.scopes ?? [])
  const [publicClient, setPublicClient] = useState(
    defaultValues?.public_client ?? false
  )
  const [waffoPancakeReturnUrl, setWaffoPancakeReturnUrl] = useState(
    defaultValues?.waffo_pancake_return_url ?? ''
  )
  const publicClientId = useId()

  const handleLogoChange = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setLogoError('')
    if (!LOGO_TYPES.includes(file.type)) {
      setLogoError(t('Logo must be a PNG, JPEG or WebP image'))
      return
    }
    if (file.size > LOGO_MAX_FILE_BYTES) {
      setLogoError(t('Logo is too large. Please choose a smaller image.'))
      return
    }
    void encodeLogoDataUrl(file)
      .then((dataUrl) => {
        if (dataUrl.length > LOGO_MAX_DATA_URL_LENGTH) {
          setLogoError(t('Logo is too large. Please choose a smaller image.'))
          return
        }
        setLogoUrl(dataUrl)
      })
      .catch(() => {
        setLogoError(t('Logo must be a PNG, JPEG or WebP image'))
      })
  }

  const handleSubmit = () => {
    if (loading) return
    if (!ratioValid || (!apiKeyEnabled && !oauthEnabled)) return
    const data: AppFormData = {
      proxy_issuer: proxyIssuer.trim(),
      api_key_enabled: apiKeyEnabled,
      oauth_enabled: oauthEnabled,
      quota_per_credit: ratio,
      marketplace_visible: marketplaceVisible,
      marketplace_description: marketplaceDescription.trim(),
      marketplace_url: marketplaceURL.trim(),
      marketplace_sort: marketplaceSort,
      name,
      logo_url: logoUrl,
      redirect_uris: redirectUris
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean),
      post_logout_redirect_uris: postLogoutUris
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean),
      scopes,
      public_client: publicClient,
      waffo_pancake_return_url: waffoPancakeReturnUrl.trim(),
    }
    if (defaultValues && ratio !== (defaultValues.quota_per_credit ?? null)) {
      setPendingData(data)
      return
    }
    onSubmit(data)
  }

  return (
    <div className='space-y-4'>
      <div>
        <Label htmlFor={logoInputId} className='mb-1 block text-sm font-medium'>
          {t('App Logo')}
        </Label>
        <div className='flex items-center gap-3'>
          {logoUrl ? (
            <img
              src={logoUrl}
              alt=''
              className='bg-background size-12 shrink-0 rounded-md border object-contain'
            />
          ) : (
            <div
              aria-hidden='true'
              className='bg-muted flex size-12 shrink-0 items-center justify-center rounded-md border text-lg font-semibold'
            >
              {(name || '?').slice(0, 1).toUpperCase()}
            </div>
          )}
          <input
            id={logoInputId}
            ref={logoInputRef}
            type='file'
            accept={LOGO_TYPES.join(',')}
            className='sr-only'
            disabled={loading}
            onChange={handleLogoChange}
          />
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={loading}
            onClick={() => logoInputRef.current?.click()}
          >
            {t('Upload')}
          </Button>
          {logoUrl ? (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              disabled={loading}
              onClick={() => {
                setLogoUrl('')
                setLogoError('')
              }}
            >
              {t('Remove')}
            </Button>
          ) : null}
        </div>
        {logoError ? (
          <p className='text-destructive mt-1 text-xs'>{logoError}</p>
        ) : null}
      </div>
      <div>
        <Label
          htmlFor={`${fieldID}-name`}
          className='mb-1 block text-sm font-medium'
        >
          {t('App Name')}
        </Label>
        <Input
          id={`${fieldID}-name`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder='My App'
          disabled={loading}
        />
      </div>
      <div>
        <Label
          htmlFor={`${fieldID}-return`}
          className='mb-1 block text-sm font-medium'
        >
          {t('Payment return URL (Waffo Pancake)')}
        </Label>
        <Input
          id={`${fieldID}-return`}
          value={waffoPancakeReturnUrl}
          onChange={(e) => setWaffoPancakeReturnUrl(e.target.value)}
          placeholder='https://example.com/payment/return'
          disabled={loading}
        />
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('Leave empty to use the global return URL')}
        </p>
      </div>
      <div className='space-y-3 rounded-md border p-3'>
        <div className='flex items-center justify-between gap-4'>
          <Label htmlFor={`${fieldID}-proxy`}>{t('API key proxy')}</Label>
          <Switch
            id={`${fieldID}-proxy`}
            checked={apiKeyEnabled}
            onCheckedChange={setAPIKeyEnabled}
            disabled={loading}
          />
        </div>
        <div className='flex items-center justify-between gap-4'>
          <Label htmlFor={`${fieldID}-oauth`}>{t('OAuth login')}</Label>
          <Switch
            id={`${fieldID}-oauth`}
            checked={oauthEnabled}
            onCheckedChange={setOAuthEnabled}
            disabled={loading}
          />
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Proxy and OAuth users have independent accounts and balances. Proxy mode has no subscriptions.'
          )}
        </p>
        {apiKeyEnabled && (
          <div className='space-y-2'>
            <Label htmlFor={`${fieldID}-issuer`}>
              {t('Trusted identity issuer')}
            </Label>
            <Input
              id={`${fieldID}-issuer`}
              type='url'
              value={proxyIssuer}
              onChange={(event) => setProxyIssuer(event.target.value)}
              placeholder='https://project.supabase.co/auth/v1'
              disabled={loading}
            />
          </div>
        )}
        {!apiKeyEnabled && !oauthEnabled && (
          <p role='alert' className='text-destructive text-xs'>
            {t('Enable at least one App integration mode')}
          </p>
        )}
      </div>
      <div className='space-y-2'>
        <Label htmlFor={`${fieldID}-ratio`}>{t('Quota per credit')}</Label>
        <Input
          id={`${fieldID}-ratio`}
          type='number'
          min={1}
          max={Number.MAX_SAFE_INTEGER}
          step={1}
          value={creditRatio}
          onChange={(event) => setCreditRatio(event.target.value)}
          disabled={loading}
          aria-invalid={!ratioValid}
        />
        <p className='text-muted-foreground text-xs'>
          {t('Leave empty to keep the original quota display')}
        </p>
        {ratioValid && ratio !== null && (
          <p className='text-muted-foreground text-xs'>
            {t('1 credit = {{quota}} quota', {
              quota: formatNumber(ratio, locale),
            })}
          </p>
        )}
        {!ratioValid && (
          <p role='alert' className='text-destructive text-xs'>
            {t('Invalid App credit ratio')}
          </p>
        )}
      </div>
      <div className='space-y-3 rounded-md border p-3'>
        <div className='flex items-center justify-between gap-4'>
          <Label htmlFor={`${fieldID}-visible`}>
            {t('Show in App marketplace')}
          </Label>
          <Switch
            id={`${fieldID}-visible`}
            checked={marketplaceVisible}
            onCheckedChange={setMarketplaceVisible}
            disabled={loading}
          />
        </div>
        <Label htmlFor={`${fieldID}-description`}>{t('App description')}</Label>
        <Textarea
          id={`${fieldID}-description`}
          maxLength={2000}
          value={marketplaceDescription}
          onChange={(event) => setMarketplaceDescription(event.target.value)}
          disabled={loading}
        />
        <Label htmlFor={`${fieldID}-url`}>{t('App visit URL')}</Label>
        <Input
          id={`${fieldID}-url`}
          type='url'
          placeholder='https://app.example.com'
          value={marketplaceURL}
          onChange={(event) => setMarketplaceURL(event.target.value)}
          disabled={loading}
        />
        <Label htmlFor={`${fieldID}-sort`}>{t('Display order')}</Label>
        <Input
          id={`${fieldID}-sort`}
          type='number'
          step={1}
          value={marketplaceSort}
          onChange={(event) => setMarketplaceSort(Number(event.target.value))}
          disabled={loading}
        />
      </div>
      {oauthEnabled && (
        <div className='space-y-4'>
          <div>
            <Label
              htmlFor={`${fieldID}-redirect`}
              className='mb-1 block text-sm font-medium'
            >
              {t('Redirect URIs (one per line)')}
            </Label>
            <Textarea
              id={`${fieldID}-redirect`}
              className='w-full rounded-md border p-2 text-sm'
              rows={3}
              value={redirectUris}
              onChange={(e) => setRedirectUris(e.target.value)}
              placeholder='https://api.example.com/oauth/callback'
              disabled={loading}
            />
          </div>
          <div>
            <Label
              htmlFor={`${fieldID}-logout`}
              className='mb-1 block text-sm font-medium'
            >
              {t('Post-Logout Redirect URIs (one per line)')}
            </Label>
            <Textarea
              id={`${fieldID}-logout`}
              className='w-full rounded-md border p-2 text-sm'
              rows={2}
              value={postLogoutUris}
              onChange={(e) => setPostLogoutUris(e.target.value)}
              placeholder='https://example.com/logout-done'
              disabled={loading}
            />
          </div>
          <div>
            <label className='mb-1 block text-sm font-medium'>
              {t('Allowed Scopes')}
            </label>
            <div className='space-y-2'>
              {OAUTH_SCOPES.map((scope) => (
                <label key={scope.value} className='flex items-start gap-2'>
                  <Checkbox
                    checked={scopes.includes(scope.value)}
                    disabled={loading}
                    onCheckedChange={(checked) => {
                      if (checked === true) {
                        setScopes([...scopes, scope.value])
                      } else {
                        setScopes(scopes.filter((s) => s !== scope.value))
                      }
                    }}
                  />
                  <div>
                    <code className='text-sm'>{scope.value}</code>
                    <p className='text-muted-foreground text-xs'>
                      {t(scope.label)}
                    </p>
                  </div>
                </label>
              ))}
            </div>
          </div>
          <div className='flex items-start justify-between gap-4 rounded-md border p-3'>
            <div className='space-y-1'>
              <Label htmlFor={publicClientId}>
                {t('Public Client (PKCE, no client secret)')}
              </Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Public clients authenticate with client ID only. PKCE is still required.'
                )}
              </p>
            </div>
            <Switch
              id={publicClientId}
              checked={publicClient}
              onCheckedChange={(checked) => setPublicClient(checked === true)}
              disabled={loading}
            />
          </div>
        </div>
      )}
      <DialogFooter>
        <Button
          onClick={handleSubmit}
          disabled={
            loading ||
            !name.trim() ||
            !ratioValid ||
            (!apiKeyEnabled && !oauthEnabled) ||
            (oauthEnabled && (scopes.length === 0 || !redirectUris.trim()))
          }
        >
          {loading ? t('Saving...') : submitLabel}
        </Button>
      </DialogFooter>
      <ConfirmDialog
        open={pendingData !== null}
        onOpenChange={(open) => !open && setPendingData(null)}
        title={t('Change credit definition?')}
        desc={t(
          'This changes the displayed credits of existing balances. Quota, prices and previous orders stay unchanged.'
        )}
        confirmText={t('Save')}
        handleConfirm={() => {
          if (pendingData) onSubmit(pendingData)
          setPendingData(null)
        }}
        isLoading={loading}
      />
    </div>
  )
}

function CreateAppDialog({
  open,
  onOpenChange,
  onSubmit,
  loading,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (data: AppFormData) => void
  loading: boolean
}) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Create OAuth Application')}</DialogTitle>
          <DialogDescription>
            {t('Register an external application to integrate with new-api.')}
          </DialogDescription>
        </DialogHeader>
        <AppForm
          onSubmit={onSubmit}
          submitLabel={t('Create')}
          loading={loading}
        />
      </DialogContent>
    </Dialog>
  )
}

function EditAppDialog({
  app,
  onOpenChange,
  onSubmit,
  loading,
  secretRecovery,
  onRetrySecret,
}: {
  app: OAuthApp
  onOpenChange: (open: boolean) => void
  onSubmit: (data: AppFormData) => void
  loading: boolean
  secretRecovery: boolean
  onRetrySecret: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className='max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Edit OAuth Application')}</DialogTitle>
        </DialogHeader>
        {secretRecovery ? (
          <ErrorState
            title={t('Failed to rotate secret')}
            description={t(
              'App saved, but no client secret was issued. Rotate the secret before using this confidential client.'
            )}
            action={
              <Button
                variant='outline'
                size='sm'
                disabled={loading}
                onClick={onRetrySecret}
              >
                {t('Retry')}
              </Button>
            }
          />
        ) : (
          <AppForm
            defaultValues={{
              proxy_issuer: app.proxy_issuer ?? '',
              api_key_enabled: app.api_key_enabled ?? false,
              oauth_enabled: app.oauth_enabled ?? true,
              quota_per_credit: app.quota_per_credit ?? null,
              marketplace_visible: app.marketplace_visible ?? false,
              marketplace_description: app.marketplace_description ?? '',
              marketplace_url: app.marketplace_url ?? '',
              marketplace_sort: app.marketplace_sort ?? 0,
              name: app.name,
              logo_url: app.logo_url,
              redirect_uris: app.redirect_uris,
              post_logout_redirect_uris: app.post_logout_redirect_uris,
              scopes: app.scopes,
              public_client: app.public_client,
              waffo_pancake_return_url: app.waffo_pancake_return_url,
            }}
            onSubmit={onSubmit}
            submitLabel={t('Save')}
            loading={loading}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}
