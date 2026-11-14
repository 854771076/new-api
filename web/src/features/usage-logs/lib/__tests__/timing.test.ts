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
import { describe, expect, test } from 'vitest'

import type { RequestTimingInfo } from '../../types'
import { attributedTimingMs, unattributedTimingMs } from '../timing'

describe('latency breakdown split', () => {
  test('sums every measured segment into the attributed total', () => {
    const timing: RequestTimingInfo = {
      queue_wait_ms: 100,
      channel_select_ms: 5,
      upstream_header_ms: 50,
      upstream_body_ms: 900,
      upstream_first_byte_ms: 250,
      upstream_stream_ms: 2000,
      settle_ms: 8,
    }

    expect(attributedTimingMs(timing)).toBe(3313)
  })

  test('reports the part of the total duration no segment accounts for', () => {
    const timing: RequestTimingInfo = {
      upstream_header_ms: 50,
      upstream_body_ms: 900,
    }

    expect(unattributedTimingMs(timing, 10)).toBe(9050)
  })

  test('never reports a negative remainder when the segments exceed the total', () => {
    const timing: RequestTimingInfo = { upstream_header_ms: 5000 }

    expect(unattributedTimingMs(timing, 1)).toBe(0)
  })

  test('treats a request without timing data as fully unattributed', () => {
    expect(attributedTimingMs(undefined)).toBe(0)
    expect(unattributedTimingMs(undefined, 3)).toBe(3000)
  })
})
