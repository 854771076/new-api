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
import { QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { apiKeySchema } from '@/features/keys/types'
import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { QuotaRule } from '../api'
import { QuotaRuleBindingDialog } from '../binding-dialog'
import { QuotaRuleDialog } from '../rule-dialog'

vi.mock('@/lib/api', () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
}))
vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
const clients: ReturnType<typeof createAppQueryClient>[] = []
const rule: QuotaRule = {
  id: 7,
  name: 'Daily budget',
  description: '',
  enabled: true,
  quota_limit_enabled: true,
  quota_period: 'daily',
  quota_limit: 1,
  time_window_enabled: false,
  time_window_timezone: 'UTC',
  allowed_weekdays: '[1,2,3,4,5,6,7]',
  bound_token_count: 0,
}

beforeEach(() => {
  vi.resetAllMocks()
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
})

function mount(node: React.ReactNode) {
  const client = createAppQueryClient()
  client.setDefaultOptions({ queries: { retry: false } })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}

it('creates a rule in quota units with explicit booleans and weekday JSON', async () => {
  vi.mocked(api.post).mockResolvedValue({ data: { success: true } })
  const close = vi.fn()
  mount(<QuotaRuleDialog rule={null} onClose={close} />)
  const user = userEvent.setup()
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Daily budget')
  const amount = screen.getByRole('spinbutton')
  await user.clear(amount)
  await user.type(amount, '2')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(api.post).toHaveBeenCalledWith(
      '/api/token/quota_rules',
      expect.objectContaining({
        id: 0,
        name: 'Daily budget',
        quota_limit: 1_000_000,
        enabled: true,
        quota_limit_enabled: true,
        time_window_enabled: false,
        allowed_weekdays: '[1,2,3,4,5,6,7]',
      })
    )
  )
  await waitFor(() => expect(close).toHaveBeenCalledOnce())
})

it('rejects unsafe limits and keeps a failed edit open without rounding small quotas', async () => {
  vi.mocked(api.put).mockResolvedValue({
    data: { success: false, message: 'Save rejected' },
  })
  const close = vi.fn()
  mount(<QuotaRuleDialog rule={rule} onClose={close} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith(
      '/api/token/quota_rules/7',
      expect.objectContaining({ quota_limit: 1 })
    )
  )
  expect(close).not.toHaveBeenCalled()
  expect(screen.getByRole('dialog')).toBeVisible()
  vi.mocked(api.put).mockClear()
  const amount = screen.getByRole('spinbutton')
  fireEvent.change(amount, { target: { value: '9007199254740992' } })
  const form = amount.closest('form')
  if (!form) throw new Error('Quota form is missing')
  fireEvent.submit(form)
  expect(
    await screen.findByText('Quota rule limit must be a positive safe integer')
  ).toBeVisible()
  expect(api.put).not.toHaveBeenCalled()
})

it('loads more than one page of rules and binds a rule from the later page', async () => {
  vi.mocked(api.get).mockImplementation(async (_url, config) => {
    const page = (config?.params as { p?: number } | undefined)?.p ?? 1
    const items =
      page === 1
        ? Array.from({ length: 50 }, (_, index) => ({
            ...rule,
            id: index + 1,
            name: `Rule ${index + 1}`,
          }))
        : [{ ...rule, id: 51, name: 'Last rule' }]
    return { data: { success: true, data: { items, total: 51 } } }
  })
  vi.mocked(api.put).mockResolvedValue({ data: { success: true } })
  const token = apiKeySchema.parse({
    id: 1,
    name: 'Key',
    key: '',
    status: 1,
    remain_quota: 0,
    used_quota: 0,
    unlimited_quota: true,
    expired_time: -1,
    created_time: 0,
    accessed_time: 0,
    model_limits_enabled: false,
  })
  const close = vi.fn(),
    refresh = vi.fn()
  mount(
    <QuotaRuleBindingDialog
      apiKey={token}
      onClose={close}
      onSuccess={refresh}
    />
  )
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Load more' }))
  await screen.findByRole('option', { name: 'Last rule' })
  await user.selectOptions(screen.getByLabelText('Quota rule'), '51')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/token/1/quota_rule', {
      rule_id: 51,
    })
  )
  await waitFor(() => expect(refresh).toHaveBeenCalledOnce())
  expect(close).toHaveBeenCalledOnce()
})
