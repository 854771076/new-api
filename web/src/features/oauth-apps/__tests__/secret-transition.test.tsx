import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { OAuthAppsPage } from '../index'
import type { OAuthApp } from '../types'

const clients: QueryClient[] = []
afterEach(() => clients.splice(0).forEach((client) => client.clear()))

async function editPublicApp(
  rotationResult: 'failure' | 'empty' | 'success' = 'success'
) {
  let app: OAuthApp = {
    id: 7,
    name: 'Browser App',
    logo_url: '',
    client_id: 'client-7',
    redirect_uris: ['https://app.example/callback'],
    post_logout_redirect_uris: [],
    scopes: ['openid'],
    status: 1,
    public_client: true,
    waffo_pancake_return_url: '',
    created_by: 1,
    created_at: 0,
    updated_at: 0,
  }
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: [app] },
  }))
  vi.spyOn(api, 'put').mockImplementation(async (_url, data) => {
    app = { ...app, ...(data as Partial<OAuthApp>) }
    return { data: { success: true, data: app } }
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, client_secret: 'one-time-secret' },
  })
  if (rotationResult === 'failure') {
    post.mockResolvedValueOnce({
      data: { success: false, message: 'Rotation unavailable' },
    })
  }
  if (rotationResult === 'empty') {
    post.mockResolvedValueOnce({ data: { success: true, client_secret: '' } })
  }
  const success = vi.spyOn(toast, 'success')
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <OAuthAppsPage />
    </QueryClientProvider>
  )
  await userEvent.click(await screen.findByRole('button', { name: 'Edit' }))
  const edit = await screen.findByRole('dialog', {
    name: 'Edit OAuth Application',
  })
  await userEvent.click(
    within(edit).getByRole('switch', {
      name: 'Public Client (PKCE, no client secret)',
    })
  )
  await userEvent.click(within(edit).getByRole('button', { name: 'Save' }))
  return { post, success }
}

test('public-to-confidential save rotates and displays a usable one-time secret', async () => {
  const { post } = await editPublicApp()
  const secret = await screen.findByRole('dialog', { name: 'Client Secret' })
  expect(within(secret).getByRole('textbox')).toHaveValue('one-time-secret')
  expect(
    within(secret).getByRole('button', { name: 'Copy to clipboard' })
  ).toBeEnabled()
  expect(post).toHaveBeenCalledWith('/api/oauth-apps/7/rotate-secret')
  await userEvent.click(within(secret).getByRole('button', { name: 'Done' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  const storedValues = Array.from(
    { length: window.localStorage.length },
    (_, index) => {
      const key = window.localStorage.key(index)
      return key === null ? '' : window.localStorage.getItem(key)
    }
  )
  expect(storedValues.join('')).not.toContain('one-time-secret')
})

test.each(['failure', 'empty'] as const)(
  'rotation %s after saving is not reported as success and offers a supported retry',
  async (result) => {
    const { post, success } = await editPublicApp(result)
    await screen.findByText(
      'App saved, but no client secret was issued. Rotate the secret before using this confidential client.'
    )
    expect(success).not.toHaveBeenCalledWith('App updated')
    expect(
      screen.queryByRole('dialog', { name: 'Client Secret' })
    ).not.toBeInTheDocument()
    const edit = screen.getByRole('dialog', { name: 'Edit OAuth Application' })
    await userEvent.click(within(edit).getByRole('button', { name: 'Retry' }))
    const secret = await screen.findByRole('dialog', { name: 'Client Secret' })
    expect(within(secret).getByRole('textbox')).toHaveValue('one-time-secret')
    expect(post).toHaveBeenCalledWith('/api/oauth-apps/7/rotate-secret')
  }
)
