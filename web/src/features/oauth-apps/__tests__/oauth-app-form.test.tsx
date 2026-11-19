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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { OAuthAppsPage } from '../index'

const clients: QueryClient[] = []
afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
})

async function openCreateDialog() {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [] },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <OAuthAppsPage />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('button', { name: 'Create App' })
  )
  return await screen.findByRole('dialog', {
    name: 'Create OAuth Application',
  })
}

async function fillRequiredAndSubmit(dialog: HTMLElement, returnUrl?: string) {
  await userEvent.type(within(dialog).getByPlaceholderText('My App'), 'My App')
  await userEvent.click(
    within(dialog).getByRole('switch', { name: 'OAuth login' })
  )
  await userEvent.type(
    within(dialog).getByPlaceholderText(
      'https://api.example.com/oauth/callback'
    ),
    'https://example.com/callback'
  )
  await userEvent.click(
    within(dialog).getByRole('checkbox', { name: /openid/i })
  )
  if (returnUrl !== undefined) {
    await userEvent.type(
      within(dialog).getByPlaceholderText('https://example.com/payment/return'),
      returnUrl
    )
  }
  await userEvent.click(within(dialog).getByRole('button', { name: 'Create' }))
}

function mockCreate() {
  return vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      client_secret: 'secret',
      data: { id: 1, name: 'My App', public_client: false },
    },
  })
}

test('keeps the app editor wide on desktop with labelled configuration groups', async () => {
  const dialog = await openCreateDialog()
  expect(dialog).toHaveClass('sm:max-w-3xl')
  expect(dialog).toHaveClass('w-[calc(100%-2rem)]')
  expect(
    within(dialog).getByRole('group', { name: 'Integration modes' })
  ).toBeInTheDocument()
  expect(
    within(dialog).getByRole('group', { name: 'Credits and payments' })
  ).toBeInTheDocument()
  expect(
    within(dialog).getByRole('group', { name: 'App marketplace' })
  ).toBeInTheDocument()
})

test('submits the per-app payment return URL trimmed', async () => {
  const post = mockCreate()
  const dialog = await openCreateDialog()
  await fillRequiredAndSubmit(dialog, '  https://app.example/success  ')
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/oauth-apps/',
      expect.objectContaining({
        waffo_pancake_return_url: 'https://app.example/success',
      })
    )
  )
})

test('submits an empty return URL when left blank', async () => {
  const post = mockCreate()
  const dialog = await openCreateDialog()
  await fillRequiredAndSubmit(dialog)
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/oauth-apps/',
      expect.objectContaining({ waffo_pancake_return_url: '' })
    )
  )
})

test('defaults to proxy mode and creates without OAuth configuration or a secret dialog', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        id: 2,
        name: 'Proxy App',
        oauth_enabled: false,
        api_key_enabled: true,
      },
    },
  })
  const dialog = await openCreateDialog()
  expect(
    within(dialog).getByRole('switch', { name: 'API key proxy' })
  ).toBeChecked()
  expect(
    within(dialog).getByRole('switch', { name: 'OAuth login' })
  ).not.toBeChecked()
  expect(within(dialog).queryByRole('checkbox')).not.toBeInTheDocument()
  await userEvent.type(
    within(dialog).getByPlaceholderText('My App'),
    'Proxy App'
  )
  await userEvent.click(within(dialog).getByRole('button', { name: 'Create' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/oauth-apps/',
      expect.objectContaining({
        api_key_enabled: true,
        oauth_enabled: false,
        quota_per_credit: null,
      })
    )
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
})

test('retains OAuth fields across toggles and rejects invalid credit ratios', async () => {
  const dialog = await openCreateDialog()
  const oauth = within(dialog).getByRole('switch', { name: 'OAuth login' })
  await userEvent.click(oauth)
  await userEvent.type(
    within(dialog).getByPlaceholderText(
      'https://api.example.com/oauth/callback'
    ),
    'https://example.com/callback'
  )
  await userEvent.click(oauth)
  expect(
    within(dialog).queryByPlaceholderText(
      'https://api.example.com/oauth/callback'
    )
  ).not.toBeInTheDocument()
  await userEvent.click(oauth)
  expect(
    within(dialog).getByPlaceholderText(
      'https://api.example.com/oauth/callback'
    )
  ).toHaveValue('https://example.com/callback')
  await userEvent.type(
    within(dialog).getByRole('spinbutton', { name: 'Quota per credit' }),
    '0'
  )
  expect(within(dialog).getByRole('alert')).toHaveTextContent(
    'Invalid App credit ratio'
  )
  expect(within(dialog).getByRole('button', { name: 'Create' })).toBeDisabled()
})

test('changing an existing credit ratio requires confirmation and can clear it', async () => {
  const app = {
    id: 3,
    name: 'Credit App',
    logo_url: '',
    client_id: 'client-3',
    redirect_uris: [],
    post_logout_redirect_uris: [],
    scopes: [],
    status: 1,
    public_client: false,
    oauth_enabled: false,
    api_key_enabled: true,
    quota_per_credit: 100,
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [app] },
  })
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: true, data: { ...app, quota_per_credit: null } },
  })
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
  const dialog = await screen.findByRole('dialog', {
    name: 'Edit OAuth Application',
  })
  await userEvent.clear(
    within(dialog).getByRole('spinbutton', { name: 'Quota per credit' })
  )
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  const confirm = await screen.findByRole('alertdialog', {
    name: 'Change credit definition?',
  })
  expect(put).not.toHaveBeenCalled()
  await userEvent.click(within(confirm).getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/oauth-apps/3',
      expect.objectContaining({ quota_per_credit: null })
    )
  )
})
