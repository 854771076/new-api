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
import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'

import type { RequestTimingInfo } from '../../types'
import { TimingMetricsCell } from '../timing-metrics-cell'

describe('timing metrics cell', () => {
  test('without timing data the cell shows only First token and Duration', () => {
    render(
      <TimingMetricsCell
        useTimeSec={12}
        completionTokens={100}
        frtMs={1500}
        isStream
      />
    )

    expect(screen.getByText('First token')).toBeInTheDocument()
    expect(screen.getByText('Duration')).toBeInTheDocument()
    expect(screen.queryByText('Queue wait')).not.toBeInTheDocument()
    expect(screen.queryByText('Upstream header wait')).not.toBeInTheDocument()
  })

  test('with timing data every measured segment is listed with its duration', () => {
    const timing: RequestTimingInfo = {
      queue_wait_ms: 120,
      channel_select_ms: 4,
      upstream_header_ms: 1020000,
      upstream_body_ms: 750,
      upstream_first_byte_ms: 250,
      settle_ms: 8,
      attempts: 2,
    }

    render(
      <TimingMetricsCell
        useTimeSec={60}
        completionTokens={100}
        frtMs={250}
        isStream
        timing={timing}
      />
    )

    expect(screen.getByText('Queue wait')).toBeInTheDocument()
    expect(screen.getByText('120ms')).toBeInTheDocument()
    expect(screen.getByText('Channel selection')).toBeInTheDocument()
    expect(screen.getByText('4ms')).toBeInTheDocument()
    expect(screen.getByText('Upstream header wait')).toBeInTheDocument()
    expect(screen.getByText('17m 0s')).toBeInTheDocument()
    expect(screen.getByText('Upstream body')).toBeInTheDocument()
    expect(screen.getByText('750ms')).toBeInTheDocument()
    expect(screen.getByText('Upstream first byte')).toBeInTheDocument()
    expect(screen.getByText('250ms')).toBeInTheDocument()
    expect(screen.getByText('Settlement')).toBeInTheDocument()
    expect(screen.getByText('8ms')).toBeInTheDocument()
  })

  test('a non-streaming request lists the upstream response body segment', () => {
    render(
      <TimingMetricsCell
        useTimeSec={30}
        completionTokens={100}
        isStream={false}
        timing={{ upstream_header_ms: 40, upstream_body_ms: 29000 }}
      />
    )

    expect(screen.getByText('Upstream body')).toBeInTheDocument()
    expect(screen.getByText('29.0s')).toBeInTheDocument()
    expect(screen.queryByText('Upstream first byte')).not.toBeInTheDocument()
    expect(screen.queryByText('Upstream stream')).not.toBeInTheDocument()
  })

  test('segments that were not measured are not listed', () => {
    render(
      <TimingMetricsCell
        useTimeSec={12}
        completionTokens={100}
        isStream={false}
        timing={{
          queue_wait_ms: 0,
          upstream_header_ms: 500,
          upstream_body_ms: 0,
          upstream_first_byte_ms: 0,
          upstream_stream_ms: 0,
        }}
      />
    )

    expect(screen.getByText('Upstream header wait')).toBeInTheDocument()
    expect(screen.getByText('500ms')).toBeInTheDocument()
    expect(screen.queryByText('Queue wait')).not.toBeInTheDocument()
    expect(screen.queryByText('Upstream body')).not.toBeInTheDocument()
    expect(screen.queryByText('Upstream first byte')).not.toBeInTheDocument()
    expect(screen.queryByText('Upstream stream')).not.toBeInTheDocument()
    expect(screen.queryByText('Settlement')).not.toBeInTheDocument()
  })
})
