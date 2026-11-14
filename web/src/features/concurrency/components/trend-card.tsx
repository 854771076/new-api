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

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import type { ConcurrencyTrendPoint } from '../lib/concurrency-transform'
import { TrendChart } from './trend-chart'

interface TrendCardProps {
  isLive: boolean
  /** Live queue depth; undefined (older backend) hides the chip. */
  queued?: number
  rejectedInWindow: number
  points: ConcurrencyTrendPoint[]
  emptyMessage?: string
}

/**
 * Card wrapping the in-flight TrendChart with the live queue-depth chip
 * and the window's rejection delta. Pure presentation; window state comes
 * from the page (useConcurrencyWindow).
 */
export function TrendCard(props: TrendCardProps) {
  const { t } = useTranslation()
  return (
    <Card className='h-full min-h-0'>
      <CardHeader className='flex flex-row flex-wrap items-center justify-between gap-2 space-y-0'>
        <CardTitle>{t('In-flight trend')}</CardTitle>
        <div className='text-muted-foreground flex flex-wrap items-center gap-4 text-sm tabular-nums'>
          {/* Queue depth is live snapshot state, so it only exists in
              the live window — historical windows chart recorded
              counters. */}
          {props.isLive && props.queued !== undefined && (
            <span>
              {t('Queued')}: {props.queued}
            </span>
          )}
          <span>
            {t('Rejected in window')}: {props.rejectedInWindow}
          </span>
        </div>
      </CardHeader>
      <CardContent className='flex min-h-0 flex-1 flex-col overflow-hidden'>
        <TrendChart points={props.points} emptyMessage={props.emptyMessage} />
      </CardContent>
    </Card>
  )
}
