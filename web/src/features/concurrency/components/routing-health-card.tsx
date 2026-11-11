/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

import { useChannelRoutingHealth } from '../hooks/use-channel-routing-health'
import { formatRoutingHealthRow } from '../lib/concurrency-transform'

const COLUMN_COUNT = 7

// Filled multiplier chips (Variant E): boosted >1 green, inactive =1 muted,
// penalised <1 amber — the displayed number itself is unchanged. Chip text
// stays foreground-on-tint: the small semantic tokens miss WCAG AA 4.5:1
// on their own /10 tint at 12px in the light theme; the tint carries tone.
const MULTIPLIER_CHIP: Record<
  'boosted' | 'inactive' | 'penalised',
  string
> = {
  boosted: 'border-success/40 bg-success/10 text-foreground',
  inactive: 'border-border bg-muted text-muted-foreground',
  penalised: 'border-warning/40 bg-warning/10 text-foreground',
}

/**
 * Channel load card (EWMA routing): one row per (channel, model) pair from
 * GET /api/concurrency/routing-health — sample count, success/latency EWMA,
 * the effective weight multiplier, and the last sample time. The data set is
 * tiny (channel × model), so rows render in full with no paging or sorting;
 * the endpoint already returns them sorted by ChannelID then Model. Live
 * state only: this card keeps its own 3s poll and ignores the trend chart's
 * window selector, and says so via the "Live · 3s poll" badge.
 */
export function RoutingHealthCard() {
  const { t } = useTranslation()
  const query = useChannelRoutingHealth()
  const rows = query.data ?? []

  // A failed fetch must not masquerade as "no samples"; show one message
  // row (error > empty) instead of a bare table body.
  let emptyMessage: string | null = null
  if (query.isError) {
    emptyMessage = t('Failed to load')
  } else if (rows.length === 0) {
    emptyMessage = t('No routing health samples yet.')
  }

  return (
    <Card className='h-full min-h-0'>
      <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2 space-y-0'>
        <div className='flex flex-wrap items-center gap-2'>
          <CardTitle>{t('Channel load (EWMA routing)')}</CardTitle>
          <Badge variant='outline' className='gap-1.5 text-muted-foreground'>
            <span
              aria-hidden='true'
              className='live-dot size-1.5 shrink-0 rounded-full bg-success text-success'
            />
            {t('Live · 3s poll')}
          </Badge>
        </div>
      </CardHeader>
      <CardContent className='flex min-h-0 flex-1 flex-col overflow-hidden'>
        {/* The card body owns the scroll (no fixed max-height): long tables
            clip inside the 2×2 cell instead of pushing the page. */}
        <div className='min-h-0 flex-1 overflow-y-auto'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Channel')}</TableHead>
                <TableHead>{t('Model')}</TableHead>
                <TableHead className='text-right'>{t('Samples')}</TableHead>
                <TableHead className='text-right'>
                  {t('Success rate')}
                </TableHead>
                <TableHead className='text-right'>
                  {t('Latency EWMA (ms)')}
                </TableHead>
                <TableHead className='text-right'>{t('Multiplier')}</TableHead>
                <TableHead>{t('Last sample')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {emptyMessage ? (
                <TableRow>
                  <TableCell
                    colSpan={COLUMN_COUNT}
                    className='text-muted-foreground py-8 text-center'
                  >
                    {emptyMessage}
                  </TableCell>
                </TableRow>
              ) : (
                rows.map((row) => {
                  const display = formatRoutingHealthRow(row)
                  let chip = MULTIPLIER_CHIP.inactive
                  if (row.Multiplier > 1) {
                    chip = MULTIPLIER_CHIP.boosted
                  } else if (row.Multiplier < 1) {
                    chip = MULTIPLIER_CHIP.penalised
                  }
                  return (
                    <TableRow key={`${row.ChannelID}:${row.Model}`}>
                      <TableCell>
                        {t('Channel #{{id}}', { id: row.ChannelID })}
                      </TableCell>
                      <TableCell>
                        <span className='font-mono text-xs'>{row.Model}</span>
                      </TableCell>
                      <TableCell className='text-right tabular-nums'>
                        {row.Samples}
                      </TableCell>
                      <TableCell className='text-right tabular-nums'>
                        {display.successRate}
                      </TableCell>
                      <TableCell className='text-right tabular-nums'>
                        {display.latencyMs}
                      </TableCell>
                      <TableCell className='text-right'>
                        <span
                          className={cn(
                            'inline-flex h-5 min-w-13 items-center justify-center rounded-md border px-1.5 text-xs font-semibold tabular-nums',
                            chip
                          )}
                        >
                          {display.multiplier}
                        </span>
                      </TableCell>
                      <TableCell className='text-muted-foreground tabular-nums'>
                        {display.lastSampleAt}
                      </TableCell>
                    </TableRow>
                  )
                })
              )}
            </TableBody>
          </Table>
        </div>
        <p className='text-muted-foreground mt-2 shrink-0 text-xs'>
          {t(
            'Multiplier: 1.0 = inactive (channel has no slot with concurrency_limit>0, or under 10 samples while warming up); >1 boosted, <1 penalised, range [0.2, 5.0].'
          )}
        </p>
      </CardContent>
    </Card>
  )
}
