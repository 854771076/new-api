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
import { CircleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  dotColorMap,
  textColorMap,
  type StatusVariant,
} from '@/components/status-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatUseTime } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  formatTimingDuration,
  getFirstResponseTimeColor,
  getResponseTimeColor,
} from '../lib/format'
import {
  TIMING_SEGMENT_KEYS,
  TIMING_SEGMENT_LABELS,
  unattributedTimingMs,
} from '../lib/timing'
import type { LogOtherData, RequestTimingInfo } from '../types'

/**
 * Softened fills for the full-height timing bar. The bar sits directly beside
 * dense numeric text, so the saturated `dotColorMap` tones (tuned for small
 * dots and badges) read as too high-contrast at that size; a translucent fill
 * keeps the status legible while matching the page's muted palette.
 */
const barColorMap: Record<StatusVariant, string> = {
  ...dotColorMap,
  success: 'bg-success/90',
  warning: 'bg-warning/80',
  danger: 'bg-destructive/80',
  neutral: 'bg-neutral/80',
}

interface TimingMetricsCellProps {
  useTimeSec: number
  completionTokens: number
  frtMs?: number
  isStream: boolean
  className?: string
  /**
   * `bar` (default) draws a full-height color segment beside the labels,
   * matching the dense desktop table. `dot` swaps that segment for small
   * status dots inline with each label, matching the lighter-weight status
   * indicator used elsewhere on the mobile card.
   */
  indicator?: 'bar' | 'dot'
  compact?: boolean
  /**
   * Admin-only latency breakdown written by the backend into
   * `other.admin_info.timing`. Without it the cell renders exactly as
   * before: First token + Duration and the two-tone bar.
   */
  timing?: RequestTimingInfo
}

/** One measured latency segment, in milliseconds. */
interface TimingSegment {
  key: string
  label: string
  ms: number
}

/**
 * The measured segments of one request, in the order an operator reads them:
 * gateway queue wait, channel selection, upstream header wait, upstream
 * response body, upstream first byte, upstream stream, settlement. Only
 * segments that were actually measured (greater than zero) are listed.
 */
function collectTimingSegments(
  timing: RequestTimingInfo | undefined,
  t: (key: string) => string
): TimingSegment[] {
  if (!timing) return []
  return TIMING_SEGMENT_KEYS.map((key) => ({
    key,
    label: t(TIMING_SEGMENT_LABELS[key]),
    ms: timing[key] ?? 0,
  })).filter((segment) => segment.ms > 0)
}

/**
 * The color bar groups the measured segments into three tiers — the gateway's
 * own queue wait, everything the upstream cost, and the rest — so the bar
 * shows which side of the gateway the time went to. Widths follow the share
 * of each tier.
 *
 * The tiers alone would always fill the whole bar, which hides how much of
 * the request no segment accounts for. The unattributed remainder is therefore
 * drawn as its own muted tier, so a request where only 0.05% of the time is
 * attributed no longer looks fully explained.
 */
function collectTimingBarSegments(
  timing: RequestTimingInfo | undefined,
  totalSeconds: number
): Array<{ key: string; className: string; ms: number }> {
  if (!timing) return []
  const tiers = [
    {
      key: 'queue',
      className: barColorMap.warning,
      ms: timing.queue_wait_ms ?? 0,
    },
    {
      key: 'upstream',
      className: barColorMap.success,
      ms:
        (timing.upstream_header_ms ?? 0) +
        (timing.upstream_body_ms ?? 0) +
        (timing.upstream_first_byte_ms ?? 0) +
        (timing.upstream_stream_ms ?? 0),
    },
    {
      key: 'other',
      className: barColorMap.neutral,
      ms: (timing.channel_select_ms ?? 0) + (timing.settle_ms ?? 0),
    },
    {
      key: 'unattributed',
      className: 'bg-muted',
      ms: unattributedTimingMs(timing, totalSeconds),
    },
  ]
  return tiers.filter((tier) => tier.ms > 0)
}

export function TimingMetricsCell(props: TimingMetricsCellProps) {
  const { t } = useTranslation()
  const indicator = props.indicator ?? 'bar'
  const showFirstToken = props.isStream
  const firstTokenSeconds =
    props.frtMs != null && props.frtMs > 0 ? props.frtMs / 1000 : null
  const firstTokenVariant: StatusVariant =
    firstTokenSeconds == null
      ? 'neutral'
      : getFirstResponseTimeColor(firstTokenSeconds)
  const totalTimeVariant = getResponseTimeColor(
    props.useTimeSec,
    props.completionTokens
  )
  const firstTokenLabel =
    firstTokenSeconds == null ? t('N/A') : formatUseTime(firstTokenSeconds)
  const totalTimeLabel = formatUseTime(props.useTimeSec)
  const timingSegments = collectTimingSegments(props.timing, t)
  const timingBarSegments = collectTimingBarSegments(
    props.timing,
    props.useTimeSec
  )
  const millisecondUnit = t('ms')

  const labels = (
    <div
      className={cn(
        'flex min-h-8 min-w-0 flex-col justify-center gap-0.5 text-xs leading-tight',
        props.compact &&
          'min-h-0 flex-row flex-wrap items-center gap-x-2.5 gap-y-1'
      )}
    >
      {showFirstToken && (
        <div className='flex items-baseline gap-1.5'>
          {indicator === 'dot' && (
            <span
              aria-hidden
              className={cn(
                'size-1.5 shrink-0 rounded-full',
                dotColorMap[firstTokenVariant]
              )}
            />
          )}
          <span className='text-muted-foreground shrink-0'>
            {t('First token')}
          </span>
          <span className={cn('tabular-nums', textColorMap[firstTokenVariant])}>
            {firstTokenLabel}
          </span>
        </div>
      )}
      <div className='flex items-baseline gap-1.5'>
        {indicator === 'dot' && (
          <span
            aria-hidden
            className={cn(
              'size-1.5 shrink-0 rounded-full',
              dotColorMap[totalTimeVariant]
            )}
          />
        )}
        <span className='text-muted-foreground shrink-0'>{t('Duration')}</span>
        <span className={cn('tabular-nums', textColorMap[totalTimeVariant])}>
          {totalTimeLabel}
        </span>
      </div>
      {timingSegments.length > 0 && (
        <div
          className={cn(
            'flex min-w-0 flex-col gap-0.5',
            props.compact && 'flex-row flex-wrap items-center gap-x-2.5 gap-y-1'
          )}
        >
          {timingSegments.map((segment) => (
            <div key={segment.key} className='flex items-baseline gap-1.5'>
              <span className='text-muted-foreground shrink-0'>
                {segment.label}
              </span>
              <span className='text-muted-foreground tabular-nums'>
                {formatTimingDuration(segment.ms, millisecondUnit)}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )

  if (indicator === 'dot') {
    return (
      <div className={cn('flex items-stretch', props.className)}>{labels}</div>
    )
  }

  const bar =
    timingBarSegments.length > 0 ? (
      <span
        aria-hidden
        className='flex w-1 shrink-0 flex-col overflow-hidden rounded-full'
      >
        {timingBarSegments.map((tier) => (
          <span
            key={tier.key}
            className={cn('min-h-px', tier.className)}
            style={{ flexGrow: tier.ms, flexBasis: 0 }}
          />
        ))}
      </span>
    ) : (
      <span
        aria-hidden
        className={cn(
          'flex w-1 shrink-0 flex-col overflow-hidden rounded-full',
          !showFirstToken && barColorMap[totalTimeVariant]
        )}
      >
        {showFirstToken && (
          <>
            <span className={cn('flex-1', barColorMap[firstTokenVariant])} />
            <span className={cn('flex-1', barColorMap[totalTimeVariant])} />
          </>
        )}
      </span>
    )

  return (
    <div className={cn('flex items-stretch gap-2', props.className)}>
      {bar}
      {labels}
    </div>
  )
}

interface StreamTpsCellProps {
  isStream: boolean
  compact?: boolean
  /** Task logs are asynchronous jobs; stream vs non-stream does not apply. */
  isTask?: boolean
  /** The task request returned its result inline, so it is a synchronous call. */
  isSyncTask?: boolean
  tokensPerSecond?: number | null
  streamStatus?: LogOtherData['stream_status']
  className?: string
}

export function StreamTpsCell(props: StreamTpsCellProps) {
  const { t } = useTranslation()
  const showStreamError =
    props.isStream && props.streamStatus && props.streamStatus.status !== 'ok'
  const tpsLabel =
    props.tokensPerSecond != null
      ? `${Math.round(props.tokensPerSecond)} t/s`
      : '—'
  let streamLabel = props.isStream ? t('Stream') : t('Non-stream')
  if (props.isTask) {
    streamLabel = props.isSyncTask ? t('Sync') : t('Async')
  }

  return (
    <div
      className={cn(
        'flex shrink-0 flex-col items-start justify-center gap-0.5 text-xs leading-tight',
        props.compact && 'flex-row flex-wrap items-center gap-1.5',
        props.className
      )}
    >
      <span
        className={cn(
          'inline-flex items-center gap-1 font-medium',
          props.isStream ? 'text-info' : 'text-muted-foreground'
        )}
      >
        {streamLabel}
        {showStreamError && (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger
                render={<CircleAlert className='text-destructive size-3' />}
              />
              <TooltipContent>
                <div className='space-y-0.5 text-xs'>
                  <p>
                    {t('Stream Status')}: {t('Error')}
                  </p>
                  <p>{props.streamStatus?.end_reason || 'unknown'}</p>
                  {(props.streamStatus?.error_count ?? 0) > 0 && (
                    <p>
                      {t('Soft Errors')}: {props.streamStatus?.error_count}
                    </p>
                  )}
                </div>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )}
      </span>
      {(!props.compact ||
        (props.isStream && props.tokensPerSecond != null)) && (
        <span className='text-muted-foreground/60 px-0.5 tabular-nums'>
          {tpsLabel}
        </span>
      )}
    </div>
  )
}
