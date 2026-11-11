/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import { useQuery, type UseQueryResult } from '@tanstack/react-query';

import { getConcurrencySnapshot } from '../api';
import type { ConcurrencySnapshot } from '../lib/concurrency-transform';

/**
 * Polls GET /api/concurrency/snapshot every 3 seconds (≤5s lag per
 * contract Y-3) with a 1.5s staleTime so a brief network hiccup does not
 * blank the page. Returns the raw TanStack Query result so callers can
 * render loading / error UI without prop drilling.
 */
export function useConcurrencySnapshot(): UseQueryResult<ConcurrencySnapshot> {
  return useQuery({
    queryKey: ['concurrency', 'snapshot'],
    queryFn: getConcurrencySnapshot,
    refetchInterval: 3000,
    staleTime: 1500,
    refetchOnWindowFocus: false,
    retry: 1,
  });
}