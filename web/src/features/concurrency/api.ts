/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import { api } from '@/lib/api';

import type {
  ChannelRoutingHealthRow,
  ConcurrencyScopeSnapshot,
  ConcurrencySnapshot,
  ConcurrencyTrendPoint,
} from './lib/concurrency-transform';

/**
 * GET /api/concurrency/snapshot — admin-only live in-flight snapshot.
 * Endpoint contract is mirrored verbatim from
 * service/concurrency/limiter.go (ScopeSnapshot) and
 * controller/concurrency.go (SnapshotResponse).
 */
export async function getConcurrencySnapshot(): Promise<ConcurrencySnapshot> {
  const res = await api.get('/api/concurrency/snapshot');
  return res.data;
}

/**
 * GET /api/concurrency/routing-health (AdminAuth + ConcurrencyRead) — EWMA
 * routing health per (channel, model) pair, sorted by ChannelID then Model.
 * Unlike the snapshot endpoint, the rows sit one level deeper: the response
 * body is { success, data: Row[] }, so this returns `res.data.data`. Empty
 * array on cold start (no samples since boot). Field names stay PascalCase
 * (Go struct fields without json tags).
 */
export async function getChannelRoutingHealth(): Promise<
  ChannelRoutingHealthRow[]
> {
  const res = await api.get('/api/concurrency/routing-health');
  return res.data.data;
}

export interface ConcurrencyHistoryResponse {
  points: ConcurrencyTrendPoint[];
  /** Per-bucket PEAK rows for the same window (live snapshot shape). */
  scopeStats: ConcurrencyScopeSnapshot[];
  start: number;
  end: number;
}

/**
 * GET /api/concurrency/history?start=&end= (unix millis) — recorded load
 * samples for a window inside the 7-day retention period, clamped and
 * downsampled server-side. Mirrors controller/concurrency.go
 * GetConcurrencyHistory.
 */
export async function getConcurrencyHistory(
  start: number,
  end: number,
): Promise<ConcurrencyHistoryResponse> {
  const res = await api.get('/api/concurrency/history', {
    params: { start, end },
  });
  return res.data;
}