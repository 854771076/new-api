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
import { useTranslation } from 'react-i18next';
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from 'recharts';

import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';

import type { ConcurrencyTrendPoint } from '../lib/concurrency-transform';

interface TrendChartProps {
  points: ConcurrencyTrendPoint[];
  /** Overrides the <2-points placeholder (e.g. historical "no samples"). */
  emptyMessage?: string;
}

/**
 * In-flight trend rendered from points — either the page's live ring buffer
 * (one sample per 3s poll) or fetched history for a selected window. Fewer
 * than two samples cannot form a line, so the empty case renders a muted
 * placeholder instead of a degenerate chart.
 */
export function TrendChart({ points, emptyMessage }: TrendChartProps) {
  const { t } = useTranslation();

  // One series; the tooltip header reuses the KPI title so the chart and the
  // tile above speak the same language without a new translation key.
  const chartConfig = {
    inFlight: {
      label: t('Total in-flight requests'),
      color: 'var(--chart-1)',
    },
  } satisfies ChartConfig;

  if (points.length < 2) {
    // Same box as the rendered chart so an empty window never resizes the
    // card body when samples arrive.
    return (
      <div className='text-muted-foreground flex h-full min-h-[160px] w-full items-center justify-center text-sm'>
        {emptyMessage ?? t('Collecting samples…')}
      </div>
    );
  }

  const data = points.map((p) => ({ t: p.t, inFlight: p.inFlight }));

  return (
    <ChartContainer
      config={chartConfig}
      className='aspect-auto h-full min-h-[160px] w-full'
    >
      <AreaChart data={data} margin={{ top: 8, right: 8, left: -14, bottom: 0 }}>
        <CartesianGrid vertical={false} strokeDasharray='3 3' />
        <XAxis
          dataKey='t'
          tickLine={false}
          axisLine={false}
          minTickGap={48}
          tickFormatter={(v: number) => {
            const d = new Date(v);
            const pad = (n: number) => String(n).padStart(2, '0');
            return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
          }}
        />
        <YAxis allowDecimals={false} tickLine={false} axisLine={false} width={36} />
        <ChartTooltip content={<ChartTooltipContent />} />
        <Area
          dataKey='inFlight'
          type='monotone'
          stroke='var(--color-inFlight)'
          fill='var(--color-inFlight)'
          fillOpacity={0.15}
          strokeWidth={2}
          // Re-animating on every 3s live poll makes the chart visibly
          // pulse; data updates should be instant, not morphed.
          isAnimationActive={false}
        />
      </AreaChart>
    </ChartContainer>
  );
}
