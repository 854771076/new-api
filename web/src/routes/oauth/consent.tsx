import { useQuery } from '@tanstack/react-query'
import { createFileRoute, useLocation, useRouter } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { getOAuthConsentContext } from '@/features/oauth-apps/api'
import { OAUTH_SCOPES } from '@/features/oauth-apps/types'

const consentSearchParams = z.object({
  client_id: z.string().min(1).max(2048),
  redirect_uri: z.string().min(1).max(2048),
  scope: z.string().min(1).max(2048),
  // Opaque protocol values must not be truncated or silently dropped.
  state: z.string().default(''),
  nonce: z.string().default(''),
  code_challenge: z.string().min(1).max(2048),
  code_challenge_method: z.literal('S256'),
  response_type: z.literal('code').optional(),
})

export const Route = createFileRoute('/oauth/consent')({
  // Router-parsed search values are not OAuth protocol values: its default
  // JSON parser changes numbers, booleans and quoted strings. Validate the
  // raw URL below, and keep invalid requests on the safe error page.
  validateSearch: z.record(z.string(), z.unknown()),
  component: OAuthConsentPage,
})

function OAuthConsentPage() {
  const { t } = useTranslation()
  const router = useRouter()
  // ParsedLocation.searchStr is also re-stringified by TanStack. Subscribe
  // through useLocation, but read the original search from router history.
  const rawSearch = useLocation({
    select: () => router.history.location.search,
  })
  const params = new URLSearchParams(rawSearch)
  const parsed = consentSearchParams.safeParse(Object.fromEntries(params))
  const duplicateParam = Object.keys(consentSearchParams.shape).some(
    (key) => params.getAll(key).length > 1
  )
  const search = parsed.success && !duplicateParam ? parsed.data : null
  const clientId = search?.client_id ?? ''

  // The consent URL is a redirect target, so its CSRF token may have expired by
  // the time the page is submitted. Fetching the context issues a fresh token
  // bound to the current session and returns the app name and logo.
  const contextQuery = useQuery({
    queryKey: ['oauth-consent-context', clientId],
    queryFn: () => getOAuthConsentContext(clientId),
    enabled: search !== null,
    retry: false,
    meta: { errorToast: false },
  })

  if (!search) {
    return (
      <div className='bg-muted/30 flex min-h-screen items-center justify-center p-4'>
        <ErrorState
          title={t('Invalid request')}
          description={t('Invalid parameters')}
        />
      </div>
    )
  }

  if (contextQuery.isPending) {
    return (
      <div className='bg-muted/30 flex min-h-screen items-center justify-center p-4'>
        <LoadingState />
      </div>
    )
  }

  if (contextQuery.isError || !contextQuery.data) {
    return (
      <div className='bg-muted/30 flex min-h-screen items-center justify-center p-4'>
        <ErrorState
          title={t('Failed to load application')}
          description={
            contextQuery.error instanceof Error
              ? contextQuery.error.message
              : undefined
          }
          onRetry={() => void contextQuery.refetch()}
        />
      </div>
    )
  }

  const context = contextQuery.data
  const appName = context.name || search.client_id
  const initial = appName.slice(0, 1).toUpperCase()
  const scopes = search.scope.split(' ').filter(Boolean)
  const scopeDescriptions = scopes.map(
    (s) => OAUTH_SCOPES.find((o) => o.value === s)?.label ?? s
  )

  return (
    <div className='bg-muted/30 flex min-h-screen items-center justify-center p-4'>
      <div className='bg-background w-full max-w-md rounded-lg border p-6 shadow-lg'>
        <h1 className='mb-2 text-xl font-bold'>{t('Authorize Application')}</h1>
        <p className='text-muted-foreground mb-4 text-sm'>
          {t('An application is requesting access to your new-api account.')}
        </p>

        <div className='bg-muted mb-4 flex items-center gap-3 rounded-md p-3'>
          {context.logo_url ? (
            <img
              src={context.logo_url}
              alt=''
              className='bg-background size-12 shrink-0 rounded-md border object-contain'
            />
          ) : (
            <div
              aria-hidden='true'
              className='bg-background flex size-12 shrink-0 items-center justify-center rounded-md border text-lg font-semibold'
            >
              {initial}
            </div>
          )}
          <p className='text-sm font-medium break-all'>{appName}</p>
        </div>

        <h2 className='mb-2 text-sm font-semibold'>
          {t('Requested Permissions')}
        </h2>
        <ul className='mb-6 space-y-1'>
          {scopeDescriptions.map((desc) => (
            <li key={desc} className='flex items-start gap-2 text-sm'>
              <span className='text-green-600'>✓</span>
              <span>{t(desc)}</span>
            </li>
          ))}
        </ul>

        <form method='POST' action='/oauth/authorize'>
          <input type='hidden' name='response_type' value='code' />
          <input type='hidden' name='client_id' value={search.client_id} />
          <input
            type='hidden'
            name='redirect_uri'
            value={search.redirect_uri}
          />
          <input type='hidden' name='scope' value={search.scope} />
          <input type='hidden' name='state' value={search.state} />
          {search.nonce ? (
            <input type='hidden' name='nonce' value={search.nonce} />
          ) : null}
          <input
            type='hidden'
            name='code_challenge'
            value={search.code_challenge}
          />
          <input
            type='hidden'
            name='code_challenge_method'
            value={search.code_challenge_method}
          />
          <input type='hidden' name='csrf_token' value={context.csrf_token} />
          <div className='flex gap-3'>
            <Button
              type='submit'
              name='consent'
              value='allow'
              className='flex-1'
            >
              {t('Allow')}
            </Button>
            <Button
              type='submit'
              name='consent'
              value='deny'
              variant='outline'
              className='flex-1'
            >
              {t('Deny')}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}
