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
import { useQuery, type UseQueryResult } from '@tanstack/react-query';

import { getConcurrencyHistory, type ConcurrencyHistoryResponse } from '../api';
import type { HistoryRange } from '../lib/concurrency-transform';

/**
 * Loads recorded load history for the selected window. Disabled while the
 * caller streams the live buffer (range === null). staleTime 30s keeps
 * re-selecting a preset cheap without going stale during a long look.
 */
export function useConcurrencyHistory(
  range: HistoryRange | null,
): UseQueryResult<ConcurrencyHistoryResponse> {
  return useQuery({
    queryKey: ['concurrency', 'history', range?.start ?? 0, range?.end ?? 0],
    queryFn: async () => {
      if (!range) {
        // enabled:false guarantees this never runs; the guard satisfies TS.
        throw new Error('history range not selected');
      }
      return getConcurrencyHistory(range.start, range.end);
    },
    enabled: range !== null,
    staleTime: 30_000,
    retry: 1,
  });
}
