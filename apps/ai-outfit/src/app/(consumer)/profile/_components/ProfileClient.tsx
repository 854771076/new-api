'use client';

import { useCallback, useState } from 'react';
import { RefreshCw, UserRound, Wallet, History } from 'lucide-react';
import { Payments } from './Payments';
import { formatQuota, logsSchema, userinfoSchema } from './profile-data';
import type { SupabaseUserLite } from './profile-data';
import { useGatewayResource } from './use-gateway-resource';

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
  const history = useGatewayResource(`/api/gateway/logs?page=${page}&page_size=20`, logsSchema, logsRevision);
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
          刷新额度与日志
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
              {info?.group ? (
                <div className="flex flex-wrap justify-between gap-2">
                  <dt className="text-base-content/70">用户组</dt><dd className="break-all">{info.group}</dd>
                </div>
              ) : null}
            </dl>
          </div>
        </section>

        <section className="card card-border min-w-0 bg-base-100 shadow-sm" aria-busy={account.loading}>
          <div className="card-body min-w-0 gap-5 p-4 sm:p-6">
            <h2 className="card-title text-xl"><Wallet size={20} className="text-primary" aria-hidden="true" />App 额度概览</h2>
            {account.loading ? (
              <div role="status" className="space-y-3">
                <p className="text-base-content/70">正在读取 App 额度…</p>
                <div className="skeleton h-10 w-48" /><div className="skeleton h-6 w-full" />
              </div>
            ) : account.error ? (
              <div role="alert" className="alert alert-error alert-soft">
                <span>{account.error}</span>
                <button type="button" className="btn btn-outline" onClick={() => setInfoRevision((value) => value + 1)}>重试额度</button>
              </div>
            ) : (
              <>
                <div className="stats stats-vertical w-full min-w-0 bg-base-200 text-base-content tabular-nums">
                  <div className="stat min-w-0 gap-2 p-5">
                    <p className="stat-title text-sm text-base-content/70">App 剩余额度</p>
                    <p className="stat-value whitespace-normal break-all text-3xl font-semibold text-primary">{formatQuota(appQuota)}</p>
                    <p className="stat-desc whitespace-normal text-sm text-base-content/70">App 已消费：{formatQuota(appUsedQuota)}</p>
                  </div>
                </div>
                {usedPercentage !== null ? (
                  <div className="space-y-2">
                    <progress className="progress progress-primary w-full" aria-label="App 已消费额度占比" value={usedPercentage} max={100} />
                    <p className="text-sm text-base-content/70">App 已消费占比 {usedPercentage.toFixed(0)}%</p>
                  </div>
                ) : null}
                {appQuota === undefined ? (
                  <div role="alert" className="alert alert-warning alert-soft"><span>网关未返回 App 额度，无法确认可用余额；不会使用平台余额替代。请检查网关版本与 api:read 授权。</span></div>
                ) : null}
                {appQuota === 0 ? (
                  <div className="alert alert-warning alert-soft"><span>App 额度池初始为 0，请先在线充值、从平台余额划转或购买额度商品。平台余额不会自动用于换装消费。</span></div>
                ) : null}
                <div className="divider my-0" />
                <p className="break-all tabular-nums">平台余额（仅可划转）：{formatQuota(info?.quota)}</p>
                <p className="text-sm text-base-content/70">平台余额不是 App 余额；balance 充值是平台 → App 的额度划转，不是赠送。</p>
              </>
            )}
          </div>
        </section>
      </div>

      <Payments appQuota={appQuota} platformQuota={info?.quota} onRefresh={refresh} />

      <section className="card card-border min-w-0 bg-base-100 shadow-sm" aria-busy={history.loading}>
        <div className="card-body min-w-0 gap-5 p-4 sm:p-6">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="card-title text-xl"><History size={20} className="text-primary" aria-hidden="true" />消费日志</h2>
            {logs ? <span className="badge badge-outline ml-auto">共 {logs.total.toLocaleString('zh-CN')} 条</span> : null}
          </div>
          {history.loading ? (
            <div role="status" className="space-y-3">
              <p className="text-base-content/70">正在加载第 {page} 页日志…</p><div className="skeleton h-32 w-full" />
            </div>
          ) : history.error ? (
            <div role="alert" className="alert alert-error alert-soft">
              <span>{history.error}</span>
              <button type="button" className="btn btn-outline" onClick={() => setLogsRevision((value) => value + 1)}>重试日志</button>
            </div>
          ) : logs && logs.data.length > 0 ? (
            <div className="min-w-0 overflow-x-auto" role="region" aria-label="消费日志表格" tabIndex={0}>
              <table className="table table-zebra">
                <caption className="sr-only">第 {page} 页消费日志，每页 20 条，花费使用整数 quota</caption>
                <thead><tr><th scope="col">时间</th><th scope="col">模型</th><th scope="col">渠道</th><th scope="col" className="text-right">Tokens（入 / 出）</th><th scope="col" className="text-right">花费（quota）</th></tr></thead>
                <tbody>
                  {logs.data.map((entry, index) => (
                    <tr key={String(entry.id ?? entry.request_id ?? index)}>
                      <td className="whitespace-nowrap text-sm">{formatDate(entry.created_at)}</td>
                      <td className="max-w-64 wrap-anywhere">{entry.model || entry.model_name || '—'}</td>
                      <td className="max-w-48 wrap-anywhere text-sm">{entry.channel_name || '—'}</td>
                      <td className="text-right font-mono text-sm">
                        {entry.prompt_tokens === undefined && entry.completion_tokens === undefined ? '—' : `${entry.prompt_tokens ?? 0} / ${entry.completion_tokens ?? 0}`}
                      </td>
                      <td className="whitespace-nowrap text-right font-mono text-sm">{formatQuota(entry.quota)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="space-y-3">
              <p className="text-base-content/70">{page === 1 ? '暂无消费记录，换装请求的消费记录会显示在这里。' : '本页暂无消费记录，可返回上一页。'}</p>
              <button type="button" className="btn btn-ghost" onClick={() => setLogsRevision((value) => value + 1)}>刷新日志</button>
            </div>
          )}
          <nav aria-label="消费日志分页" className="flex flex-wrap items-center justify-between gap-3 border-t border-base-300 pt-5">
            <button type="button" className="btn btn-outline" disabled={history.loading || page <= 1} onClick={() => setPage((value) => value - 1)}>上一页</button>
            <p className="text-sm tabular-nums" aria-live="polite">第 {page} 页 · 每页 20 条</p>
            <button type="button" className="btn btn-outline" disabled={history.loading || !logs || page * 20 >= logs.total} onClick={() => setPage((value) => value + 1)}>下一页</button>
          </nav>
        </div>
      </section>

      <details className="collapse border border-base-300 bg-base-100">
        <summary className="collapse-title font-medium">额度与付款规则</summary>
        <div className="collapse-content space-y-2 text-sm text-base-content/70">
          <p>new-api 使用整数 quota 记账。本页不把 quota 硬换算成美元，也不将展示单位当作支付币种。</p>
          <p>App 请求预扣、结算与退款使用当前 App 额度池；App 套餐可能提供独立额度，本页没有订阅状态接口，不能据余额推断已订阅。</p>
          <p>在线支付须由网关收到支付确认后生效；pending、支付页返回或关闭窗口都不是已付款证明。刷新额度仅展示网关最新数值，不更改订单状态。</p>
        </div>
      </details>
    </div>
  );
}
