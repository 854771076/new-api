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
import type { RequestTimingInfo } from '../types'

/**
 * The measured latency segments of one request, in the order an operator reads
 * them: gateway queue wait, channel selection, upstream header wait, upstream
 * response body, upstream first byte, upstream stream, settlement. The keys
 * mirror the backend snapshot keys one-to-one.
 */
export const TIMING_SEGMENT_KEYS = [
  'queue_wait_ms',
  'channel_select_ms',
  'upstream_header_ms',
  'upstream_body_ms',
  'upstream_first_byte_ms',
  'upstream_stream_ms',
  'settle_ms',
] as const

export type TimingSegmentKey = (typeof TIMING_SEGMENT_KEYS)[number]

/**
 * The label of each measured segment. The values are the English source
 * strings used as i18n keys.
 */
export const TIMING_SEGMENT_LABELS: Record<TimingSegmentKey, string> = {
  queue_wait_ms: 'Queue wait',
  channel_select_ms: 'Channel selection',
  upstream_header_ms: 'Upstream header wait',
  upstream_body_ms: 'Upstream body',
  upstream_first_byte_ms: 'Upstream first byte',
  upstream_stream_ms: 'Upstream stream',
  settle_ms: 'Settlement',
}

/** One measured latency segment, in milliseconds. */
export function timingSegmentMs(
  timing: RequestTimingInfo | undefined,
  key: TimingSegmentKey
): number {
  if (!timing) return 0
  return timing[key] ?? 0
}

/**
 * Sum of every measured segment.
 *
 * The segments are a breakdown, not a partition: connection setup, retries and
 * the parts of the request the gateway does not measure are left out, so the
 * sum is usually well below the total duration. The difference is the part of
 * the request no segment accounts for.
 */
export function attributedTimingMs(
  timing: RequestTimingInfo | undefined
): number {
  if (!timing) return 0
  return TIMING_SEGMENT_KEYS.reduce((sum, key) => sum + (timing[key] ?? 0), 0)
}

/**
 * The part of the total duration that no measured segment accounts for.
 *
 * `use_time` is recorded with second precision, so this is an order-of-
 * magnitude estimate rather than an exact figure. A negative difference means
 * the segments already exceed the total (possible when a request was retried),
 * and is clamped to zero.
 */
export function unattributedTimingMs(
  timing: RequestTimingInfo | undefined,
  totalSeconds: number
): number {
  const totalMs = Math.max(0, totalSeconds) * 1000
  return Math.max(0, totalMs - attributedTimingMs(timing))
}
