'use client';

import { useCallback, useState } from 'react';
import { RefreshCw, UserRound, Wallet, History } from 'lucide-react';
import { Payments } from './Payments';
import { formatCredits, logsSchema, userinfoSchema } from './profile-data';
import type { SupabaseUserLite } from './profile-data';
import { useGatewayResource } from './use-gateway-resource';

const PAGE_SIZE = 20;

function formatDate(iso?: string): string {
  if (!iso) return '—';
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false });
}

export function ProfileClient(props: { user: SupabaseUserLite }) {
  const [infoRevision, setInfoRevision] = useState(0);
  const [logsRevision, setLogsRevision] = useState(0);
  const [page, setPage] = useState(1);
  const account = useGatewayResource('/api/gateway/userinfo', userinfoSchema, infoRevision);
  const history = useGatewayResource(`/api/gateway/logs?page=${page}&page_size=${PAGE_SIZE}`, logsSchema, logsRevision);
  const refresh = useCallback(() => {
    setInfoRevision((value) => value + 1);
    setLogsRevision((value) => value + 1);
  }, []);
  const info = account.data;
  const logs = history.data;
  const displayName = props.user.user_metadata.name || props.user.user_metadata.full_name
    || info?.display_name || info?.username || '—';
  const appQuota = info?.app_quota;
  const appUsedQuota = info?.app_used_quota;
  let usedPercentage: number | null = null;
  if (appQuota !== undefined && appUsedQuota !== undefined) {
    usedPercentage = appQuota + appUsedQuota > 0 ? (appUsedQuota / (appUsedQuota + appQuota)) * 100 : 0;
  }

  return (
    <div className="min-w-0 space-y-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">个人中心</h1>
        <button type="button" className="btn btn-ghost gap-2" disabled={account.loading || history.loading} onClick={refresh}>
          {account.loading || history.loading
            ? <span className="loading loading-spinner loading-sm" aria-hidden="true" />
            : <RefreshCw size={16} aria-hidden="true" />}
          刷新
        </button>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <section className="card card-border min-w-0 bg-base-100 shadow-sm">
          <div className="card-body gap-5 p-4 sm:p-6">
            <h2 className="card-title text-xl"><UserRound size={20} className="text-primary" aria-hidden="true" />账户信息</h2>
            <dl className="space-y-4 text-sm">
              <div className="flex flex-wrap justify-between gap-2">
                <dt className="text-base-content/70">用户 ID</dt><dd className="break-all font-mono">{props.user.id}</dd>
              </div>
              <div className="flex flex-wrap justify-between gap-2">
                <dt className="text-base-content/70">登录提供方</dt><dd className="wrap-anywhere">{info?.provider || props.user.app_metadata.provider || '—'}</dd>
              </div>
              <div className="flex flex-wrap justify-between gap-2">
                <dt className="text-base-content/70">显示名</dt><dd className="break-all">{displayName}</dd>
              </div>
              <div className="flex flex-wrap justify-between gap-2">
                <dt className="text-base-content/70">邮箱</dt><dd className="break-all">{props.user.email || info?.email || '—'}</dd>
              </div>
            </dl>
          </div>
        </section>

        <section className="card card-border min-w-0 bg-base-100 shadow-sm" aria-busy={account.loading}>
          <div className="card-body min-w-0 gap-5 p-4 sm:p-6">
            <h2 className="card-title text-xl"><Wallet size={20} className="text-primary" aria-hidden="true" />我的积分</h2>
            {account.loading ? (
              <div role="status" className="space-y-3">
                <p className="text-base-content/70">正在读取积分…</p>
                <div className="skeleton h-10 w-48" /><div className="skeleton h-6 w-full" />
              </div>
            ) : account.error ? (
              <div role="alert" className="alert alert-error alert-soft">
                <span>暂时无法读取积分，请稍后重试。</span>
                <button type="button" className="btn btn-outline" onClick={() => setInfoRevision((value) => value + 1)}>重试</button>
              </div>
            ) : (
              <>
                <div className="stats stats-vertical w-full min-w-0 bg-base-200 text-base-content tabular-nums">
                  <div className="stat min-w-0 gap-2 p-5">
                    <p className="stat-title text-sm text-base-content/70">剩余积分</p>
                    <p className="stat-value whitespace-normal break-all text-3xl font-semibold text-primary">{formatCredits(appQuota)}</p>
                    <p className="stat-desc whitespace-normal text-sm text-base-content/70">已用积分：{formatCredits(appUsedQuota)}</p>
                  </div>
                </div>
                {usedPercentage !== null ? (
                  <div className="space-y-2">
                    <progress className="progress progress-primary w-full" aria-label="已使用积分占比" value={usedPercentage} max={100} />
                    <p className="text-sm text-base-content/70">已使用 {usedPercentage.toFixed(0)}%</p>
                  </div>
                ) : null}
                {appQuota === 0 ? (
                  <div className="alert alert-info alert-soft"><span>积分不足，可在下方充值后继续换装。</span></div>
                ) : null}
              </>
            )}
          </div>
        </section>
      </div>

      <Payments appQuota={appQuota} onRefresh={refresh} />

      <section className="card card-border min-w-0 bg-base-100 shadow-sm" aria-busy={history.loading}>
        <div className="card-body min-w-0 gap-5 p-4 sm:p-6">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="card-title text-xl"><History size={20} className="text-primary" aria-hidden="true" />消费记录</h2>
            {logs ? <span className="badge badge-outline ml-auto">共 {logs.total.toLocaleString('zh-CN')} 条</span> : null}
          </div>
          {history.loading ? (
            <div role="status" className="space-y-3">
              <p className="text-base-content/70">正在加载第 {page} 页记录…</p><div className="skeleton h-32 w-full" />
            </div>
          ) : history.error ? (
            <div role="alert" className="alert alert-error alert-soft">
              <span>{history.error}</span>
              <button type="button" className="btn btn-outline" onClick={() => setLogsRevision((value) => value + 1)}>重试</button>
            </div>
          ) : logs && logs.data.length > 0 ? (
            <div className="min-w-0 overflow-x-auto" role="region" aria-label="消费记录表格" tabIndex={0}>
              <table className="table table-zebra">
                <caption className="sr-only">第 {page} 页消费记录，每页 {PAGE_SIZE} 条</caption>
                <thead><tr><th scope="col">时间</th><th scope="col">模型</th><th scope="col">渠道</th><th scope="col" className="text-right">Tokens（入 / 出）</th><th scope="col" className="text-right">消耗积分</th></tr></thead>
                <tbody>
                  {logs.data.map((entry, index) => (
                    <tr key={String(entry.id ?? entry.request_id ?? index)}>
                      <td className="whitespace-nowrap text-sm">{formatDate(entry.created_at)}</td>
                      <td className="max-w-64 wrap-anywhere">{entry.model || entry.model_name || '—'}</td>
                      <td className="max-w-48 wrap-anywhere text-sm">{entry.channel_name || '—'}</td>
                      <td className="text-right font-mono text-sm">
                        {entry.prompt_tokens === undefined && entry.completion_tokens === undefined ? '—' : `${entry.prompt_tokens ?? 0} / ${entry.completion_tokens ?? 0}`}
                      </td>
                      <td className="whitespace-nowrap text-right font-mono text-sm">{formatCredits(entry.quota)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="space-y-3">
              <p className="text-base-content/70">{page === 1 ? '暂无消费记录，换装消耗会显示在这里。' : '本页暂无记录，可返回上一页。'}</p>
              <button type="button" className="btn btn-ghost" onClick={() => setLogsRevision((value) => value + 1)}>刷新</button>
            </div>
          )}
          <nav aria-label="消费记录分页" className="flex flex-wrap items-center justify-between gap-3 border-t border-base-300 pt-5">
            <button type="button" className="btn btn-outline" disabled={history.loading || page <= 1} onClick={() => setPage((value) => value - 1)}>上一页</button>
            <p className="text-sm tabular-nums" aria-live="polite">第 {page} 页 · 每页 {PAGE_SIZE} 条</p>
            <button type="button" className="btn btn-outline" disabled={history.loading || !logs || page * PAGE_SIZE >= logs.total} onClick={() => setPage((value) => value + 1)}>下一页</button>
          </nav>
        </div>
      </section>
    </div>
  );
}
