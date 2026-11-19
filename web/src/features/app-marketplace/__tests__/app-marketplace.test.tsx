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
import type { ReactNode } from 'react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { AppMarketplace } from '../index'

vi.mock('@/components/layout', () => ({
  PublicLayout: (props: { children: ReactNode }) => (
    <main>{props.children}</main>
  ),
}))
const clients: QueryClient[] = []
afterEach(() => clients.splice(0).forEach((client) => client.clear()))
function renderMarketplace() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <AppMarketplace />
    </QueryClientProvider>
  )
}

test('renders public App metadata as text and opens a protected external link', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: [
        {
          id: 1,
          name: 'Drawing App',
          logo_url: '',
          description: '<script>example</script>',
          url: 'https://app.example',
        },
      ],
    },
  })
  renderMarketplace()
  const link = await screen.findByRole('link', { name: 'Open Drawing App' })
  expect(link).toHaveAttribute('href', 'https://app.example')
  expect(link).toHaveAttribute('target', '_blank')
  expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  expect(screen.getByText('<script>example</script>')).toBeInTheDocument()
  expect(
    screen.getByRole('heading', { name: 'Drawing App', level: 2 })
  ).toBeInTheDocument()
  expect(screen.getByTitle('<script>example</script>')).toHaveClass(
    'line-clamp-3'
  )
  expect(screen.getByText('D')).toBeInTheDocument()
  expect(document.querySelector('main script')).toBeNull()
})

test('distinguishes an empty catalogue from a failed request', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
  renderMarketplace()
  expect(await screen.findByText('No apps available yet')).toBeInTheDocument()
  expect(
    screen.getByText(
      'Published apps will appear here. Check back soon to discover something new.'
    )
  ).toBeInTheDocument()
  expect(screen.queryByText('Failed to load apps')).not.toBeInTheDocument()
})

test('failed requests offer retry instead of an empty catalogue', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('Unavailable'))
  renderMarketplace()
  expect(await screen.findByText('Failed to load apps')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(screen.queryByText('No apps available yet')).not.toBeInTheDocument()
})
