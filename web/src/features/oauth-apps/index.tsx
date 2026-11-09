import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { Switch } from '@/components/ui/switch'
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

  const [createOpen, setCreateOpen] = useState(false)
  const [editApp, setEditApp] = useState<OAuthApp | null>(null)
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
      setSecretApp({
        name: res.data.name,
        secret: res.client_secret,
        publicClient: res.data.public_client,
      })
      setCreateOpen(false)
      queryClient.invalidateQueries({ queryKey: ['oauth-apps'] })
    },
    onError: (error) => handleServerError(error, t('Failed to create app')),
  })

  const updateMutation = useMutation({
    mutationFn: ({
      id,
      data,
    }: {
      id: number
      data: Parameters<typeof updateOAuthApp>[1]
    }) => updateOAuthApp(id, data),
    onSuccess: () => {
      toast.success(t('App updated'))
      setEditApp(null)
      queryClient.invalidateQueries({ queryKey: ['oauth-apps'] })
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

  const rotateMutation = useMutation({
    mutationFn: rotateOAuthAppSecret,
    onSuccess: (res, id) => {
      const app = apps?.find((a) => a.id === id)
      setSecretApp({
        name: app?.name ?? '',
        secret: res.client_secret,
        publicClient: app?.public_client ?? false,
      })
    },
    onError: (error) => handleServerError(error, t('Failed to rotate secret')),
  })

  return (
    <div className='container mx-auto space-y-4 py-6'>
      <div className='flex items-center justify-between'>
        <h1 className='text-2xl font-bold'>{t('OAuth Applications')}</h1>
        <Button onClick={() => setCreateOpen(true)}>{t('Create App')}</Button>
      </div>

      <div className='rounded-md border'>
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
              {apps?.map((app) => (
                <tr key={app.id} className='border-b last:border-0'>
                  <td className='px-4 py-3 font-medium'>
                    <div className='flex items-center gap-2'>
                      {app.name}
                      {app.public_client && (
                        <Badge variant='secondary'>{t('Public Client')}</Badge>
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
                        disabled={updateMutation.isPending}
                        onClick={() => setEditApp(app)}
                      >
                        {t('Edit')}
                      </Button>
                      {!app.public_client && (
                        <Button
                          variant='outline'
                          size='sm'
                          disabled={rotateMutation.isPending}
                          onClick={() => rotateMutation.mutate(app.id)}
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
                              t('Delete this app? All tokens will be revoked.')
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
          onOpenChange={(open) => !open && setEditApp(null)}
          onSubmit={(data) => updateMutation.mutate({ id: editApp.id, data })}
          loading={updateMutation.isPending}
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
  name: string
  redirect_uris: string[]
  post_logout_redirect_uris: string[]
  scopes: string[]
  public_client: boolean
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
  const { t } = useTranslation()
  const [name, setName] = useState(defaultValues?.name ?? '')
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
  const publicClientId = useId()

  const handleSubmit = () => {
    if (loading) return
    onSubmit({
      name,
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
    })
  }

  return (
    <div className='space-y-4'>
      <div>
        <label className='mb-1 block text-sm font-medium'>
          {t('App Name')}
        </label>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder='My App'
          disabled={loading}
        />
      </div>
      <div>
        <label className='mb-1 block text-sm font-medium'>
          {t('Redirect URIs (one per line)')}
        </label>
        <textarea
          className='w-full rounded-md border p-2 text-sm'
          rows={3}
          value={redirectUris}
          onChange={(e) => setRedirectUris(e.target.value)}
          placeholder='https://api.example.com/oauth/callback'
          disabled={loading}
        />
      </div>
      <div>
        <label className='mb-1 block text-sm font-medium'>
          {t('Post-Logout Redirect URIs (one per line)')}
        </label>
        <textarea
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
              <input
                type='checkbox'
                checked={scopes.includes(scope.value)}
                disabled={loading}
                onChange={(e) => {
                  if (e.target.checked) {
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
      <DialogFooter>
        <Button
          onClick={handleSubmit}
          disabled={loading || !name || scopes.length === 0}
        >
          {loading ? t('Saving...') : submitLabel}
        </Button>
      </DialogFooter>
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
}: {
  app: OAuthApp
  onOpenChange: (open: boolean) => void
  onSubmit: (data: AppFormData) => void
  loading: boolean
}) {
  const { t } = useTranslation()
  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className='max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Edit OAuth Application')}</DialogTitle>
        </DialogHeader>
        <AppForm
          defaultValues={{
            name: app.name,
            redirect_uris: app.redirect_uris,
            post_logout_redirect_uris: app.post_logout_redirect_uris,
            scopes: app.scopes,
            public_client: app.public_client,
          }}
          onSubmit={onSubmit}
          submitLabel={t('Save')}
          loading={loading}
        />
      </DialogContent>
    </Dialog>
  )
}
