/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import { useQuery, type UseQueryResult } from '@tanstack/react-query'

import { getChannelRoutingHealth } from '../api'
import type { ChannelRoutingHealthRow } from '../lib/concurrency-transform'

/**
 * Polls GET /api/concurrency/routing-health with the same 3s cadence,
 * staleTime, retry and focus policy as useConcurrencySnapshot: the channel
 * load card is live-only state and deliberately does not participate in the
 * trend chart's window selector.
 */
export function useChannelRoutingHealth(): UseQueryResult<
  ChannelRoutingHealthRow[]
> {
  return useQuery({
    queryKey: ['concurrency', 'routing-health'],
    queryFn: getChannelRoutingHealth,
    refetchInterval: 3000,
    staleTime: 1500,
    refetchOnWindowFocus: false,
    retry: 1,
  })
}
