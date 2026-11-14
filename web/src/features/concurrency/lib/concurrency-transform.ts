/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

// Pure transforms for the admin concurrency page. No React, no network —
// kept separate from hooks so they stay trivially unit-testable.
//
// Mirrors the JSON envelope returned by GET /api/concurrency/snapshot:
//   { scopes: ScopeSnapshot[], generatedAt: number, rejections: number,
//     queued: number, degraded: DegradedStatus }
// where ScopeSnapshot is documented in service/concurrency/limiter.go,
// rejections mirrors controller/concurrency.go SnapshotResponse, and
// DegradedStatus below mirrors the same envelope's degraded object.

import { formatDateTimeStr, formatPercent } from '@/lib/format';

export type ConcurrencyKind = 'key' | 'user' | 'channel';

export interface ConcurrencyScopeSnapshot {
  scope: string;
  kind: ConcurrencyKind;
  subjectId: number;
  model: string;
  current: number;
  limit: number;
  utilization: number;
  lastUpdatedAt: number;
}

/**
 * D-14 observation state of the concurrency backend, mirroring the
 * `degraded` object of GET /api/concurrency/snapshot
 * (service/concurrency/limiter.go DegradedStatus):
 *   - active — a Redis outage fail-open window is in progress
 *   - lastDegradedAt — unix milliseconds of the most recent degradation,
 *     0 when the backend never degraded (MemoryLimiter is always healthy)
 *   - consecutiveFailures — failures since the last recovery (0 when healthy)
 */
export interface ConcurrencyDegradedStatus {
  active: boolean;
  lastDegradedAt: number;
  consecutiveFailures: number;
}

export interface ConcurrencySnapshot {
  scopes: ConcurrencyScopeSnapshot[];
  generatedAt: number;
  /** Monotonic client-visible concurrency-429 count since process start. */
  rejections: number;
  /**
   * Requests currently waiting in the bounded queue (contract Y-3 queue
   * settings). Optional so an older backend without the queue feature still
   * renders — the page hides the chip when the field is absent.
   */
  queued?: number;
  degraded: ConcurrencyDegradedStatus;
}

/** Health-tone for the visualization page (Y-5). */
export type ConcurrencyTone = 'healthy' | 'warning' | 'destructive';

export interface ConcurrencyKpi {
  totalCurrent: number;
  totalLimit: number;
  avgUtilization: number;
  cappedScopes: number;
  overLimitScopes: number;
}

function isCapped(limit: number): boolean {
  return limit > 0;
}

export function deriveKpi(scopes: ConcurrencyScopeSnapshot[]): ConcurrencyKpi {
  if (scopes.length === 0) {
    return {
      totalCurrent: 0,
      totalLimit: 0,
      avgUtilization: 0,
      cappedScopes: 0,
      overLimitScopes: 0,
    };
  }

  // "Total in-flight requests" is a count of REQUESTS, not of buckets: the
  // same request simultaneously occupies a key-global bucket, a per-model
  // key bucket, a user bucket, and a channel bucket. Every relay request
  // holds exactly ONE user-axis bucket (middleware buildScopes always emits
  // it), so summing only the user rows yields the true unique in-flight
  // count — summing every row would multiply the same request by its axis
  // count (the pre-fix page showed 15 for 5 real requests).
  let totalCurrent = 0;
  let totalLimit = 0;
  let cappedScopes = 0;
  let overLimitScopes = 0;
  // Average across the capped set only — unlimited scopes (limit=0) would
  // dilute the signal with 0/0 utilization values. Cap aggregates stay on
  // the key/user axes to match the KPI labels ("per-key / per-user caps");
  // channel buckets surface through the scope table instead.
  let cappedUtilSum = 0;

  for (const s of scopes) {
    if (s.kind === 'user') {
      totalCurrent += s.current;
    }
    if (s.kind === 'channel') {
      continue;
    }
    if (isCapped(s.limit)) {
      cappedScopes += 1;
      totalLimit += s.limit;
      cappedUtilSum += s.utilization;
      if (s.current > s.limit) {
        overLimitScopes += 1;
      }
    }
  }

  const avgUtilization = cappedScopes === 0 ? 0 : cappedUtilSum / cappedScopes;
  return {
    totalCurrent,
    totalLimit,
    avgUtilization,
    cappedScopes,
    overLimitScopes,
  };
}

/**
 * Map a scope's utilization (and optionally its limit) to a UI health tone
 * per contract Y-5:
 *   - limit <= 0           → unlimited, never unhealthy
 *   - utilization > 1.0    → destructive (over-limit, requests rejected)
 *   - utilization >= 0.9   → warning
 *   - otherwise            → healthy
 *
 * `limit` is optional; pass it when available so unlimited scopes render
 * healthy even if utilization is reported as 0 by the snapshot.
 */
export function utilizationTone(
  utilization: number,
  limit: number = Number.POSITIVE_INFINITY,
): ConcurrencyTone {
  if (limit <= 0) {
    return 'healthy';
  }
  if (utilization > 1) {
    return 'destructive';
  }
  if (utilization >= 0.9) {
    return 'warning';
  }
  return 'healthy';
}

/** Sort scopes by descending utilization so the hottest buckets bubble up. */
export function sortByUtilization(
  scopes: ConcurrencyScopeSnapshot[],
): ConcurrencyScopeSnapshot[] {
  return [...scopes].sort((a, b) => b.utilization - a.utilization);
}

/**
 * One sample of the frontend trend ring buffer. `t` is the snapshot's
 * generatedAt (unix millis), `inFlight` the unique-request KPI at that
 * instant, `rejections` the backend's monotonic 429 counter.
 */
export interface ConcurrencyTrendPoint {
  t: number;
  inFlight: number;
  rejections: number;
}

/** Buffer size: 100 samples × the 3s poll = 5 minutes of history. */
export const TREND_WINDOW_POINTS = 100;

/**
 * Appends a snapshot to the trend buffer, dropping the oldest point once the
 * window is full. Duplicate or stale timestamps are ignored so React's
 * StrictMode double-render cannot double-count a sample, and a server
 * restart (generatedAt going backwards is impossible, but a stale queued
 * render could re-push) cannot corrupt the series. Pure: returns a new
 * array (or the same reference when nothing changes).
 */
export function pushTrendPoint(
  buffer: ConcurrencyTrendPoint[],
  point: ConcurrencyTrendPoint,
): ConcurrencyTrendPoint[] {
  const last = buffer.at(-1);
  if (last && last.t >= point.t) {
    return buffer;
  }
  const next = [...buffer, point];
  if (next.length > TREND_WINDOW_POINTS) {
    return next.slice(next.length - TREND_WINDOW_POINTS);
  }
  return next;
}

/**
 * Concurrency 429s observed inside the buffered window, computed as the sum
 * of POSITIVE increments between consecutive samples. A backend restart
 * mid-window resets the monotonic counter (a backward jump would poison a
 * naive last-minus-first delta — the chip would show 0 despite a window full
 * of rejects); rising edges count every generation of rejects exactly once
 * and ignore the reset. For an uninterrupted monotonic series this equals
 * last-first. Fewer than two samples cannot form a delta yet.
 */
export function trendRejectionDelta(
  buffer: ConcurrencyTrendPoint[],
): number {
  if (buffer.length < 2) {
    return 0;
  }
  let total = 0;
  for (let i = 1; i < buffer.length; i++) {
    const step = buffer[i].rejections - buffer[i - 1].rejections;
    if (step > 0) {
      total += step;
    }
  }
  return total;
}
/**
 * Trend-chart time-window modes: `live` streams the rolling in-memory
 * buffer; every other mode loads recorded history for a fixed span (or a
 * custom range) from GET /api/concurrency/history.
 */
export type HistoryWindowMode =
  | 'live'
  | '1h'
  | '6h'
  | '24h'
  | '3d'
  | '7d'
  | 'custom';

export interface HistoryRange {
  start: number;
  end: number;
}

const PRESET_WINDOW_MS: Partial<Record<HistoryWindowMode, number>> = {
  '1h': 60 * 60 * 1000,
  '6h': 6 * 60 * 60 * 1000,
  '24h': 24 * 60 * 60 * 1000,
  '3d': 3 * 24 * 60 * 60 * 1000,
  '7d': 7 * 24 * 60 * 60 * 1000,
};

/**
 * Resolves a window mode to the [start, end] millis the history endpoint
 * expects. `live` and an incomplete custom range return null (the caller
 * keeps the streaming chart / waits for both dates); a custom range with
 * reversed ends is normalised so callers never send start > end. `now` is a
 * parameter so tests are deterministic.
 */
export function resolveHistoryRange(
  mode: HistoryWindowMode,
  custom: { start?: Date | number; end?: Date | number },
  now: number,
): HistoryRange | null {
  if (mode === 'live') {
    return null;
  }
  if (mode === 'custom') {
    const start =
      custom.start instanceof Date ? custom.start.getTime() : custom.start;
    const end = custom.end instanceof Date ? custom.end.getTime() : custom.end;
    if (!start || !end) {
      return null;
    }
    return start <= end ? { start, end } : { start: end, end: start };
  }
  const span = PRESET_WINDOW_MS[mode];
  return span ? { start: now - span, end: now } : null;
}

/**
 * Windowed KPI set rendered INSTEAD of the live tiles whenever the user has
 * selected a historical window — the tiles must describe the same data the
 * chart shows, otherwise the row keeps ticking on live values under a
 * static curve (the "flashing" mismatch). Derived purely from the fetched
 * points: peak / mean in-flight, rejected delta (rising-edge sum, so a
 * mid-window backend restart still counts correctly), and the share of
 * sampled time with at least one request in flight.
 */
export interface ConcurrencyWindowKpi {
  peak: number;
  avg: number;
  rejected: number;
  activeShare: number;
}

export function deriveWindowKpi(
  points: ConcurrencyTrendPoint[],
): ConcurrencyWindowKpi {
  if (points.length === 0) {
    return { peak: 0, avg: 0, rejected: 0, activeShare: 0 };
  }
  let peak = 0;
  let sum = 0;
  let active = 0;
  for (const p of points) {
    peak = Math.max(peak, p.inFlight);
    sum += p.inFlight;
    if (p.inFlight > 0) {
      active += 1;
    }
  }
  return {
    peak,
    avg: sum / points.length,
    rejected: trendRejectionDelta(points),
    activeShare: active / points.length,
  };
}

/**
 * One row of GET /api/concurrency/routing-health — the EWMA routing health
 * of one (channel, model) pair. Field names mirror the Go struct fields
 * verbatim (the endpoint emits no json tags), which is why they stay
 * PascalCase instead of following frontend naming conventions.
 *   - SuccessEWMA — exponentially-weighted success ratio in [0, 1]
 *   - LatencyEWMA — exponentially-weighted mean latency in milliseconds
 *   - Multiplier  — effective routing weight: 1.0 means inactive (the
 *                   channel has no slot with concurrency_limit>0, or fewer
 *                   than 10 samples while warming up), >1 boosted, <1
 *                   penalised, clamped to [0.2, 5.0]
 *   - LastSampleAt — RFC3339 timestamp with offset
 */
export interface ChannelRoutingHealthRow {
  ChannelID: number;
  Model: string;
  Samples: number;
  SuccessEWMA: number;
  LatencyEWMA: number;
  Multiplier: number;
  LastSampleAt: string;
}

/** Pre-formatted display strings for one routing-health table row. */
export interface ChannelRoutingHealthDisplay {
  successRate: string;
  latencyMs: string;
  multiplier: string;
  lastSampleAt: string;
}

/**
 * Formats a routing-health row for the channel load card: success EWMA as a
 * percentage, latency rounded to whole milliseconds, multiplier pinned to
 * two decimals, and the RFC3339 sample time as local YYYY-MM-DD HH:mm:ss.
 * An unparsable timestamp renders as a dash so a bad value can never surface
 * "Invalid Date" in the table.
 */
export function formatRoutingHealthRow(
  row: ChannelRoutingHealthRow,
): ChannelRoutingHealthDisplay {
  const when = new Date(row.LastSampleAt);
  return {
    successRate: formatPercent(row.SuccessEWMA * 100),
    latencyMs: String(Math.round(row.LatencyEWMA)),
    multiplier: row.Multiplier.toFixed(2),
    lastSampleAt: Number.isNaN(when.getTime()) ? '-' : formatDateTimeStr(when),
  };
}

/** Verdict band tone: success = all clear, warning = watch, destructive = act. */
export type ConcurrencyVerdictTone = 'success' | 'warning' | 'destructive';

/**
 * Verdict band derivation (one-screen spec §3). The tone ladder answers
 * "does the operator need to act?" before any KPI is read:
 *   1. a key/user bucket above its cap → destructive (channel rows stay
 *      table-only, matching deriveKpi and the KPI card description);
 *   2. active backend degradation     → warning;
 *   3. rejections in the window       → warning;
 *   4. requests waiting in the queue  → warning;
 *   5. otherwise                      → success.
 * `titleKey` is the i18n key of the band's one-line headline (only the
 * over-cap title interpolates {n}); the counts ride along so the band's four
 * chips render from the same derivation as the KPI/trend cards — one pure
 * call per poll, no extra request.
 */
export interface ConcurrencyVerdict {
  tone: ConcurrencyVerdictTone;
  titleKey: string;
  overLimit: number;
  rejected: number;
  queued: number;
  degraded: boolean;
}

export interface VerdictInput {
  scopes: ConcurrencyScopeSnapshot[];
  /** Concurrency 429s observed inside the displayed trend window. */
  rejected: number;
  /** Requests currently waiting in the bounded queue (live snapshot). */
  queued: number;
  degraded?: ConcurrencyDegradedStatus;
}

export function deriveVerdict(input: VerdictInput): ConcurrencyVerdict {
  const overLimit = deriveKpi(input.scopes).overLimitScopes;
  const rejected = input.rejected;
  const queued = input.queued;
  const degraded = Boolean(input.degraded?.active);

  let tone: ConcurrencyVerdictTone = 'success';
  let titleKey = 'All buckets within caps';
  if (overLimit > 0) {
    tone = 'destructive';
    titleKey = 'Action needed — {{n}} buckets over cap';
  } else if (degraded) {
    tone = 'warning';
    titleKey = 'Backend degraded';
  } else if (rejected > 0) {
    tone = 'warning';
    titleKey = 'Rejections observed in this window';
  } else if (queued > 0) {
    tone = 'warning';
    titleKey = 'Requests waiting in the bounded queue';
  }
  return { tone, titleKey, overLimit, rejected, queued, degraded };
}
