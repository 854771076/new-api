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

import assert from 'node:assert/strict'
import { describe, expect, test } from 'vitest'

import {
  deriveKpi,
  deriveVerdict,
  deriveWindowKpi,
  formatRoutingHealthRow,
  pushTrendPoint,
  TREND_WINDOW_POINTS,
  resolveHistoryRange,
  trendRejectionDelta,
  type ChannelRoutingHealthRow,
  type ConcurrencyScopeSnapshot,
  type ConcurrencyTrendPoint,
  type HistoryWindowMode,
  type VerdictInput,
  utilizationTone,
} from '../lib/concurrency-transform'

function scope(
  overrides: Partial<ConcurrencyScopeSnapshot> &
    Pick<ConcurrencyScopeSnapshot, 'scope' | 'kind'>,
): ConcurrencyScopeSnapshot {
  return {
    subjectId: 1,
    model: '*',
    current: 0,
    limit: 0,
    utilization: 0,
    lastUpdatedAt: 0,
    ...overrides,
  }
}

describe('concurrency KPI derivation (I-5)', () => {
  test('totalCurrent counts unique requests via the user axis only', () => {
    // The same three in-flight requests occupy all four buckets below —
    // summing every row would report 12 instead of 3.
    const scopes: ConcurrencyScopeSnapshot[] = [
      scope({ scope: 'key:1:*', kind: 'key', current: 3, limit: 10, utilization: 0.3 }),
      scope({ scope: 'key:1:gpt-4o', kind: 'key', model: 'gpt-4o', current: 3, limit: 0 }),
      scope({ scope: 'user:7:*', kind: 'user', subjectId: 7, current: 3, limit: 4, utilization: 0.75 }),
      scope({ scope: 'channel:1', kind: 'channel', subjectId: 1, current: 3, limit: 5, utilization: 0.6 }),
    ]
    const kpi = deriveKpi(scopes)
    assert.equal(
      kpi.totalCurrent,
      3,
      'one request held on every axis must count once, not once per bucket',
    )
    assert.equal(
      kpi.totalLimit,
      14,
      'the cap sum stays on the labelled per-key / per-user axes (channel cap excluded)',
    )
    assert.ok(Math.abs(kpi.avgUtilization - (0.3 + 0.75) / 2) < 1e-9)
    assert.equal(kpi.cappedScopes, 2)
    assert.equal(kpi.overLimitScopes, 0)
  })

  test('handles empty scope list without dividing by zero', () => {
    const kpi = deriveKpi([])
    assert.equal(kpi.totalCurrent, 0)
    assert.equal(kpi.totalLimit, 0)
    assert.equal(kpi.avgUtilization, 0)
  })

  test('ignores unlimited (limit=0) scopes in the limit sum', () => {
    const kpi = deriveKpi([
      scope({ scope: 'key:1:*', kind: 'key', current: 1, limit: 5, utilization: 0.2 }),
      scope({ scope: 'key:2:*', kind: 'key', subjectId: 2, current: 1, limit: 0 }),
      scope({ scope: 'user:9:*', kind: 'user', subjectId: 9, current: 2, limit: 0 }),
    ])
    assert.equal(kpi.totalCurrent, 2, 'the user row carries the unique count')
    assert.equal(kpi.totalLimit, 5)
  })

  test('channel rows stay visible to the table but never enter any KPI tile', () => {
    // OBS-02 landing: channel rows render in the scope table, yet the KPI
    // labels promise per-key / per-user aggregates, so every tile must
    // ignore the channel axis — including its over-limit state.
    const kpi = deriveKpi([
      scope({ scope: 'channel:42', kind: 'channel', subjectId: 42, current: 5, limit: 1, utilization: 1 }),
      scope({ scope: 'key:1:*', kind: 'key', current: 2, limit: 10, utilization: 0.2 }),
      scope({ scope: 'user:7:*', kind: 'user', subjectId: 7, current: 1, limit: 4, utilization: 0.25 }),
    ])
    assert.equal(kpi.totalCurrent, 1, "the channel bucket's current must not leak")
    assert.equal(kpi.totalLimit, 14, "the channel bucket's limit must not leak")
    assert.equal(kpi.cappedScopes, 2, 'channel caps are not per-key/per-user caps')
    assert.equal(kpi.overLimitScopes, 0, 'the over-limit channel row stays table-only')
  })
})

describe('utilization tone mapping (Y-5 health colour)', () => {
  test('over 100% is destructive', () => {
    assert.equal(utilizationTone(1.01), 'destructive')
    assert.equal(utilizationTone(2.5), 'destructive')
  })

  test('90% to 100% is warning', () => {
    assert.equal(utilizationTone(0.9), 'warning')
    assert.equal(utilizationTone(0.99), 'warning')
  })

  test('under 90% is healthy', () => {
    assert.equal(utilizationTone(0), 'healthy')
    assert.equal(utilizationTone(0.5), 'healthy')
    assert.equal(utilizationTone(0.89), 'healthy')
  })

  test('zero limit is treated as healthy', () => {
    assert.equal(utilizationTone(0, 0), 'healthy')
  })
})

describe('trend ring buffer', () => {
  const point = (t: number, inFlight = 0, rejections = 0): ConcurrencyTrendPoint => ({
    t,
    inFlight,
    rejections,
  })

  test('appends points in order', () => {
    let buf: ConcurrencyTrendPoint[] = []
    buf = pushTrendPoint(buf, point(1000, 1))
    buf = pushTrendPoint(buf, point(4000, 2))
    assert.deepEqual(
      buf.map((p) => p.t),
      [1000, 4000],
    )
  })

  test('ignores duplicate or stale timestamps (StrictMode double-render)', () => {
    let buf: ConcurrencyTrendPoint[] = []
    buf = pushTrendPoint(buf, point(4000, 5))
    const second = pushTrendPoint(buf, point(4000, 5))
    assert.equal(second, buf, 'an identical timestamp must not push twice')
    assert.equal(pushTrendPoint(buf, point(2000, 1)), buf, 'stale renders cannot rewind')
  })

  test('trims to the 5-minute window', () => {
    let buf: ConcurrencyTrendPoint[] = []
    for (let i = 0; i < TREND_WINDOW_POINTS + 10; i++) {
      buf = pushTrendPoint(buf, point(1000 + i * 3000, i))
    }
    assert.equal(buf.length, TREND_WINDOW_POINTS, 'the buffer never exceeds the window')
    assert.equal(buf.at(-1)?.t, 1000 + (TREND_WINDOW_POINTS + 9) * 3000, 'newest point survives')
  })

  test('rejection delta spans the buffered window', () => {
    let buf: ConcurrencyTrendPoint[] = []
    buf = pushTrendPoint(buf, point(1000, 0, 100))
    assert.equal(trendRejectionDelta(buf), 0, 'one sample cannot form a delta')
    buf = pushTrendPoint(buf, point(4000, 0, 130))
    assert.equal(trendRejectionDelta(buf), 30)
    buf = pushTrendPoint(buf, point(7000, 0, 145))
    assert.equal(trendRejectionDelta(buf), 45)
  })

  test('a backend restart mid-window is ignored by rising-edge summation', () => {
    let buf: ConcurrencyTrendPoint[] = []
    buf = pushTrendPoint(buf, point(1000, 0, 0))
    buf = pushTrendPoint(buf, point(4000, 0, 27))
    // restart: the monotonic counter drops back to zero
    buf = pushTrendPoint(buf, point(7000, 0, 0))
    buf = pushTrendPoint(buf, point(10000, 0, 4))
    assert.equal(
      trendRejectionDelta(buf),
      31,
      'rejects before AND after a restart count; the reset itself contributes nothing',
    )
  })

  test('never returns a negative when only a reset is observed', () => {
    let buf: ConcurrencyTrendPoint[] = []
    buf = pushTrendPoint(buf, point(1000, 0, 500))
    buf = pushTrendPoint(buf, point(4000, 0, 7))
    assert.equal(trendRejectionDelta(buf), 0, 'a backward-only jump shows zero, not negative')
  })
})

describe('resolveHistoryRange (time-window control)', () => {
  const now = 1_791_785_000_000

  test('live streams the buffer — no history range', () => {
    assert.equal(resolveHistoryRange('live', {}, now), null)
  })

  test('presets span exactly their advertised duration ending now', () => {
    const cases: Array<[HistoryWindowMode, number]> = [
      ['1h', 60 * 60 * 1000],
      ['6h', 6 * 60 * 60 * 1000],
      ['24h', 24 * 60 * 60 * 1000],
      ['3d', 3 * 24 * 60 * 60 * 1000],
      ['7d', 7 * 24 * 60 * 60 * 1000],
    ]
    for (const [mode, span] of cases) {
      const range = resolveHistoryRange(mode, {}, now)
      assert.ok(range, `${mode} must resolve a range`)
      assert.equal(range.end, now)
      assert.equal(range.start, now - span)
    }
  })

  test('custom waits for both endpoints and normalises reversed order', () => {
    assert.equal(resolveHistoryRange('custom', {}, now), null)
    assert.equal(resolveHistoryRange('custom', { start: new Date(500) }, now), null)
    assert.deepEqual(
      resolveHistoryRange('custom', { start: new Date(500), end: new Date(200) }, now),
      { start: 200, end: 500 },
      'reversed custom ranges swap so the API never sees start > end',
    )
    assert.deepEqual(
      resolveHistoryRange('custom', { start: 100, end: 900 }, now),
      { start: 100, end: 900 },
      'numeric endpoints pass through untouched',
    )
  })
})


describe('deriveWindowKpi (historical window tiles)', () => {
  test('empty window renders zeroed tiles', () => {
    assert.deepEqual(deriveWindowKpi([]), {
      peak: 0,
      avg: 0,
      rejected: 0,
      activeShare: 0,
    })
  })

  test('aggregates peak, mean, rejected delta and active share', () => {
    const points = [
      { t: 1000, inFlight: 0, rejections: 10 },
      { t: 4000, inFlight: 5, rejections: 12 },
      { t: 7000, inFlight: 3, rejections: 12 },
      { t: 10000, inFlight: 0, rejections: 20 },
      { t: 13000, inFlight: 2, rejections: 25 },
    ]
    const wk = deriveWindowKpi(points)
    assert.equal(wk.peak, 5, 'peak is the max in-flight in the window')
    assert.ok(Math.abs(wk.avg - 2) < 1e-9, 'mean over all samples')
    assert.equal(wk.rejected, 15, 'rising-edge sum 2+8+5, resets ignored')
    assert.ok(Math.abs(wk.activeShare - 0.6) < 1e-9, '3 of 5 samples busy')
  })
})

describe('formatRoutingHealthRow (channel load card display)', () => {
  // Field names mirror the PascalCase GET /api/concurrency/routing-health
  // contract (Go struct fields without json tags).
  function row(
    overrides: Partial<ChannelRoutingHealthRow> = {},
  ): ChannelRoutingHealthRow {
    return {
      ChannelID: 3,
      Model: 'Qwen3.6-35B',
      Samples: 28,
      SuccessEWMA: 1,
      LatencyEWMA: 160.65600490546385,
      Multiplier: 2,
      LastSampleAt: '2026-10-14T10:55:12.5125677+08:00',
      ...overrides,
    }
  }

  test('a 0-1 success EWMA renders as a percentage', () => {
    // Locale-independent digits; the separator/space before % may vary by
    // Intl locale, hence the tolerant pattern.
    expect(formatRoutingHealthRow(row({ SuccessEWMA: 1 })).successRate).toMatch(
      /^100\s?%$/u,
    )
    expect(
      formatRoutingHealthRow(row({ SuccessEWMA: 0.98765 })).successRate,
    ).toMatch(/^98[.,]\d{2}\s?%$/u)
  })

  test('latency rounds to whole milliseconds and the multiplier keeps two decimals', () => {
    const display = formatRoutingHealthRow(row())
    expect(display.latencyMs).toBe('161')
    expect(display.multiplier).toBe('2.00')
    expect(
      formatRoutingHealthRow(row({ Multiplier: 0.851 })).multiplier,
    ).toBe('0.85')
    expect(formatRoutingHealthRow(row({ Multiplier: 1 })).multiplier).toBe(
      '1.00',
    )
  })

  test('an RFC3339 sample time renders as local YYYY-MM-DD HH:mm:ss', () => {
    // Independent oracle: Date's local getters describe "local time display"
    // without reusing the formatter under test.
    const when = new Date('2026-10-14T10:55:12.5125677+08:00')
    const pad = (n: number) => String(n).padStart(2, '0')
    const expected = `${when.getFullYear()}-${pad(when.getMonth() + 1)}-${pad(when.getDate())} ${pad(when.getHours())}:${pad(when.getMinutes())}:${pad(when.getSeconds())}`
    expect(formatRoutingHealthRow(row()).lastSampleAt).toBe(expected)
  })

  test('an unparsable sample time renders a dash instead of "Invalid Date"', () => {
    const display = formatRoutingHealthRow(
      row({ LastSampleAt: 'not-a-timestamp' }),
    )
    expect(display.lastSampleAt).toBe('-')
  })
})

describe('deriveVerdict (verdict band tone ladder)', () => {
  const input = (overrides: Partial<VerdictInput> = {}): VerdictInput => ({
    scopes: [],
    rejected: 0,
    queued: 0,
    degraded: undefined,
    ...overrides,
  })
  const degraded = {
    active: true,
    lastDegradedAt: 1_767_225_845_000,
    consecutiveFailures: 2,
  }

  test('an over-cap key bucket is destructive with the action title and carries the counts', () => {
    const verdict = deriveVerdict(
      input({
        scopes: [
          scope({
            scope: 'key:1:*',
            kind: 'key',
            current: 3,
            limit: 2,
            utilization: 1.5,
          }),
        ],
        rejected: 2,
        queued: 5,
      }),
    )
    assert.equal(verdict.tone, 'destructive')
    assert.equal(verdict.titleKey, 'Action needed — {{n}} buckets over cap')
    assert.equal(verdict.overLimit, 1)
    assert.equal(verdict.rejected, 2)
    assert.equal(verdict.queued, 5)
  })

  test('an over-cap channel bucket never raises the band (key/user axis only)', () => {
    const verdict = deriveVerdict(
      input({
        scopes: [
          scope({
            scope: 'channel:9',
            kind: 'channel',
            subjectId: 9,
            current: 5,
            limit: 1,
            utilization: 5,
          }),
        ],
      }),
    )
    assert.equal(verdict.tone, 'success')
    assert.equal(verdict.overLimit, 0)
  })

  test('active backend degradation is warning, but over-limit outranks it', () => {
    const degradedVerdict = deriveVerdict(input({ degraded }))
    assert.equal(degradedVerdict.tone, 'warning')
    assert.equal(degradedVerdict.titleKey, 'Backend degraded')
    assert.equal(degradedVerdict.degraded, true)

    const outranked = deriveVerdict(
      input({
        degraded,
        scopes: [
          scope({
            scope: 'user:7:*',
            kind: 'user',
            current: 9,
            limit: 4,
            utilization: 2.25,
          }),
        ],
      }),
    )
    assert.equal(outranked.tone, 'destructive', 'ladder step 1 beats step 2')
    assert.equal(outranked.degraded, true, 'the chip state is still reported')
  })

  test('rejections and queueing each render a warning with their own title', () => {
    const rejectedVerdict = deriveVerdict(input({ rejected: 3 }))
    assert.equal(rejectedVerdict.tone, 'warning')
    assert.equal(rejectedVerdict.titleKey, 'Rejections observed in this window')

    const queuedVerdict = deriveVerdict(input({ queued: 1 }))
    assert.equal(queuedVerdict.tone, 'warning')
    assert.equal(queuedVerdict.titleKey, 'Requests waiting in the bounded queue')
  })

  test('a clean gateway renders the success title', () => {
    const verdict = deriveVerdict(input())
    assert.equal(verdict.tone, 'success')
    assert.equal(verdict.titleKey, 'All buckets within caps')
    assert.equal(verdict.degraded, false)
  })
})
