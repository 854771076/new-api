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
import { render, screen } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'

import { ROLE } from '@/lib/roles'
import { PLATFORM_APP_ID, useActiveAppStore } from '@/stores/active-app-store'
import { useAuthStore } from '@/stores/auth-store'

import { ActiveAppSwitcher } from '../active-app-switcher'

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
