import { useQuery } from '@tanstack/react-query'
import { createFileRoute, useSearch } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { getOAuthConsentContext } from '@/features/oauth-apps/api'
import { OAUTH_SCOPES } from '@/features/oauth-apps/types'

const CONSENT_PARAM_MAX_LENGTH = 2048

function normalizeConsentParam(value: unknown): string {
  if (typeof value !== 'string') {
    return ''
  }
  if (value.length > CONSENT_PARAM_MAX_LENGTH) {
    return ''
  }
  return value
}

const consentSearchParams = z.object({
  client_id: z.preprocess(normalizeConsentParam, z.string()),
  redirect_uri: z.preprocess(normalizeConsentParam, z.string()),
  scope: z.preprocess(normalizeConsentParam, z.string()),
  state: z.preprocess(normalizeConsentParam, z.string()),
  nonce: z.preprocess(normalizeConsentParam, z.string()),
  code_challenge: z.preprocess(normalizeConsentParam, z.string()),
  code_challenge_method: z.preprocess(normalizeConsentParam, z.string()),
})

export const Route = createFileRoute('/oauth/consent')({
  validateSearch: consentSearchParams,
  component: OAuthConsentPage,
})

function OAuthConsentPage() {
  const { t } = useTranslation()
  const search = useSearch({ from: '/oauth/consent' })

  const missingRequiredParam =
    search.client_id === '' ||
    search.redirect_uri === '' ||
    search.scope === '' ||
    search.code_challenge === '' ||
    search.code_challenge_method === ''

  // The consent URL is a redirect target, so its CSRF token may have expired by
  // the time the page is submitted. Fetching the context issues a fresh token
  // bound to the current session and returns the app name and logo.
  const contextQuery = useQuery({
    queryKey: ['oauth-consent-context', search.client_id],
    queryFn: () => getOAuthConsentContext(search.client_id),
    enabled: !missingRequiredParam,
    retry: false,
    meta: { errorToast: false },
  })

  if (missingRequiredParam) {
    return (
      <div className='flex min-h-screen items-center justify-center bg-muted/30 p-4'>
        <ErrorState
          title={t('Invalid request')}
          description={t('Invalid parameters')}
        />
      </div>
    )
  }

  if (contextQuery.isPending) {
    return (
      <div className='flex min-h-screen items-center justify-center bg-muted/30 p-4'>
        <LoadingState />
      </div>
    )
  }

  if (contextQuery.isError || !contextQuery.data) {
    return (
      <div className='flex min-h-screen items-center justify-center bg-muted/30 p-4'>
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
    <div className='flex min-h-screen items-center justify-center bg-muted/30 p-4'>
      <div className='w-full max-w-md rounded-lg border bg-background p-6 shadow-lg'>
        <h1 className='mb-2 text-xl font-bold'>{t('Authorize Application')}</h1>
        <p className='mb-4 text-sm text-muted-foreground'>
          {t('An application is requesting access to your new-api account.')}
        </p>

        <div className='mb-4 flex items-center gap-3 rounded-md bg-muted p-3'>
          {context.logo_url ? (
            <img
              src={context.logo_url}
              alt=''
              className='size-12 shrink-0 rounded-md border bg-background object-contain'
            />
          ) : (
            <div
              aria-hidden='true'
              className='flex size-12 shrink-0 items-center justify-center rounded-md border bg-background text-lg font-semibold'
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
          <input type='hidden' name='redirect_uri' value={search.redirect_uri} />
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
            <Button type='submit' name='consent' value='allow' className='flex-1'>
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
