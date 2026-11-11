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
import { Activity, Gauge, Layers, ShieldAlert } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { StatCard } from '@/features/dashboard/components/ui/stat-card';

import {
  deriveKpi,
  deriveWindowKpi,
  type ConcurrencyScopeSnapshot,
  type ConcurrencyTrendPoint,
} from '../lib/concurrency-transform';

interface KpiCardsProps {
  scopes: ConcurrencyScopeSnapshot[];
  loading?: boolean;
  error?: boolean;
  /**
   * Non-null selects the historical-window tile set derived from these
   * points (peak / mean / rejected / active share); null keeps the live
   * tiles. Without this split the row keeps ticking on live snapshot values
   * underneath a static historical chart — two clocks on one card.
   */
  windowPoints?: ConcurrencyTrendPoint[] | null;
  /** Drives the skeleton while the history query is fetching. */
  windowLoading?: boolean;
}

/**
 * Renders the four KPI tiles the admin sees above the trend chart
 * (contract Y-3). Live mode shows the current scope-derived state; window
 * mode shows aggregates of the selected time window so every element on the
 * page describes the SAME data. Pure presentation; tone mapping is supplied
 * by callers through StatCard, which already supports destructive /
 * warning details.
 */
export function KpiCards({
  scopes,
  loading,
  error,
  windowPoints,
  windowLoading,
}: KpiCardsProps) {
  const { t } = useTranslation();

  if (windowPoints) {
    const wk = deriveWindowKpi(windowPoints);
    return (
      <div className='grid shrink-0 grid-cols-1 gap-3 [&>*]:h-full sm:grid-cols-2 lg:grid-cols-4'>
        <StatCard
          title={t('Peak in-flight')}
          value={wk.peak}
          description={t('Highest in-flight requests in the selected window.')}
          icon={Activity}
          tone='accent-1'
          loading={windowLoading}
          error={false}
        />
        <StatCard
          title={t('Average in-flight')}
          value={wk.avg.toFixed(1)}
          description={t(
            'Average in-flight requests across the selected window.',
          )}
          icon={Layers}
          tone='accent-2'
          loading={windowLoading}
          error={false}
        />
        <StatCard
          title={t('Rejected in window')}
          value={wk.rejected}
          description={t(
            'Concurrency 429 responses observed in the trend window.',
          )}
          icon={ShieldAlert}
          tone='accent-3'
          loading={windowLoading}
          error={false}
        />
        <StatCard
          title={t('Active share')}
          value={`${Math.round(wk.activeShare * 100)}%`}
          description={t(
            'Share of sampled time with at least one request in flight.',
          )}
          icon={Gauge}
          tone='accent-3'
          loading={windowLoading}
          error={false}
        />
      </div>
    );
  }

  const kpi = deriveKpi(scopes);

  return (
    // shrink-0: the tile row keeps its height inside the page's one
    // elastic scroll region, so a refresh can never collapse it; every
    // StatCard gets h-full via [&>*] so the four tiles share one row
    // height, and the description clamps to a single line with the full
    // text preserved in its title (no layout shift on refresh).
    <div className='grid shrink-0 grid-cols-1 gap-3 [&>*]:h-full sm:grid-cols-2 lg:grid-cols-4'>
      <StatCard
        title={t('Total in-flight requests')}
        value={kpi.totalCurrent}
        description={t(
          'Unique in-flight requests across every active user bucket.',
        )}
        icon={Activity}
        tone='accent-1'
        loading={loading}
        error={error}
      />
      <StatCard
        title={t('Configured concurrency caps')}
        value={kpi.totalLimit}
        description={t(
          'Sum of explicit per-key / per-user caps (unlimited scopes excluded).',
        )}
        icon={Layers}
        tone='accent-2'
        loading={loading}
        error={error}
      />
      <StatCard
        title={t('Average utilisation')}
        value={`${Math.round(kpi.avgUtilization * 100)}%`}
        description={t(
          'Mean of capped-bucket utilisation. 0% means no caps are currently in use.',
        )}
        icon={Gauge}
        tone='accent-3'
        loading={loading}
        error={error}
      />
      <StatCard
        title={t('Over-limit scopes')}
        value={kpi.overLimitScopes}
        description={t(
          'Number of per-key / per-user buckets currently exceeding their configured cap.',
        )}
        icon={ShieldAlert}
        tone='accent-3'
        loading={loading}
        error={error}
      />
    </div>
  );
}
