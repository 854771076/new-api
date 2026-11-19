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
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { userSchema } from '../../../types'
import { AppProxyKeysDialog } from '../app-proxy-keys-dialog'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
}))
const clients: QueryClient[] = []
const user = userSchema.parse({
  id: 42,
  owner_app_id: 7,
  identity_source: 'proxy',
  username: 'Member',
  display_name: '',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
  status: 1,
  role: 1,
})
const key = {
  id: 8,
  user_id: 42,
  app_id: 7,
  name: 'Managed',
  key: 'masked',
  status: 1,
  remain_quota: 0,
  unlimited_quota: true,
  quota_rule_id: 1,
}
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>
      <AppProxyKeysDialog user={user} open onOpenChange={() => {}} />
    </QueryClientProvider>
  )
}
beforeEach(() => {
  vi.resetAllMocks()
  sessionStorage.clear()
})
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
})

test('rule-bound keys cannot stage rotation and cancelled replacements cannot be enabled', async () => {
  vi.mocked(api.get).mockImplementation(async (path) => ({
    data: {
      success: true,
      data: {
        items: String(path).endsWith('quota-rules')
          ? []
          : [
              key,
              {
                ...key,
                id: 9,
                status: 2,
                name: 'Cancelled',
                rotation_status: 'cancelled',
              },
            ],
        total: 2,
      },
    },
  }))
  mount()
  await screen.findByText(/Managed/)
  expect(
    screen
      .getAllByRole('button', { name: 'Stage replacement key' })
      .every((button) => (button as HTMLButtonElement).disabled)
  ).toBe(true)
  expect(screen.getByRole('button', { name: 'Enable' })).toBeDisabled()
  expect(
    screen.getAllByRole('button', { name: 'Bind quota rule' })
  ).toHaveLength(2)
})

test('cancel confirms revocation, refreshes state and permits a new rotation operation', async () => {
  let cancelled = false
  vi.mocked(api.get).mockImplementation(async (path) => ({
    data: {
      success: true,
      data: {
        items: String(path).endsWith('quota-rules')
          ? []
          : [
              {
                ...key,
                quota_rule_id: 0,
                rotation_status: cancelled ? 'cancelled' : 'pending',
                status: cancelled ? 2 : 1,
              },
            ],
        total: 1,
      },
    },
  }))
  vi.mocked(api.post).mockImplementation(async () => {
    cancelled = true
    return { data: { success: true } }
  })
  sessionStorage.setItem('app-key-rotation:42:7:8', '{"old_token_id":1}')
  sessionStorage.setItem(
    'app-key-operation:42:7:{"old_token_id":1}',
    'old-operation'
  )
  sessionStorage.setItem(
    'app-key-operation:99:7:{"old_token_id":1}',
    'other-user'
  )
  mount()
  fireEvent.click(
    await screen.findByRole('button', { name: 'Cancel rotation' })
  )
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/app-proxy/users/42/keys/8/cancel-rotation',
      {},
      { params: { app_id: 7 } }
    )
  )
  await screen.findByText('Rotation cancelled')
  expect(
    screen.queryByRole('button', { name: 'Cancel rotation' })
  ).not.toBeInTheDocument()
  expect(
    sessionStorage.getItem('app-key-operation:42:7:{"old_token_id":1}')
  ).toBeNull()
  expect(
    sessionStorage.getItem('app-key-operation:99:7:{"old_token_id":1}')
  ).toBe('other-user')
})

test('creation can load rules after page one and atomically supplies the selected rule', async () => {
  vi.mocked(api.get).mockImplementation(async (path, config) => ({
    data: {
      success: true,
      data: String(path).endsWith('quota-rules')
        ? {
            items: [
              {
                id:
                  (config?.params as Record<string, unknown> | undefined)?.p ===
                  2
                    ? 101
                    : 1,
                name:
                  (config?.params as Record<string, unknown> | undefined)?.p ===
                  2
                    ? 'Later member'
                    : 'First member',
              },
            ],
            total: 101,
          }
        : { items: [], total: 0 },
    },
  }))
  vi.mocked(api.post).mockResolvedValue({
    data: { success: true, data: { ...key, key: 'once-secret' } },
  })
  mount()
  fireEvent.click(await screen.findByRole('button', { name: 'Load more' }))
  await screen.findByRole('option', { name: 'Later member' })
  fireEvent.change(screen.getByRole('combobox', { name: 'Quota rule' }), {
    target: { value: '101' },
  })
  fireEvent.change(screen.getByRole('textbox', { name: 'Name' }), {
    target: { value: 'New member' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Create' }))
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/app-proxy/users/42/keys',
      expect.objectContaining({ quota_rule_id: 101 }),
      expect.objectContaining({ params: { app_id: 7 } })
    )
  )
})
