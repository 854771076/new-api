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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test } from 'vitest'

import { ProfileDropdown } from '@/components/profile-dropdown'
import { ROLE } from '@/lib/roles'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

afterEach(() => {
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  window.localStorage.clear()
})

test.each([ROLE.USER, ROLE.ADMIN, ROLE.SUPER_ADMIN])(
  'CLI docs visibility for role %s',
  async (role) => {
    useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(STATUS_QUERY_KEY, {})
    const router = createRouter({
      routeTree: createRootRoute({ component: ProfileDropdown }),
      history: createMemoryHistory({ initialEntries: ['/'] }),
    })
    await router.load()
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    )
    await userEvent.click(
      await screen.findByRole('button', { name: 'Account menu' })
    )
    const entry = screen.queryByRole('menuitem', {
      name: 'CLI installation & usage',
    })
    if (role === ROLE.USER) {
      expect(entry).not.toBeInTheDocument()
      return
    }
    expect(entry).toBeInTheDocument()
    await userEvent.click(
      screen.getByRole('menuitem', { name: 'CLI installation & usage' })
    )
    const dialog = await screen.findByRole('dialog', {
      name: 'CLI installation & usage',
    })
    expect(dialog).toHaveTextContent('/api/cli/install')
    expect(dialog).toHaveTextContent(window.location.origin)
    expect(dialog).toHaveTextContent('self-update --run --server')
    expect(dialog).toHaveTextContent('~/.agents/skills/new-api-app-framework')
    expect(
      within(dialog).getAllByRole('button', { name: /^Copy .* commands$/ })
    ).toHaveLength(4)
  }
)
