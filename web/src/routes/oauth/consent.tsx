import { createFileRoute, useSearch } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { OAUTH_SCOPES } from '@/features/oauth-apps/types'

interface ConsentSearch {
  client_id: string
  redirect_uri: string
  scope: string
  state: string
  nonce?: string
  code_challenge: string
  code_challenge_method: string
}

export const Route = createFileRoute('/oauth/consent')({
  component: OAuthConsentPage,
})

function OAuthConsentPage() {
  const { t } = useTranslation()
  const search = useSearch({ from: '/oauth/consent' }) as unknown as ConsentSearch

  const scopes = (search.scope ?? '').split(' ').filter(Boolean)
  const scopeDescriptions = scopes.map(
    (s) => OAUTH_SCOPES.find((o) => o.value === s)?.label ?? s,
  )

  const formAction = '/oauth/authorize'

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 p-4">
      <div className="w-full max-w-md rounded-lg border bg-background p-6 shadow-lg">
        <h1 className="mb-2 text-xl font-bold">{t('Authorize Application')}</h1>
        <p className="mb-4 text-sm text-muted-foreground">
          {t('An application is requesting access to your new-api account.')}
        </p>

        <div className="mb-4 rounded-md bg-muted p-3">
          <p className="text-sm">
            <span className="font-medium">{t('Client ID:')}</span>{' '}
            <code className="text-xs">{search.client_id}</code>
          </p>
        </div>

        <h2 className="mb-2 text-sm font-semibold">{t('Requested Permissions')}</h2>
        <ul className="mb-6 space-y-1">
          {scopeDescriptions.map((desc, i) => (
            <li key={i} className="flex items-start gap-2 text-sm">
              <span className="text-green-600">✓</span>
              <span>{t(desc)}</span>
            </li>
          ))}
        </ul>

        <form method="POST" action={formAction}>
          <input type="hidden" name="response_type" value="code" />
          <input type="hidden" name="client_id" value={search.client_id} />
          <input type="hidden" name="redirect_uri" value={search.redirect_uri} />
          <input type="hidden" name="scope" value={search.scope} />
          <input type="hidden" name="state" value={search.state} />
          {search.nonce ? <input type="hidden" name="nonce" value={search.nonce} /> : null}
          <input type="hidden" name="code_challenge" value={search.code_challenge} />
          <input
            type="hidden"
            name="code_challenge_method"
            value={search.code_challenge_method}
          />
          <div className="flex gap-3">
            <Button type="submit" name="consent" value="allow" className="flex-1">
              {t('Allow')}
            </Button>
            <Button
              type="submit"
              name="consent"
              value="deny"
              variant="outline"
              className="flex-1"
            >
              {t('Deny')}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}
