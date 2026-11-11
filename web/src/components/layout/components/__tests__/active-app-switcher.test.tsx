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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'
import { TooltipProvider } from '@/components/ui/tooltip'
import { SearchProvider } from '@/context/search-provider'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { PLATFORM_APP_ID, useActiveAppStore } from '@/stores/active-app-store'
import { useAuthStore } from '@/stores/auth-store'

import { ActiveAppSwitcher } from '../active-app-switcher'
import { AppHeader } from '../app-header'

afterEach(() => {
  useActiveAppStore.setState(useActiveAppStore.getInitialState(), true)
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  window.localStorage.clear()
})

test('starts on the platform app and persists only the selected app id', () => {
  expect(useActiveAppStore.getState().appId).toBe(PLATFORM_APP_ID)

  useActiveAppStore.getState().setAppId(7)

  expect(useActiveAppStore.getState().appId).toBe(7)
  expect(
    JSON.parse(window.localStorage.getItem('new-api-active-app') ?? '{}')
  ).toEqual({ state: { appId: 7 }, version: 0 })
})

test.each(['loading', 'failed'] as const)(
  'keeps app 7 visible and selectable on mobile when the list is %s',
  async (status) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
    useActiveAppStore.getState().setAppId(7)
    vi.spyOn(api, 'get').mockImplementation(() => {
      if (status === 'failed') return Promise.reject(new Error('Unavailable'))
      return new Promise(() => {})
    })
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches: query.includes('max-width'),
    }))
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <ActiveAppSwitcher />
      </QueryClientProvider>
    )
    if (status === 'failed') {
      await waitFor(() =>
        expect(client.getQueryState(['oauth-apps'])?.status).toBe('error')
      )
    }
    const trigger = screen.getByRole('combobox', { name: 'Active App' })
    expect(trigger).toHaveTextContent('App #7')
    expect(trigger.parentElement).not.toHaveClass('hidden')
    expect(trigger).not.toHaveAttribute('aria-disabled', 'true')
    trigger.focus()
    await userEvent.keyboard('{ArrowDown}')
    const option = await screen.findByRole('option', { name: 'App #7' })
    expect(option).toHaveAttribute('aria-selected', 'true')
    await userEvent.click(screen.getByRole('option', { name: 'Platform' }))
    await waitFor(() => expect(trigger).toHaveTextContent('Platform'))
    expect(useActiveAppStore.getState().appId).toBe(PLATFORM_APP_ID)
    client.clear()
  }
)

test('the mobile app header keeps scope selection accessible and allows controls to wrap', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  useActiveAppStore.getState().setAppId(7)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/oauth-apps/') return { data: { success: true, data: [] } }
    if (url === '/api/notice') return { data: { success: true, data: '' } }
    return { data: { success: true, data: {} } }
  })
  const root = createRootRoute({
    component: () => (
      <TooltipProvider>
        <SearchProvider>
          <SidebarProvider>
            <AppHeader showTopNav={false} showConfigDrawer={false} />
          </SidebarProvider>
        </SearchProvider>
      </TooltipProvider>
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  const trigger = await screen.findByRole('combobox', { name: 'Active App' })
  expect(trigger).toBeVisible()
  expect(trigger.closest('header')).toHaveClass('h-auto')
  expect(trigger.parentElement?.parentElement).toHaveClass(
    'min-w-0',
    'flex-wrap'
  )
  expect(trigger).toHaveClass('w-24')
  client.clear()
})

test('does not render the app switcher for regular users', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'bob', role: ROLE.USER })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  render(
    <QueryClientProvider client={client}>
      <ActiveAppSwitcher />
    </QueryClientProvider>
  )

  expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
})
