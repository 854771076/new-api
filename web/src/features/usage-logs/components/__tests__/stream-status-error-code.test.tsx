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

const queryClients: QueryClient[] = []

function makeLog(other: LogOtherData): UsageLog {
  return {
    id: 1,
    user_id: 1,
    created_at: 1,
    type: 2,
    content: 'stream ended',
    username: 'user',
    token_name: 'token',
    model_name: 'gpt-test',
    quota: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    use_time: 0,
    is_stream: true,
    channel: 1,
    channel_name: 'channel',
    token_id: 1,
    group: 'default',
    ip: '',
    other: JSON.stringify(other),
    request_id: 'req-1',
    upstream_request_id: '',
  }
}

function renderDetails(other: LogOtherData, isAdmin: boolean): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const freshAt = Date.now() + 60_000
  queryClient.setQueryData(['status'], {}, { updatedAt: freshAt })
  queryClients.push(queryClient)

  render(
    <QueryClientProvider client={queryClient}>
      <DetailsDialog
        log={makeLog(other)}
        isAdmin={isAdmin}
        isRoot={false}
        open
        onOpenChange={() => undefined}
      />
    </QueryClientProvider>
  )
}

afterEach(() => {
  for (const queryClient of queryClients) {
    queryClient.clear()
  }
  queryClients.length = 0
})

describe('usage log stream status error code', () => {
  test('shows the error code of a failed stream', () => {
    renderDetails(
      {
        stream_status: {
          status: 'error',
          end_reason: 'timeout',
          error_code: 'upstream_idle_timeout',
        },
      },
      false
    )

    expect(screen.getByText('Stream Status')).toBeInTheDocument()
    expect(screen.getByText('Error Code')).toBeInTheDocument()
    expect(screen.getByText('upstream_idle_timeout')).toBeInTheDocument()
  })

  test('shows the error code to administrators as well', () => {
    renderDetails(
      {
        stream_status: {
          status: 'error',
          end_reason: 'timeout',
          error_code: 'upstream_idle_timeout',
        },
      },
      true
    )

    expect(screen.getByText('upstream_idle_timeout')).toBeInTheDocument()
  })

  test('omits the error code row when the stream carries no code', () => {
    renderDetails(
      {
        stream_status: {
          status: 'error',
          end_reason: 'eof',
        },
      },
      false
    )

    expect(screen.getByText('Stream Status')).toBeInTheDocument()
    expect(screen.queryByText('Error Code')).toBeNull()
  })
})
