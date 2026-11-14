/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { QueueSettingsCard } from '../queue-settings-card'

// Stored server state: deliberately different from the contract defaults so
// the render case proves values come from GET /api/option/, not fallbacks.
const SERVER_OPTIONS = [
  { key: 'concurrency.queue_enabled', value: 'false' },
  { key: 'concurrency.queue_depth_ratio', value: '6' },
  { key: 'concurrency.queue_depth_cap', value: '256' },
  { key: 'concurrency.queue_max_wait_seconds', value: '20' },
]

function mockOptionApi() {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, message: '', data: SERVER_OPTIONS },
  })
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true, message: '' } })
  return { get, put }
}

function renderCard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <QueueSettingsCard />
    </QueryClientProvider>
  )
}

// The controls stay disabled until GET /api/option/ resolves; the stored
// depth ratio (6 ≠ contract default 4) rendering is that exact moment.
async function waitForLoadedForm() {
  const ratio = await screen.findByDisplayValue('6')
  expect(ratio).toBeEnabled()
  return ratio
}

describe('QueueSettingsCard', () => {
  beforeEach(() => {
    mockOptionApi()
  })

  test('loads the stored queue options into the form controls', async () => {
    renderCard()

    await waitForLoadedForm()
    expect(
      screen.getByRole('switch', { name: 'Enable queueing' })
    ).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByLabelText('Depth cap')).toHaveValue(256)
    expect(screen.getByLabelText('Max wait')).toHaveValue(20)
  })

  test('rejects a depth ratio above 64 and skips the option update', async () => {
    const { put } = mockOptionApi()
    renderCard()
    const user = userEvent.setup()

    const ratio = await waitForLoadedForm()
    // jsdom's input[type=number] has no selection API, so typing appends to
    // the stored digit: 6 -> 65 is out of range (0-64).
    await user.click(ratio)
    await user.type(ratio, '5')
    expect(
      await screen.findByText('Allowed values: 0 to 64')
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: 'Save queue settings' })
    )
    await act(async () => {})
    expect(screen.getByText('Allowed values: 0 to 64')).toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  test('clearing a numeric field keeps the stored value and blocks no save', async () => {
    const { put } = mockOptionApi()
    renderCard()
    const user = userEvent.setup()

    await waitForLoadedForm()
    await user.clear(screen.getByLabelText('Max wait'))
    await user.click(
      screen.getByRole('button', { name: 'Save queue settings' })
    )
    await act(async () => {})
    // An emptied input must never leak NaN / '' into form state: no range
    // error appears and nothing is submitted (the value stays at 20).
    expect(
      document.querySelector('[data-slot="form-message"]')
    ).not.toBeInTheDocument()
    expect(put).not.toHaveBeenCalled()
  })

  test('saving submits only the edited option as a string value', async () => {
    const { put } = mockOptionApi()
    renderCard()
    const user = userEvent.setup()

    const ratio = await waitForLoadedForm()
    // Appending 4 turns the stored 6 into 64 (inside 0-64) — an edit whose
    // only changed option must be the depth ratio.
    await user.click(ratio)
    await user.type(ratio, '4')
    await user.click(
      screen.getByRole('button', { name: 'Save queue settings' })
    )

    await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
    expect(put).toHaveBeenCalledWith('/api/option/', {
      key: 'concurrency.queue_depth_ratio',
      value: '64',
    })
  })

  test('toggling the switch saves the enabled flag as a string value', async () => {
    const { put } = mockOptionApi()
    renderCard()
    const user = userEvent.setup()

    await waitForLoadedForm()
    const enable = screen.getByRole('switch', { name: 'Enable queueing' })
    expect(enable).toHaveAttribute('aria-checked', 'false')
    await user.click(enable)
    expect(enable).toHaveAttribute('aria-checked', 'true')

    await user.click(
      screen.getByRole('button', { name: 'Save queue settings' })
    )
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/option/', {
        key: 'concurrency.queue_enabled',
        value: 'true',
      })
    )
  })

  test('saving without edits issues no option update', async () => {
    const { put } = mockOptionApi()
    renderCard()
    const user = userEvent.setup()

    await waitForLoadedForm()
    await user.click(
      screen.getByRole('button', { name: 'Save queue settings' })
    )
    // Flush the submit promise chain before the negative assertion so an
    // "always PUT every key" regression cannot slip through microtasks.
    await act(async () => {})
    expect(put).not.toHaveBeenCalled()
  })
})
