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
import { useConcurrencySnapshot } from './hooks/use-concurrency-snapshot';
import { useConcurrencyWindow } from './hooks/use-concurrency-window';
import { BucketsCard } from './components/buckets-card';
import { KpiCards } from './components/kpi-cards';
import { QueueSettingsCard } from './components/queue-settings-card';
import { RoutingHealthCard } from './components/routing-health-card';
import { TrendCard } from './components/trend-card';
import { VerdictBand } from './components/verdict-band';
import { WindowHeader } from './components/window-header';

/**
 * Admin /concurrency page (contract Y-3 / Y-4 / Y-5), laid out as the
 * one-screen ops console: a height-constrained root (h-full min-h-0, the
 * page itself never scrolls), then header → verdict band → KPI row → one
 * elastic region that is the page's ONLY internal scroll area below `lg`
 * and a fixed 2×2 card grid at `lg` and above (each card body scrolls on
 * its own). Polls /api/concurrency/snapshot every 3s and renders the four
 * KPI tiles, an in-flight trend, the per-scope table (key, user, AND
 * channel rows — OBS-02 landed with the backend's channel-cap backfill),
 * and the live EWMA channel-load card.
 *
 * The trend's time-window control switches between the live rolling buffer
 * and recorded history (any span inside the 7-day retention window) fetched
 * from GET /api/concurrency/history; both feed the same TrendChart. The
 * window selector lives in the page header so KPI, verdict, trend and
 * buckets visibly answer to the SAME window.
 */
export function ConcurrencyAdminPage() {
  const query = useConcurrencySnapshot();
  const snapshot = query.data;
  const {
    scopes,
    windowMode,
    selectWindow,
    customRange,
    onCustomRange,
    isLive,
    historyLoading,
    displayPoints,
    emptyMessage,
    rejectedInWindow,
    verdict,
    tableScopes,
    overCapBuckets,
  } = useConcurrencyWindow(snapshot);

  return (
    <div className='flex h-full min-h-0 flex-col gap-3 p-4'>
      <WindowHeader
        degraded={snapshot?.degraded}
        windowMode={windowMode}
        onSelectWindow={selectWindow}
        customRange={customRange}
        onCustomRange={onCustomRange}
        isLive={isLive}
      />

      <VerdictBand verdict={verdict} />

      {/* The page's one elastic zone: below lg it is the only scrolling
          area (KPI row + cards stack inside it); at lg+ it locks into a
          fixed 2×2 grid whose card bodies scroll individually. */}
      <div className='flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto lg:overflow-hidden'>
        <KpiCards
          scopes={scopes}
          loading={query.isLoading}
          error={query.isError}
          windowPoints={isLive ? null : displayPoints}
          windowLoading={!isLive && historyLoading}
        />

        <div className='grid grid-cols-1 gap-3 lg:min-h-0 lg:flex-1 lg:grid-cols-2 lg:grid-rows-2'>
          <QueueSettingsCard />

          <TrendCard
            isLive={isLive}
            queued={snapshot?.queued}
            rejectedInWindow={rejectedInWindow}
            points={displayPoints}
            emptyMessage={emptyMessage}
          />

          <BucketsCard
            isLive={isLive}
            scopes={tableScopes}
            overCap={overCapBuckets}
            loading={historyLoading}
          />

          {/* Channel load (EWMA routing) is live-only state: it polls its
              own endpoint every 3s and stays mounted regardless of the
              trend window selector above. */}
          <RoutingHealthCard />
        </div>
      </div>
    </div>
  );
}
