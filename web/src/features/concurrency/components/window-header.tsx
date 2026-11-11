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

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'

import type { CustomWindowRange } from '../hooks/use-concurrency-window'
import type {
  ConcurrencyDegradedStatus,
  HistoryWindowMode,
} from '../lib/concurrency-transform'
import { DegradedBadge } from './degraded-badge'

// i18n keys for the window selector; SelectValue needs the label as
// children (Base UI falls back to the raw value otherwise).
const WINDOW_LABEL_KEYS: Record<HistoryWindowMode, string> = {
  live: 'Live',
  '1h': 'Last hour',
  '6h': 'Last 6 hours',
  '24h': 'Last 24 hours',
  '3d': 'Last 3 days',
  '7d': 'Last 7 days',
  custom: 'Custom range',
}

interface WindowHeaderProps {
  degraded?: ConcurrencyDegradedStatus
  windowMode: HistoryWindowMode
  onSelectWindow: (mode: HistoryWindowMode) => void
  customRange: CustomWindowRange
  onCustomRange: (next: CustomWindowRange) => void
  isLive: boolean
}

/**
 * Page header: title with the degradation badge plus the time-window
 * control (live / presets / custom range). The selector lives in the page
 * header so KPI, verdict, trend and buckets visibly answer to the SAME
 * window.
 */
export function WindowHeader(props: WindowHeaderProps) {
  const { t } = useTranslation()
  return (
    <header className='flex shrink-0 flex-wrap items-start justify-between gap-x-4 gap-y-2'>
      <div className='min-w-0'>
        <h1 className='text-2xl font-semibold tracking-tight'>
          {t('Concurrency')}
          <DegradedBadge degraded={props.degraded} />
        </h1>
        <p className='text-muted-foreground text-sm'>
          {t(
            'In-flight request counts per key, per user, and per channel. Refreshes every 3 seconds.'
          )}
        </p>
      </div>
      <div className='flex flex-wrap items-center gap-2'>
        <Select
          value={props.windowMode}
          onValueChange={(v) => props.onSelectWindow(v as HistoryWindowMode)}
        >
          <SelectTrigger className='h-8 w-[140px]'>
            <SelectValue>{t(WINDOW_LABEL_KEYS[props.windowMode])}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value='live'>{t('Live')}</SelectItem>
            <SelectItem value='1h'>{t('Last hour')}</SelectItem>
            <SelectItem value='6h'>{t('Last 6 hours')}</SelectItem>
            <SelectItem value='24h'>{t('Last 24 hours')}</SelectItem>
            <SelectItem value='3d'>{t('Last 3 days')}</SelectItem>
            <SelectItem value='7d'>{t('Last 7 days')}</SelectItem>
            <SelectItem value='custom'>{t('Custom range')}</SelectItem>
          </SelectContent>
        </Select>
        {props.windowMode === 'custom' && (
          <CompactDateTimeRangePicker
            start={props.customRange.start}
            end={props.customRange.end}
            onChange={props.onCustomRange}
          />
        )}
        {/* Honest live indicator: only the live window auto-refreshes; a
            historical window says nothing about "now". */}
        {props.isLive && (
          <span className='inline-flex items-center gap-1.5 text-xs font-medium'>
            <span
              aria-hidden='true'
              className='live-dot bg-success text-success size-1.5 shrink-0 rounded-full'
            />
            {t('Auto-refreshing')}
          </span>
        )}
        <span className='text-muted-foreground text-xs'>
          {t('KPI, trend and buckets follow this window')}
        </span>
      </div>
    </header>
  )
}
