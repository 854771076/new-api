import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { Route } from '../consent'

const clients: QueryClient[] = []
afterEach(() => clients.splice(0).forEach((client) => client.clear()))

function consentUrl(state: string, nonce: string): string {
  const search = new URLSearchParams({
    client_id: 'app-client',
    redirect_uri: 'https://app.example/callback',
    scope: 'openid',
    code_challenge: 'challenge',
    code_challenge_method: 'S256',
    state,
    nonce,
  })
  return `/oauth/consent?${search}`
}

async function renderConsent(url: string) {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { name: 'Trusted App', logo_url: '', csrf_token: 'session-csrf' },
    },
  })
  const root = createRootRoute()
  const route = createRoute({
    getParentRoute: () => root,
    path: '/oauth/consent',
    component: Route.options.component,
    validateSearch: Route.options.validateSearch,
  })
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [url] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { router, get }
}

test.each([
  ['123', '456'],
  ['900719925474099312345', '1e3'],
  ['true', 'null'],
  ['"opaque"', '[1,2]'],
  ['s'.repeat(2049), 'n'.repeat(2049)],
  [' + / = & % ', '😀 nonce'],
])(
  'preserves opaque state and nonce exactly in the consent POST (%s)',
  async (state, nonce) => {
    await renderConsent(consentUrl(state, nonce))
    const allow = await screen.findByRole('button', { name: 'Allow' })
    const form = (allow as HTMLButtonElement).form
    if (!form) throw new Error('Missing consent form')
    const submitted = new FormData(form)
    expect(submitted.get('state')).toBe(state)
    expect(submitted.get('nonce')).toBe(nonce)
    expect(submitted.get('csrf_token')).toBe('session-csrf')
    expect(form).toHaveAttribute('action', '/oauth/authorize')
  }
)

test('navigation between consent URLs updates protocol fields from the router history URL', async () => {
  const { router } = await renderConsent(consentUrl('123', '456'))
  await screen.findByRole('button', { name: 'Allow' })
  await act(async () => {
    // OAuth URLs arrive as protocol URLs, not JSON-typed router search objects.
    router.history.push(consentUrl('900719925474099312345', 'false'))
    await router.load()
  })
  await waitFor(() => {
    const form = (
      screen.getByRole('button', { name: 'Allow' }) as HTMLButtonElement
    ).form
    if (!form) throw new Error('Missing consent form')
    expect(new FormData(form).get('state')).toBe('900719925474099312345')
    expect(new FormData(form).get('nonce')).toBe('false')
  })
})

test.each([
  '/oauth/consent?client_id=app-client',
  `${consentUrl('state', 'nonce')}&state=duplicate`,
  consentUrl('state', 'nonce').replace('S256', 'plain'),
  consentUrl('state', 'nonce').replace(
    'code_challenge=challenge',
    `code_challenge=${'x'.repeat(2049)}`
  ),
])(
  'rejects invalid protocol fields without loading context or offering consent',
  async (url) => {
    const { get } = await renderConsent(url)
    await screen.findByText('Invalid request')
    expect(
      screen.queryByRole('button', { name: 'Allow' })
    ).not.toBeInTheDocument()
    expect(get).not.toHaveBeenCalled()
  }
)
