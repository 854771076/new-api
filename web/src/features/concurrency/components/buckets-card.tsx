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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'

import type { ConcurrencyScopeSnapshot } from '../lib/concurrency-transform'
import { ScopeTable } from './scope-table'

interface BucketsCardProps {
  isLive: boolean
  /** Live scopes or the window's membership merged with live values. */
  scopes: ConcurrencyScopeSnapshot[]
  /** Buckets currently over their cap (drives the outline badge). */
  overCap: number
  /** History-fetch state; only surfaces while a window is selected. */
  loading: boolean
}

/**
 * Card wrapping the per-scope bucket table with the window-dependent title
 * and the over-cap count badge. Pure presentation; window state comes from
 * the page (useConcurrencyWindow).
 */
export function BucketsCard(props: BucketsCardProps) {
  const { t } = useTranslation()
  return (
    <Card className='h-full min-h-0'>
      <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2 space-y-0'>
        <div className='flex flex-wrap items-center gap-2'>
          <CardTitle>
            {props.isLive
              ? t('Active concurrency buckets')
              : t('Buckets in the window')}
          </CardTitle>
          <Badge
            variant='outline'
            className={cn(
              'tabular-nums',
              props.overCap > 0
                ? 'border-destructive/40 bg-destructive/10 text-foreground'
                : 'border-border bg-muted/40 text-muted-foreground'
            )}
          >
            {t('{{n}} over cap', { n: props.overCap })}
          </Badge>
        </div>
        <span className='text-muted-foreground text-xs'>
          {t('Sorted by utilisation, hottest first')}
        </span>
      </CardHeader>
      <CardContent className='min-h-0 flex-1 overflow-hidden'>
        <ScopeTable
          scopes={props.scopes}
          windowed={!props.isLive}
          loading={!props.isLive && props.loading}
        />
      </CardContent>
    </Card>
  )
}
