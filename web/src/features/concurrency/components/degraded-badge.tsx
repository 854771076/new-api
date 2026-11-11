/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or any later version.
*/

import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { formatTimestampToDate } from '@/lib/format'

import type { ConcurrencyDegradedStatus } from '../lib/concurrency-transform'

/**
 * D-14/D-16 observation badge for the concurrency snapshot page header.
 * Three states (state switches arrive with the 3s snapshot poll; static
 * content, no animation):
 *   - active (Redis outage fail-open window) → warning badge whose title
 *     explains the local-fallback counting;
 *   - recovered (lastDegradedAt > 0, not active) → badge gone, the
 *     "Last degraded at" timestamp row stays for post-incident triage;
 *   - never degraded (lastDegradedAt = 0, or the degraded envelope is
 *     missing on an old backend) → renders nothing (RED-04 boundary:
 *     MemoryLimiter is never degraded, so no badge without Redis).
 */
export function DegradedBadge(props: { degraded?: ConcurrencyDegradedStatus }) {
  const { t } = useTranslation()
  const degraded = props.degraded
  if (!degraded) {
    return null
  }
  if (degraded.active) {
    return (
      <Badge
        variant='warning'
        className='ml-2 align-middle'
        title={t(
          'Redis concurrency backend unavailable. Requests are being counted on local fallback buckets.'
        )}
      >
        {t('Redis degraded')}
      </Badge>
    )
  }
  if (!(degraded.lastDegradedAt > 0)) {
    return null
  }
  return (
    <span className='text-muted-foreground ml-2 align-middle text-xs font-normal'>
      {t('Last degraded at')}{' '}
      {formatTimestampToDate(degraded.lastDegradedAt, 'milliseconds')}
    </span>
  )
}
