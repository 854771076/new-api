/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RoutingHealthCard } from '../routing-health-card'

// PascalCase field names mirror GET /api/concurrency/routing-health verbatim
// (Go struct fields without json tags) so a camelCased adapter cannot pass.
const ROWS = [
  {
    ChannelID: 3,
    Model: 'Qwen3.6-35B',
    Samples: 28,
    SuccessEWMA: 1,
    LatencyEWMA: 160.65600490546385,
    Multiplier: 2,
    LastSampleAt: '2026-10-14T10:55:12.5125677+08:00',
  },
  {
    ChannelID: 5,
    Model: 'gpt-4o-mini',
    Samples: 12,
    SuccessEWMA: 0.5,
    LatencyEWMA: 843.4,
    Multiplier: 0.42,
    LastSampleAt: '2026-10-14T10:54:02.1234567+08:00',
  },
]

const MULTIPLIER_NOTE =
  'Multiplier: 1.0 = inactive (channel has no slot with concurrency_limit>0, or under 10 samples while warming up); >1 boosted, <1 penalised, range [0.2, 5.0].'

const LOCAL_TIME = /\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/

function renderCard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <RoutingHealthCard />
    </QueryClientProvider>
  )
}

describe('RoutingHealthCard', () => {
  test('shows the empty state when the endpoint reports no samples', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: [] },
    })
    renderCard()

    expect(
      await screen.findByText('No routing health samples yet.')
    ).toBeInTheDocument()
    // Header row + the empty-state message row, and no sample row.
    expect(screen.getAllByRole('row')).toHaveLength(2)
    expect(
      screen.queryByRole('cell', { name: /Channel #/ })
    ).not.toBeInTheDocument()
  })

  test('renders one row per channel-model pair with formatted metrics', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: ROWS },
    })
    renderCard()

    expect(
      await screen.findByRole('cell', { name: 'Channel #3' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('columnheader', { name: 'Samples' })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('cell', { name: 'Qwen3.6-35B' })
    ).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '28' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '100%' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '161' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '2.00' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: 'Channel #5' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '0.42' })).toBeInTheDocument()
    // Header + one row per (channel, model) pair.
    expect(screen.getAllByRole('row')).toHaveLength(3)
    expect(screen.getAllByText(LOCAL_TIME)).toHaveLength(2)
    expect(screen.getByText(MULTIPLIER_NOTE)).toBeInTheDocument()
  })

  test('surfaces the failure state when the endpoint rejects', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('permission_denied'))
    renderCard()

    expect(await screen.findByText('Failed to load')).toBeInTheDocument()
  })
})
