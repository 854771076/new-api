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
import { afterEach, describe, expect, test } from 'vitest'

import type { UsageLog } from '../../data/schema'
import type { LogOtherData } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'

function makeLog(other: LogOtherData, useTime: number): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: '',
    username: 'user',
    token_name: 'token',
    model_name: 'gpt-5.1',
    quota: 5000,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: useTime,
    is_stream: false,
    channel: 1,
    channel_name: '',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

function renderDialog(
  other: LogOtherData,
  useTime: number,
  isAdmin: boolean
): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClient.setQueryData(
    ['pricing'],
    { data: [], vendors: [] },
    { updatedAt: freshAt }
  )

  render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={makeLog(other, useTime)}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
  return queryClient
}

function rowValue(label: string): string | null {
  return screen.getByText(label).nextElementSibling?.textContent ?? null
}

const queryClients: QueryClient[] = []

afterEach(() => {
  for (const queryClient of queryClients) {
    queryClient.clear()
  }
  queryClients.length = 0
})

describe('log details timing breakdown', () => {
  test('an administrator sees a placeholder for segments that were not measured', () => {
    queryClients.push(
      renderDialog(
        {
          admin_info: {
            timing: { queue_wait_ms: 120, upstream_header_ms: 500 },
          },
        },
        1,
        true
      )
    )

    expect(screen.getByText('Timing breakdown')).toBeInTheDocument()
    expect(rowValue('Queue wait')).toBe('120ms')
    expect(rowValue('Upstream header wait')).toBe('500ms')
    expect(rowValue('Upstream body')).toBe('—')
    expect(rowValue('Upstream first byte')).toBe('—')
    expect(rowValue('Upstream stream')).toBe('—')
    expect(rowValue('Settlement')).toBe('—')
  })

  test('the attributed and unattributed totals are compared against the request duration', () => {
    queryClients.push(
      renderDialog(
        {
          admin_info: {
            timing: { queue_wait_ms: 120, upstream_header_ms: 500 },
          },
        },
        1,
        true
      )
    )

    expect(rowValue('Attributed')).toBe('620ms')
    expect(rowValue('Unattributed')).toBe('380ms')
    expect(
      screen.getByText(
        'The total duration has second precision, so the attributed split is an estimate.'
      )
    ).toBeInTheDocument()
  })

  test('a non-administrator does not see the timing breakdown', () => {
    queryClients.push(
      renderDialog(
        {
          admin_info: {
            timing: { queue_wait_ms: 120, upstream_header_ms: 500 },
          },
        },
        1,
        false
      )
    )

    expect(screen.queryByText('Timing breakdown')).toBeNull()
    expect(screen.queryByText('Unattributed')).toBeNull()
  })
})
