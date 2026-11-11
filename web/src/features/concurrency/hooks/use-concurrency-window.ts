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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  deriveKpi,
  deriveVerdict,
  pushTrendPoint,
  resolveHistoryRange,
  trendRejectionDelta,
  type ConcurrencyScopeSnapshot,
  type ConcurrencySnapshot,
  type ConcurrencyTrendPoint,
  type ConcurrencyVerdict,
  type HistoryRange,
  type HistoryWindowMode,
} from '../lib/concurrency-transform'
import { useConcurrencyHistory } from './use-concurrency-history'

/** Endpoints of the operator-picked custom window (both must be set). */
export interface CustomWindowRange {
  start?: Date
  end?: Date
}

/** Derived inputs the concurrency page renders from (see ConcurrencyAdminPage). */
export interface ConcurrencyWindowState {
  scopes: ConcurrencyScopeSnapshot[]
  windowMode: HistoryWindowMode
  selectWindow: (mode: HistoryWindowMode) => void
  customRange: CustomWindowRange
  onCustomRange: (next: CustomWindowRange) => void
  isLive: boolean
  historyLoading: boolean
  displayPoints: ConcurrencyTrendPoint[]
  emptyMessage: string | undefined
  rejectedInWindow: number
  verdict: ConcurrencyVerdict
  tableScopes: ConcurrencyScopeSnapshot[]
  overCapBuckets: number
}

/**
 * Trend ring buffer + time-window state for the concurrency admin page
 * (moved out of index.tsx unchanged): appends one trend point per distinct
 * snapshot, resolves the selected window's history range, and derives the
 * chart/table/KPI inputs for BOTH the live buffer and recorded history.
 */
export function useConcurrencyWindow(
  snapshot: ConcurrencySnapshot | undefined
): ConcurrencyWindowState {
  const { t } = useTranslation()
  const scopes = snapshot?.scopes ?? []

  // Trend ring buffer: appended once per distinct snapshot (guarded by
  // generatedAt so a refetch of the same sample cannot double-count; the
  // pure pushTrendPoint additionally dedups by timestamp for StrictMode).
  const [trend, setTrend] = useState<ConcurrencyTrendPoint[]>([])
  const lastPushed = useRef<number | null>(null)
  useEffect(() => {
    if (!snapshot || snapshot.generatedAt === lastPushed.current) {
      return
    }
    lastPushed.current = snapshot.generatedAt
    setTrend((prev) =>
      pushTrendPoint(prev, {
        t: snapshot.generatedAt,
        inFlight: deriveKpi(snapshot.scopes).totalCurrent,
        rejections: snapshot.rejections ?? 0,
      })
    )
  }, [snapshot])

  // Time window: `live` streams the buffer above; presets resolve their
  // range ON selection (stable query key — not per render); custom applies
  // once both endpoints are picked, normalised by resolveHistoryRange.
  const [windowMode, setWindowMode] = useState<HistoryWindowMode>('live')
  const [customRange, setCustomRange] = useState<CustomWindowRange>({})
  const [range, setRange] = useState<HistoryRange | null>(null)

  const selectWindow = (mode: HistoryWindowMode) => {
    setWindowMode(mode)
    if (mode === 'live') {
      setRange(null)
      return
    }
    if (mode !== 'custom') {
      setRange(resolveHistoryRange(mode, {}, Date.now()))
    } else {
      // Keep the previous custom range's points visible until replaced.
      setRange(resolveHistoryRange('custom', customRange, Date.now()))
    }
  }
  const onCustomRange = (next: CustomWindowRange) => {
    setCustomRange(next)
    setRange(resolveHistoryRange('custom', next, Date.now()))
  }

  const isLive = windowMode === 'live'
  const historyQuery = useConcurrencyHistory(range)
  const displayPoints: ConcurrencyTrendPoint[] = isLive
    ? trend
    : (historyQuery.data?.points ?? [])

  // Bucket list: the window decides WHICH keys appear (membership from
  // scopeStats); the numbers stay REAL-TIME — live current over the
  // configured cap. Operators asked for "which keys existed back then",
  // not historical counters; mixing window peaks with today's caps would
  // show nonsense like 6/3 after a cap was lowered.
  const liveByScope = new Map(scopes.map((sc) => [sc.scope, sc]))
  const windowBuckets = (historyQuery.data?.scopeStats ?? []).map((ws) => {
    const current = liveByScope.get(ws.scope)?.current ?? 0
    const limit = ws.limit
    return {
      ...ws,
      current,
      utilization: limit > 0 ? current / limit : 0,
    }
  })

  // Placeholder copy for <2 points: live keeps "collecting", an incomplete
  // custom range asks for both dates, otherwise the window has no samples.
  let emptyMessage: string | undefined
  if (!isLive) {
    if (windowMode === 'custom' && (!customRange.start || !customRange.end)) {
      emptyMessage = t('Pick a start and end time.')
    } else if (historyQuery.isError) {
      // A failed fetch (expired session, server down) must NOT masquerade
      // as an empty window — that reads as 'no data recorded'.
      emptyMessage = t('Failed to load this window.')
    } else if (
      !historyQuery.isLoading &&
      (historyQuery.data?.points.length ?? 0) < 2
    ) {
      emptyMessage = t('No samples in this window.')
    }
  }

  const rejectedInWindow = trendRejectionDelta(displayPoints)
  // Same sources as the KPI row and the trend chart — no extra request.
  const verdict = deriveVerdict({
    scopes,
    rejected: rejectedInWindow,
    queued: snapshot?.queued ?? 0,
    degraded: snapshot?.degraded,
  })

  // The over-cap badge counts EVERY kind rendered in the table (the KPI /
  // verdict "Over-limit" figure stays key+user only, per contract).
  const tableScopes = isLive ? scopes : windowBuckets
  const overCapBuckets = tableScopes.filter(
    (s) => s.limit > 0 && s.current > s.limit
  ).length

  return {
    scopes,
    windowMode,
    selectWindow,
    customRange,
    onCustomRange,
    isLive,
    historyLoading: historyQuery.isLoading,
    displayPoints,
    emptyMessage,
    rejectedInWindow,
    verdict,
    tableScopes,
    overCapBuckets,
  }
}
