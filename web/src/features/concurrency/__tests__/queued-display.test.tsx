/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ConcurrencyAdminPage } from '../index'

// The chart pulls in a full visualization stack; the queued chip's contract
// lives in the card header, so the chart itself is not under test here.
vi.mock('../components/trend-chart', () => ({
  TrendChart: () => <div data-testid='trend-chart' />,
}))

vi.mock('../hooks/use-concurrency-history', () => ({
  useConcurrencyHistory: () => ({
    data: undefined,
    isLoading: false,
    isError: false,
  }),
}))

vi.mock('../hooks/use-concurrency-snapshot', () => {
  const snapshot = {
    scopes: [],
    generatedAt: 1_767_225_845_000,
    rejections: 0,
    queued: 7,
    degraded: { active: false, lastDegradedAt: 0, consecutiveFailures: 0 },
  }
  return {
    useConcurrencySnapshot: () => ({
      data: snapshot,
      isLoading: false,
      isError: false,
    }),
  }
})

function renderPage() {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, message: '', data: [] },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ConcurrencyAdminPage />
    </QueryClientProvider>
  )
}

describe('concurrency page queued counter', () => {
  test('shows the queued count beside the rejection delta in live mode', async () => {
    renderPage()

    expect(await screen.findByText(/^Queued: 7$/)).toBeInTheDocument()
    expect(screen.getByText(/^Rejected in window: 0$/)).toBeInTheDocument()
    expect(await screen.findByText('Bounded wait queue')).toBeInTheDocument()
  })

  test('hides the queued count when a historical window is selected', async () => {
    renderPage()
    const user = userEvent.setup()

    await screen.findByText(/^Queued: 7$/)
    await user.click(screen.getByRole('combobox'))
    await user.click(await screen.findByRole('option', { name: 'Last hour' }))

    expect(screen.queryByText(/^Queued:/)).not.toBeInTheDocument()
    expect(screen.getByText(/^Rejected in window:/)).toBeInTheDocument()
  })
})

describe('one-screen layout contract (spec §1/§2)', () => {
  test('page root is height-constrained so the page itself never scrolls', () => {
    const { container } = renderPage()

    const root = container.firstElementChild
    expect(root).not.toBeNull()
    // h-full borrows the shell's fixed viewport height; min-h-0 lets the
    // flex column shrink instead of overflowing it.
    expect(root).toHaveClass('h-full', 'min-h-0', 'flex', 'flex-col')
    expect(root).not.toHaveClass('overflow-y-auto', 'overflow-auto')
  })

  test('the elastic region is the only scrolling area and locks to a 2x2 grid at lg', () => {
    const { container } = renderPage()

    const root = container.firstElementChild as HTMLElement
    const region = [...root.children].find((el) =>
      el.classList.contains('flex-1'),
    )
    expect(region).toBeDefined()
    // Below lg the region scrolls internally; at lg+ it stops scrolling and
    // hands overflow to the individual card bodies.
    expect(region).toHaveClass('min-h-0', 'overflow-y-auto')
    expect(region).toHaveClass('lg:overflow-hidden')
    const grid = region?.querySelector('.lg\\:grid-cols-2')
    expect(grid).not.toBeNull()
    expect(grid).toHaveClass('lg:grid-rows-2')
  })

  test('card bodies scroll inside their cell instead of the page', () => {
    renderPage()

    // First card body in DOM order = the queue settings form.
    const body = document.querySelector('[data-slot=card-content]')
    expect(body).not.toBeNull()
    expect(body).toHaveClass('min-h-0', 'overflow-y-auto')
  })
})
