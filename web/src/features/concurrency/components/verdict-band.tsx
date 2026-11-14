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
import {
  AlertTriangle,
  CircleCheck,
  ShieldAlert,
  type LucideIcon,
} from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

import type {
  ConcurrencyVerdict,
  ConcurrencyVerdictTone,
} from '../lib/concurrency-transform'

// Semantic tints only (theme.css tokens): the band reads as one coloured
// surface — 7px rail + /10 fill — and every number inside stays tabular.
const TONE_TEXT: Record<ConcurrencyVerdictTone, string> = {
  destructive: 'text-destructive',
  warning: 'text-warning',
  success: 'text-success',
}

const TONE_EDGE: Record<ConcurrencyVerdictTone, string> = {
  destructive: 'border-destructive/40',
  warning: 'border-warning/40',
  success: 'border-success/40',
}

const TONE_FILL: Record<ConcurrencyVerdictTone, string> = {
  destructive: 'bg-destructive/10',
  warning: 'bg-warning/10',
  success: 'bg-success/10',
}

const TONE_RAIL: Record<ConcurrencyVerdictTone, string> = {
  destructive: 'border-l-destructive',
  warning: 'border-l-warning',
  success: 'border-l-success',
}

const VERDICT_ICON: Record<ConcurrencyVerdictTone, LucideIcon> = {
  destructive: ShieldAlert,
  warning: AlertTriangle,
  success: CircleCheck,
}

// Chips sit on top of `variant='outline'`; only the foreground/border/bg
// differ per tone, so a tone swap repaints the chip without resizing it.
// Chip TEXT stays `text-foreground` on the tone tint: at 12px the small
// semantic tokens cannot reach WCAG AA 4.5:1 on their own /10 tint in the
// light theme (measured 2.65–4.06:1); foreground-on-tint is ≥16:1 in both
// themes. Tone rides on the tinted surface, the rail and the icon.
const CHIP_TONE = {
  destructive: 'border-destructive/40 bg-destructive/10 text-foreground',
  warning: 'border-warning/40 bg-warning/10 text-foreground',
  success: 'border-success/40 bg-success/10 text-foreground',
  muted: 'border-border bg-muted/40 text-muted-foreground',
} as const

interface VerdictBandProps {
  verdict: ConcurrencyVerdict
}

/**
 * The one-screen page's single bold element: a conclusion band that answers
 * "is the gateway healthy?" before the admin reads a KPI. Tone and headline
 * come from the pure `deriveVerdict` ladder; the four chips keep a fixed
 * slot (Over-limit / Rejected / Queued / backend state) so a data refresh
 * repaints colour and digits only — no layout shift. Deliberately NOT
 * `role="status"`: that would be an aria-live region the 3s poll replays.
 */
export function VerdictBand(props: VerdictBandProps) {
  const { t } = useTranslation()
  const headingId = useId()
  const verdict = props.verdict
  const Icon = VERDICT_ICON[verdict.tone]

  return (
    <section
      aria-labelledby={headingId}
      className={cn(
        'flex min-h-16 shrink-0 flex-wrap items-center gap-3 rounded-lg border border-l-[7px] px-4 py-3',
        TONE_FILL[verdict.tone],
        TONE_EDGE[verdict.tone],
        TONE_RAIL[verdict.tone]
      )}
    >
      <Icon
        aria-hidden='true'
        className={cn('size-5 shrink-0', TONE_TEXT[verdict.tone])}
      />
      {/* Headline stays foreground-coloured for AA; the band's tone is
          carried by the 7px rail, the tinted canvas, the icon and chips. */}
      <h2
        id={headingId}
        className={cn('min-w-0 truncate text-sm font-semibold')}
      >
        {t(verdict.titleKey, { n: verdict.overLimit })}
      </h2>
      <div className='ml-auto flex shrink-0 flex-wrap items-center justify-end gap-2'>
        <Badge
          className={cn(
            'tabular-nums',
            CHIP_TONE[verdict.overLimit > 0 ? 'destructive' : 'muted']
          )}
        >
          {t('Over-limit {{n}}', { n: verdict.overLimit })}
        </Badge>
        <Badge
          className={cn(
            'tabular-nums',
            CHIP_TONE[verdict.rejected > 0 ? 'warning' : 'muted']
          )}
        >
          {t('Rejected {{n}}', { n: verdict.rejected })}
        </Badge>
        <Badge
          className={cn(
            'tabular-nums',
            CHIP_TONE[verdict.queued > 0 ? 'warning' : 'muted']
          )}
        >
          {t('Queued {{n}}', { n: verdict.queued })}
        </Badge>
        <Badge
          className={cn(
            verdict.degraded ? CHIP_TONE.warning : CHIP_TONE.success
          )}
        >
          {verdict.degraded ? t('Backend degraded') : t('Backend healthy')}
        </Badge>
      </div>
    </section>
  )
}
