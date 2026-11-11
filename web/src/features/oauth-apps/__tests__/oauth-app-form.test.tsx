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
