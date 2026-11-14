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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import type { ApiKey } from '../../types'

const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { ApiKeysProvider } = await import('../api-keys-provider')
const { ApiKeysMutateDrawer } = await import('../api-keys-mutate-drawer')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = {
  get: ApiMethod
  post: ApiMethod
  put: ApiMethod
}
type RenderedDrawer = {
  queryClient: InstanceType<typeof QueryClient>
}

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const originalPost = apiClient.post
const originalPut = apiClient.put
let renderedDrawer: RenderedDrawer | null = null

function installApiFixtures(
  createdPayloads: Array<Record<string, unknown>>,
  updatedPayloads: Array<Record<string, unknown>> = [],
  storedApiKey: Record<string, unknown> = {}
) {
  apiClient.get = async (url) => {
    switch (url) {
      case '/api/status':
        return { data: { data: { default_use_auto_group: true } } }
      case '/api/user/models':
        return { data: { success: true, data: [] } }
      case '/api/user/self/groups':
        return {
          data: {
            success: true,
            data: {
              auto: { desc: 'Automatic routing', ratio: 'auto' },
              default: { desc: 'Standard access', ratio: 1 },
              vip: { desc: 'Priority access', ratio: 2 },
            },
          },
        }
      case '/api/token/auto-groups':
        return {
          data: {
            success: true,
            data: { groups: ['vip', 'default'], max_count: 3 },
          },
        }
      case '/api/token/1':
        return { data: { success: true, data: storedApiKey } }
      default:
        throw new Error(`Unexpected GET ${url}`)
    }
  }
  apiClient.post = async (url, data) => {
    expect(url).toBe('/api/token/')
    expect(data && typeof data === 'object').toBeTruthy()
    createdPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: {} } }
  }
  apiClient.put = async (url, data) => {
    expect(url).toBe('/api/token/')
    expect(data && typeof data === 'object').toBeTruthy()
    updatedPayloads.push(data as Record<string, unknown>)
    return { data: { success: true, data: {} } }
  }
}

function createDrawerQueryClient(): InstanceType<typeof QueryClient> {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(
    ['status'],
    { default_use_auto_group: true },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-models'],
    { success: true, data: [] },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['user-groups'],
    {
      success: true,
      data: {
        auto: { desc: 'Automatic routing', ratio: 'auto' },
        default: { desc: 'Standard access', ratio: 1 },
        vip: { desc: 'Priority access', ratio: 2 },
      },
    },
    { updatedAt: freshAt }
  )
  queryClient.setQueryData(
    ['token-auto-groups'],
    {
      success: true,
      data: { groups: ['vip', 'default'], max_count: 3 },
    },
    { updatedAt: freshAt }
  )
  renderedDrawer = { queryClient }
  return queryClient
}

async function renderCreateDrawer(): Promise<void> {
  const queryClient = createDrawerQueryClient()

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          <ApiKeysMutateDrawer open onOpenChange={() => undefined} />
        </ApiKeysProvider>
      </I18nextProvider>
    </QueryClientProvider>
  )
  await waitFor(
    () => {
      const saveButton = findButton('Save changes', false)
      expect(saveButton).toBeEnabled()
    },
    { timeout: 1500 }
  )
}

async function renderUpdateDrawer(currentRow: {
  id: number
  name: string
}): Promise<void> {
  const queryClient = createDrawerQueryClient()

  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <ApiKeysProvider>
          <ApiKeysMutateDrawer
            open
            onOpenChange={() => undefined}
            currentRow={currentRow as never}
          />
        </ApiKeysProvider>
      </I18nextProvider>
    </QueryClientProvider>
  )
  await waitFor(
    () => {
      const saveButton = findButton('Save changes', false)
      expect(saveButton).toBeEnabled()
    },
    { timeout: 1500 }
  )
}

function findButton(text: string, required: true): HTMLButtonElement
function findButton(text: string, required: false): HTMLButtonElement | null
function findButton(text: string, required = true): HTMLButtonElement | null {
  const button = screen
    .queryAllByRole<HTMLButtonElement>('button')
    .find((candidate) => candidate.textContent?.includes(text))
  if (required && !button) {
    throw new Error(`Expected button containing "${text}"`)
  }
  return button ?? null
}

function getControlByLabel(labelText: 'Name' | 'Quantity'): HTMLInputElement
function getControlByLabel(labelText: 'Concurrency Limit'): HTMLInputElement
function getControlByLabel(
  labelText: 'Concurrency Limit By Model'
): HTMLTextAreaElement
function getControlByLabel(labelText: 'Group'): HTMLButtonElement
function getControlByLabel(labelText: 'Auto group order'): HTMLElement
function getControlByLabel(labelText: string): HTMLElement {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === labelText
  )
  if (!label) {
    throw new Error(`Expected label "${labelText}"`)
  }

  const control =
    label.control ??
    label
      .closest('[data-slot="form-item"]')
      ?.querySelector<HTMLElement>(
        '[data-slot="form-control"], input, textarea, button[role="combobox"], [role="group"]'
      )
  if (!control) {
    throw new Error(`Expected control for label "${labelText}"`)
  }
  return control
}

function changeInput(input: HTMLInputElement, value: string): void {
  fireEvent.input(input, { target: { value } })
}

function selectComboboxOption(
  trigger: HTMLButtonElement,
  optionDescription: string
): void {
  fireEvent.click(trigger)
  const option = [
    ...document.querySelectorAll<HTMLElement>('[data-slot="command-item"]'),
  ].find((candidate) => candidate.textContent?.includes(optionDescription))
  if (!option) {
    throw new Error(`Expected option containing "${optionDescription}"`)
  }
  fireEvent.click(option)
}

afterEach(() => {
  apiClient.get = originalGet
  apiClient.post = originalPost
  apiClient.put = originalPut
  useAuthStore.getState().auth.reset()
  localStorage.clear()
  if (renderedDrawer) {
    renderedDrawer.queryClient.clear()
    renderedDrawer = null
  }
})

describe('API keys mutate drawer Auto group integration', () => {
  test('inherits the root Auto order and sends an empty override for every batch-created key', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    const groupTrigger = getControlByLabel('Group')
    expect(groupTrigger.textContent?.includes('auto')).toBe(true)
    expect(
      document.body.textContent?.includes(
        'Using the complete global Auto order (2 groups)'
      )
    ).toBe(true)
    expect(
      [
        ...document.querySelectorAll('[data-slot="global-auto-order-name"]'),
      ].map((item) => item.textContent)
    ).toEqual(['vip', 'default'])
    expect(findButton('Restore global Auto', true).disabled).toBe(true)

    changeInput(getControlByLabel('Name'), 'batch')
    changeInput(getControlByLabel('Quantity'), '2')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(2))

    expect(createdPayloads.length).toBe(2)
    expect(createdPayloads[0]?.name).toBe('batch')
    for (const payload of createdPayloads) {
      expect(payload.group).toBe('auto')
      expect(payload.auto_groups).toEqual([])
      expect(payload.cross_group_retry).toBe(true)
    }
  })

  test('preserves an unsaved custom order and mode after Auto to ordinary to Auto changes', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    const autoOrderControl = getControlByLabel('Auto group order')
    const addGroupTrigger = autoOrderControl.querySelector<HTMLButtonElement>(
      'button[role="combobox"]'
    )
    if (!addGroupTrigger) {
      throw new Error('Expected Auto group order combobox')
    }
    selectComboboxOption(addGroupTrigger, 'Priority access')

    expect(
      document.querySelector('button[aria-label="Remove vip"]')
    ).toBeTruthy()
    expect(document.body.textContent?.includes('1 / 3 groups selected')).toBe(
      true
    )
    expect(findButton('Restore global Auto', true).disabled).toBe(false)

    const groupTrigger = getControlByLabel('Group')
    selectComboboxOption(groupTrigger, 'Standard access')
    expect(document.querySelector('button[aria-label="Remove vip"]')).toBe(null)
    selectComboboxOption(groupTrigger, 'Automatic routing')

    expect(
      document.querySelector('button[aria-label="Remove vip"]')
    ).toBeTruthy()
    expect(document.body.textContent?.includes('1 / 3 groups selected')).toBe(
      true
    )
    expect(findButton('Restore global Auto', true).disabled).toBe(false)

    changeInput(getControlByLabel('Name'), 'custom')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(createdPayloads[0]?.auto_groups).toEqual(['vip'])
  })
})

describe('API key expiry across editing sessions', () => {
  test.each(['close', 'save'] as const)(
    'keeps an uncached key non-expiring after %s of a key with an expiry',
    async (finish) => {
      installApiFixtures([])
      const first: ApiKey = {
        id: 1,
        name: 'expiring',
        key: '',
        status: 1,
        remain_quota: 0,
        used_quota: 0,
        unlimited_quota: true,
        expired_time: 2000000000,
        created_time: 1,
        accessed_time: 0,
        group: 'default',
        auto_groups: [],
        cross_group_retry: false,
        model_limits_enabled: false,
        model_limits: '',
        allow_ips: '',
        allow_user_quota_query: true,
      }
      const second = { ...first, id: 2, name: 'non-expiring', expired_time: -1 }
      let resolveDetail!: (value: { data: unknown }) => void
      const detail = new Promise<{ data: unknown }>((resolve) => {
        resolveDetail = resolve
      })
      const getFixture = apiClient.get
      const get = vi.spyOn(apiClient, 'get').mockImplementation((url) => {
        if (url === '/api/token/1') {
          return Promise.resolve({ data: { success: true, data: first } })
        }
        if (url === '/api/token/2') return detail
        return getFixture(url)
      })
      const put = vi
        .spyOn(api, 'put')
        .mockResolvedValue({ data: { success: true } })
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      renderedDrawer = { queryClient }
      const close = vi.fn()
      const tree = (currentRow: ApiKey, open: boolean) => (
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <ApiKeysProvider>
              <ApiKeysMutateDrawer
                open={open}
                currentRow={currentRow}
                onOpenChange={close}
              />
            </ApiKeysProvider>
          </I18nextProvider>
        </QueryClientProvider>
      )
      const view = render(tree(first, true))
      await waitFor(() =>
        expect(findButton('Save changes', true)).toBeEnabled()
      )
      await waitFor(() => expect(screen.getByLabelText('Time')).toBeEnabled())

      if (finish === 'save') {
        fireEvent.click(findButton('Save changes', true))
        await waitFor(() => expect(put).toHaveBeenCalled())
        expect(put.mock.calls[0][1]).toMatchObject({
          id: 1,
          expired_time: 2000000000,
        })
      } else {
        fireEvent.click(screen.getAllByRole('button', { name: 'Close' })[0])
      }
      await waitFor(() => expect(close.mock.calls[0]?.[0]).toBe(false))
      view.rerender(tree(first, false))
      await waitFor(() =>
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      )
      view.rerender(tree(second, true))
      await waitFor(() => expect(get).toHaveBeenCalledWith('/api/token/2'))
      expect(findButton('Save changes', true)).toBeDisabled()
      await act(async () =>
        resolveDetail({ data: { success: true, data: second } })
      )
      await waitFor(() =>
        expect(findButton('Save changes', true)).toBeEnabled()
      )
      expect(
        screen.getByRole('button', { name: 'Never expires' })
      ).toBeVisible()
      expect(screen.getByLabelText('Time')).toBeDisabled()
      fireEvent.click(findButton('Save changes', true))
      await waitFor(() =>
        expect(put).toHaveBeenLastCalledWith(
          '/api/token/',
          expect.objectContaining({ id: 2, expired_time: -1 })
        )
      )
    }
  )

  test.each(['Never', 'Clear'])(
    'submits a non-expiring key when its date is cleared with %s',
    async (action) => {
      const payloads: Array<Record<string, unknown>> = []
      installApiFixtures(payloads)
      await renderCreateDrawer()
      changeInput(getControlByLabel('Name'), 'no-expiry')
      fireEvent.click(screen.getByRole('button', { name: '1 Month' }))
      expect(screen.getByLabelText('Time')).toBeEnabled()
      fireEvent.click(screen.getByRole('button', { name: action }))
      expect(
        screen.getByRole('button', { name: 'Never expires' })
      ).toBeVisible()
      expect(screen.getByLabelText('Time')).toBeDisabled()
      fireEvent.click(findButton('Save changes', true))
      await waitFor(() => expect(payloads).toHaveLength(1))
      expect(payloads[0]?.expired_time).toBe(-1)
    }
  )
})

describe('API keys mutate drawer user quota query switch', () => {
  test('sends false for a new key after the switch, which starts on, is turned off', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    fireEvent.click(findButton('Advanced Settings', true))
    const quotaQuerySwitch = await screen.findByRole('switch', {
      name: 'Allow querying user quota',
    })
    expect(quotaQuerySwitch).toBeChecked()
    fireEvent.click(quotaQuerySwitch)
    expect(quotaQuerySwitch).not.toBeChecked()

    changeInput(getControlByLabel('Name'), 'no-user-quota')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))
    expect(createdPayloads[0]?.allow_user_quota_query).toBe(false)
  })
})

describe('API keys mutate drawer concurrency limit', () => {
  const storedRow = {
    id: 1,
    name: 'existing-key',
    key: 'sk-abcd***wxyz',
    status: 1,
    remain_quota: 0,
    used_quota: 0,
    unlimited_quota: true,
    expired_time: -1,
    created_time: 1700000000,
    accessed_time: 1700000000,
    group: 'default',
    auto_groups: null,
    cross_group_retry: false,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: '',
    concurrency_limit: 5,
    concurrency_limit_by_model: 'gpt-4o=3',
  }

  beforeEach(() => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'operator', role: 10 })
  })

  test('create drawer renders the Concurrency Limit input with the unlimited hint', async () => {
    installApiFixtures([])
    await renderCreateDrawer()

    const input = getControlByLabel('Concurrency Limit')
    expect(input.type).toBe('number')
    expect(input.min).toBe('0')
    expect(input.placeholder).toBe('0 means unlimited')
    expect(document.body.textContent).toContain('0 means unlimited')
  })

  test('create payload carries the typed concurrency limit as an explicit value', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    changeInput(getControlByLabel('Name'), 'batch')
    changeInput(getControlByLabel('Concurrency Limit'), '3')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))

    const payload = createdPayloads[0] as Record<string, unknown>
    expect(Object.hasOwn(payload, 'concurrency_limit')).toBe(true)
    expect(payload.concurrency_limit).toBe(3)
  })

  test('create payload keeps an explicit zero instead of dropping the unlimited value', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    changeInput(getControlByLabel('Name'), 'zero')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))

    const payload = createdPayloads[0] as Record<string, unknown>
    expect(Object.hasOwn(payload, 'concurrency_limit')).toBe(true)
    expect(payload.concurrency_limit).toBe(0)
  })

  test('update drawer backfills the stored concurrency limit', async () => {
    installApiFixtures([], [], storedRow)
    await renderUpdateDrawer(storedRow)

    const input = getControlByLabel('Concurrency Limit')
    await waitFor(() => expect(input.value).toBe('5'))
  })

  test('update payload carries the stored concurrency limit unchanged', async () => {
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures([], updatedPayloads, storedRow)
    await renderUpdateDrawer(storedRow)

    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(updatedPayloads).toHaveLength(1))

    const payload = updatedPayloads[0] as Record<string, unknown>
    expect(Object.hasOwn(payload, 'concurrency_limit')).toBe(true)
    expect(payload.concurrency_limit).toBe(5)
  })

  test('update payload keeps an explicit zero after clearing the field', async () => {
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures([], updatedPayloads, storedRow)
    await renderUpdateDrawer(storedRow)

    const input = getControlByLabel('Concurrency Limit')
    await waitFor(() => expect(input.value).toBe('5'))
    changeInput(input, '0')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(updatedPayloads).toHaveLength(1))

    const payload = updatedPayloads[0] as Record<string, unknown>
    expect(Object.hasOwn(payload, 'concurrency_limit')).toBe(true)
    expect(payload.concurrency_limit).toBe(0)
  })

  test('create drawer renders the Concurrency Limit By Model textarea with the format hint', async () => {
    installApiFixtures([])
    await renderCreateDrawer()

    const textarea = getControlByLabel('Concurrency Limit By Model')
    expect(textarea.tagName).toBe('TEXTAREA')
    expect(textarea.placeholder).toBe('gpt-4o=3,claude-sonnet=2')
    expect(document.body.textContent).toContain(
      'model=limit pairs separated by commas. Leave empty for no per-model limits.'
    )
  })

  test('preview lists each accepted model=limit pair for a valid CSV', async () => {
    installApiFixtures([])
    await renderCreateDrawer()

    fireEvent.input(getControlByLabel('Concurrency Limit By Model'), {
      target: { value: 'gpt-4o=3,claude=2' },
    })

    expect(screen.getByText('Parsed entries')).toBeInTheDocument()
    expect(document.body.textContent).toContain('gpt-4o=3')
    expect(document.body.textContent).toContain('claude=2')
    expect(screen.queryByText('Invalid entries')).not.toBeInTheDocument()
  })

  test('defect entry shows its reason and blocks the save before any write request', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads, updatedPayloads)
    await renderCreateDrawer()

    fireEvent.input(getControlByLabel('Concurrency Limit By Model'), {
      target: { value: 'gpt-4o=x' },
    })
    expect(screen.getByText('Invalid entries')).toBeInTheDocument()
    expect(document.body.textContent).toContain('gpt-4o=x')
    expect(document.body.textContent).toContain('Limit must be an integer')

    fireEvent.click(findButton('Save changes', true))
    await waitFor(() =>
      expect(
        screen.getByText('Fix invalid entries before saving')
      ).toBeInTheDocument()
    )
    expect(createdPayloads).toHaveLength(0)
    expect(updatedPayloads).toHaveLength(0)
  })

  test('create payload always carries the CSV key explicitly (empty means no per-model limits)', async () => {
    const createdPayloads: Array<Record<string, unknown>> = []
    installApiFixtures(createdPayloads)
    await renderCreateDrawer()

    changeInput(getControlByLabel('Name'), 'fresh-csv')
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(createdPayloads).toHaveLength(1))

    const payload = createdPayloads[0] as Record<string, unknown>
    expect(Object.hasOwn(payload, 'concurrency_limit_by_model')).toBe(true)
    expect(payload.concurrency_limit_by_model).toBe('')
  })

  test('update drawer backfills the stored CSV so reopening never silently clears it', async () => {
    installApiFixtures([], [], storedRow)
    await renderUpdateDrawer(storedRow)

    const textarea = getControlByLabel('Concurrency Limit By Model')
    await waitFor(() => expect(textarea.value).toBe('gpt-4o=3'))
  })

  test('update payload keeps the stored CSV, then sends an explicit empty string after clearing', async () => {
    const updatedPayloads: Array<Record<string, unknown>> = []
    installApiFixtures([], updatedPayloads, storedRow)
    await renderUpdateDrawer(storedRow)

    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(updatedPayloads).toHaveLength(1))
    const kept = updatedPayloads[0] as Record<string, unknown>
    expect(Object.hasOwn(kept, 'concurrency_limit_by_model')).toBe(true)
    expect(kept.concurrency_limit_by_model).toBe('gpt-4o=3')

    const textarea = getControlByLabel('Concurrency Limit By Model')
    await waitFor(() => expect(textarea.value).toBe('gpt-4o=3'))
    fireEvent.input(textarea, { target: { value: '' } })
    fireEvent.click(findButton('Save changes', true))
    await waitFor(() => expect(updatedPayloads).toHaveLength(2))
    const cleared = updatedPayloads[1] as Record<string, unknown>
    expect(Object.hasOwn(cleared, 'concurrency_limit_by_model')).toBe(true)
    expect(cleared.concurrency_limit_by_model).toBe('')
  })

  test('non-admin editor does not render Concurrency Limit', async () => {
    installApiFixtures([])
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'operator', role: 1 })
    await renderCreateDrawer()

    expect(screen.queryByLabelText('Concurrency Limit')).not.toBeInTheDocument()
    expect(
      screen.queryByLabelText('Concurrency Limit By Model')
    ).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('0 means unlimited')
    expect(document.body.textContent).not.toContain(
      'model=limit pairs separated by commas. Leave empty for no per-model limits.'
    )
  })
})
