'use client';

import { useEffect, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { QUOTA_PER_UNIT } from '@/lib/constants';

// 从网关返回的数据类型（与 gateway.ts 对齐）
interface GatewayUserinfo {
  id: string;
  username?: string;
  display_name?: string;
  email?: string;
  provider?: string;
  role?: number;
  status?: number;
  quota?: number;
  used_quota?: number;
  request_count?: number;
  group?: string;
  [key: string]: unknown;
}

interface GatewayLogEntry {
  id?: number | string;
  model?: string;
  model_name?: string;
  prompt_tokens?: number;
  completion_tokens?: number;
  quota?: number;
  use_time?: number;
  channel_name?: string;
  group?: string;
  request_id?: string;
  created_at?: string;
  [key: string]: unknown;
}

interface GatewayLogsResult {
  data: GatewayLogEntry[];
  total: number;
  page: number;
  page_size: number;
}

interface SupabaseUserLite {
  id: string;
  email?: string | null;
  phone?: string | null;
  app_metadata?: { provider?: string; [k: string]: unknown };
  user_metadata?: { name?: string; full_name?: string; [k: string]: unknown };
}

type ProfileClientProps = {
  user: SupabaseUserLite;
  /** 网关控制台地址，用于“去网关查看”降级入口 */
  consoleUrl: string;
};

/** 接口失败时给 UI 的结构化错误；unsupported 表示旧网关没有该路由 */
type LoadError = { message: string; unsupported: boolean };

function quotaToMoney(q: number | null | undefined): string {
  if (q === null || q === undefined || Number.isNaN(q)) return '—';
  const dollars = q / QUOTA_PER_UNIT;
  if (Math.abs(dollars) >= 1000) return `$${dollars.toFixed(2)}`;
  return `$${dollars.toFixed(4)}`;
}

function formatDate(iso?: string): string {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString('zh-CN', { hour12: false });
  } catch {
    return iso;
  }
}

function formatTokens(l: GatewayLogEntry): string {
  const p = l.prompt_tokens;
  const c = l.completion_tokens;
  if (p === undefined && c === undefined) return '—';
  return `${p ?? 0} / ${c ?? 0}`;
}

export function ProfileClient({ user, consoleUrl }: ProfileClientProps) {
  const [info, setInfo] = useState<GatewayUserinfo | null>(null);
  const [infoErr, setInfoErr] = useState<LoadError | null>(null);
  const [logs, setLogs] = useState<GatewayLogsResult | null>(null);
  const [logsErr, setLogsErr] = useState<LoadError | null>(null);
  const [loading, setLoading] = useState(true);
  const [reloadKey, setReloadKey] = useState(0);

  // 充值 modal 状态
  const [topupOpen, setTopupOpen] = useState(false);
  const [amount, setAmount] = useState<number>(50);
  const [topupLoading, setTopupLoading] = useState(false);
  const [checkoutUrl, setCheckoutUrl] = useState<string | null>(null);
  const [topupErr, setTopupErr] = useState<LoadError | null>(null);

  useEffect(() => {
    let cancelled = false;

    const readError = async (res: Response): Promise<LoadError> => {
      const body = (await res.json().catch(() => ({}))) as { error?: string; message?: string };
      return {
        message: body.message ?? `HTTP ${res.status}`,
        unsupported: body.error === 'UNSUPPORTED',
      };
    };

    (async () => {
      // 两个接口独立处理：任一失败都不能影响另一块卡片的渲染
      const [infoRes, logsRes] = await Promise.all([
        fetch('/api/gateway/userinfo').catch(() => null),
        fetch('/api/gateway/logs?page=1&page_size=20').catch(() => null),
      ]);
      if (cancelled) return;

      if (!infoRes) {
        setInfoErr({ message: '网络异常，无法读取账户信息', unsupported: false });
      } else if (!infoRes.ok) {
        setInfoErr(await readError(infoRes));
      } else {
        const body = (await infoRes.json().catch(() => ({}))) as { data?: GatewayUserinfo };
        setInfo(body.data ?? null);
        setInfoErr(null);
      }

      if (!logsRes) {
        setLogsErr({ message: '网络异常，无法读取消费日志', unsupported: false });
      } else if (!logsRes.ok) {
        setLogsErr(await readError(logsRes));
      } else {
        const body = (await logsRes.json().catch(() => ({}))) as { data?: GatewayLogsResult };
        setLogs(body.data ?? null);
        setLogsErr(null);
      }

      if (!cancelled) setLoading(false);
    })();

    return () => { cancelled = true; };
  }, [reloadKey]);

  async function handleTopup() {
    setTopupErr(null);
    setTopupLoading(true);
    setCheckoutUrl(null);
    try {
      const res = await fetch('/api/gateway/topup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ amount }),
      });
      const data = (await res.json().catch(() => ({}))) as {
        error?: string;
        message?: string;
        data?: { checkout_url?: string; payment_url?: string };
      };
      if (!res.ok) {
        throw Object.assign(new Error(data.message ?? `HTTP ${res.status}`), {
          unsupported: data.error === 'UNSUPPORTED',
        });
      }
      const url = data.data?.checkout_url ?? data.data?.payment_url;
      if (!url) throw new Error('网关未返回支付链接');
      setCheckoutUrl(url);
      // 只有拿到支付链接才打开弹窗，避免出现空弹窗
      setTopupOpen(true);
    } catch (e) {
      setTopupErr({
        message: e instanceof Error ? e.message : String(e),
        unsupported: Boolean((e as { unsupported?: boolean })?.unsupported),
      });
    } finally {
      setTopupLoading(false);
    }
  }

  const displayName =
    user.user_metadata?.name ?? user.user_metadata?.full_name ?? info?.display_name ?? info?.username ?? '—';

  const quota = info?.quota;
  const used = info?.used_quota ?? 0;
  const usedPct = typeof quota === 'number' && used + quota > 0
    ? Math.min(100, (used / (used + quota)) * 100)
    : 0;
  const hasQuota = typeof quota === 'number';

  const consoleLink = (hash: string) => (consoleUrl ? `${consoleUrl}/app.html#${hash}` : '');

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-2xl font-bold">个人中心</h1>
        <button
          type="button"
          className="btn btn-ghost btn-xs gap-1"
          disabled={loading}
          onClick={() => { setLoading(true); setReloadKey((k) => k + 1); }}
        >
          {loading ? <span className="loading loading-spinner loading-xs" /> : <RefreshCw size={12} />}
          刷新
        </button>
      </div>

      {/* 顶部三卡片：账户 + 额度 + 充值入口 */}
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {/* 账户卡片 */}
        <div className="card border border-base-300 bg-base-100 shadow-sm">
          <div className="card-body">
            <h2 className="card-title text-lg">账户信息</h2>
            <dl className="space-y-1 text-sm">
              <div className="flex justify-between gap-2">
                <dt className="text-base-content/60">用户 ID</dt>
                <dd className="truncate font-mono text-xs" title={user.id}>{user.id}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-base-content/60">Provider</dt>
                <dd className="truncate">{info?.provider ?? user.app_metadata?.provider ?? '—'}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-base-content/60">显示名</dt>
                <dd className="truncate">{displayName}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-base-content/60">邮箱</dt>
                <dd className="truncate">{user.email ?? info?.email ?? '—'}</dd>
              </div>
              {info?.group ? (
                <div className="flex justify-between gap-2">
                  <dt className="text-base-content/60">用户组</dt>
                  <dd className="truncate">{info.group}</dd>
                </div>
              ) : null}
            </dl>
          </div>
        </div>

        {/* 额度卡片 */}
        <div className="card border border-base-300 bg-base-100 shadow-sm">
          <div className="card-body">
            <h2 className="card-title text-lg">额度概览</h2>
            {loading && !info && !infoErr ? (
              <div className="flex items-center gap-2 text-sm text-base-content/60">
                <span className="loading loading-spinner loading-sm" /> 加载中…
              </div>
            ) : infoErr ? (
              <div role="alert" className="alert alert-warning alert-soft text-sm">
                <span>{infoErr.message}</span>
              </div>
            ) : hasQuota ? (
              <>
                <div className="stats bg-transparent">
                  <div className="stat p-0">
                    <div className="stat-title">剩余额度</div>
                    <div className="stat-value text-2xl text-primary">{quotaToMoney(quota)}</div>
                    <div className="stat-desc">
                      已用 {quotaToMoney(used)} · 共 {info?.request_count ?? 0} 次请求
                    </div>
                  </div>
                </div>
                <progress className="progress progress-primary h-2" value={usedPct} max={100} />
                <p className="text-xs text-base-content/50">已完成用量占比 {usedPct.toFixed(0)}%</p>
              </>
            ) : (
              <div role="alert" className="alert alert-info alert-soft text-sm">
                <span>
                  该网关未返回额度字段
                  {consoleUrl ? (
                    <a className="link link-primary ml-1" href={consoleLink('/topup')} target="_blank" rel="noreferrer">
                      去网关查看 ↗
                    </a>
                  ) : null}
                </span>
              </div>
            )}
          </div>
        </div>

        {/* 充值入口 */}
        <div className="card border border-base-300 bg-base-100 shadow-sm">
          <div className="card-body">
            <h2 className="card-title text-lg">充值</h2>
            <p className="text-sm text-base-content/70">选择金额，打开支付窗口完成充值。</p>
            <div className="flex flex-wrap gap-2">
              {[10, 50, 100, 500].map((a) => (
                <button
                  key={a}
                  type="button"
                  className={`btn btn-sm ${amount === a ? 'btn-primary' : 'btn-ghost bg-base-200'}`}
                  onClick={() => setAmount(a)}
                >
                  ${a}
                </button>
              ))}
              <label className="input input-sm flex w-28 items-center gap-1">
                <span className="text-base-content/50">$</span>
                <input
                  type="number"
                  min={1}
                  max={10000}
                  className="w-full bg-transparent outline-none"
                  placeholder="自定义"
                  value={[10, 50, 100, 500].includes(amount) ? '' : amount}
                  onChange={(e) => {
                    const v = parseInt(e.target.value, 10);
                    if (!Number.isNaN(v) && v > 0) setAmount(v);
                  }}
                />
              </label>
            </div>
            <div className="card-actions">
              <button
                type="button"
                className="btn btn-primary btn-sm"
                disabled={topupLoading}
                onClick={() => { setTopupErr(null); setCheckoutUrl(null); handleTopup(); }}
              >
                {topupLoading ? <span className="loading loading-spinner loading-sm" /> : null}
                {topupLoading ? '创建订单中…' : '立即充值'}
              </button>
            </div>
            {topupErr ? (
              <div
                role="alert"
                className={`alert ${topupErr.unsupported ? 'alert-warning' : 'alert-error'} alert-soft text-sm`}
              >
                <span>{topupErr.message}</span>
              </div>
            ) : null}
          </div>
        </div>
      </div>

      {/* 消费日志表格 */}
      <div className="card border border-base-300 bg-base-100 shadow-sm">
        <div className="card-body">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="card-title text-lg">消费日志</h2>
            {logs?.total != null ? <span className="badge badge-ghost ml-auto">共 {logs.total} 条</span> : null}
          </div>
          {loading && !logs && !logsErr ? (
            <div className="flex items-center gap-2 text-sm text-base-content/60">
              <span className="loading loading-spinner loading-sm" /> 加载中…
            </div>
          ) : logsErr ? (
            <div
              role="alert"
              className={`alert ${logsErr.unsupported ? 'alert-warning' : 'alert-error'} alert-soft text-sm`}
            >
              <span>{logsErr.message}</span>
              {logsErr.unsupported && consoleUrl ? (
                <a className="link link-primary" href={consoleLink('/log')} target="_blank" rel="noreferrer">
                  去网关查看日志 ↗
                </a>
              ) : null}
            </div>
          ) : logs && logs.data.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="table table-sm table-zebra">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>模型</th>
                    <th>渠道</th>
                    <th className="text-right">Tokens（入 / 出）</th>
                    <th className="text-right">花费</th>
                  </tr>
                </thead>
                <tbody>
                  {logs.data.map((l, i) => (
                    <tr key={String(l.id ?? l.request_id ?? i)}>
                      <td className="whitespace-nowrap text-xs">{formatDate(l.created_at)}</td>
                      <td className="max-w-[160px] truncate" title={l.model}>{l.model ?? '—'}</td>
                      <td className="truncate text-xs">{l.channel_name ?? '—'}</td>
                      <td className="text-right font-mono text-xs">{formatTokens(l)}</td>
                      <td className="text-right font-mono text-xs">{quotaToMoney(l.quota)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="rounded-box border border-dashed border-base-300 bg-base-200/40 p-6 text-center text-sm text-base-content/60">
              暂无消费记录
            </div>
          )}
        </div>
      </div>

      {/* 积分规则折叠 */}
      <details className="collapse border border-base-300 bg-base-100">
        <summary className="collapse-title text-sm font-medium">积分规则</summary>
        <div className="collapse-content space-y-1 text-sm text-base-content/70">
          <p>new-api 以 quota 单位记账，默认 500,000 quota = $1（即 <code className="text-xs">QuotaPerUnit</code>）。</p>
          <p>实际汇率、单价由网关管理员在 new-api 后台配置；不同模型按各自 <code className="text-xs">Price</code> 计费。</p>
          <p>每次请求消耗 <code className="text-xs">prompt_tokens × prompt_price + completion_tokens × completion_price</code>，换算 quota 后从账户余额扣除。</p>
        </div>
      </details>

      {/* 充值 Modal（含 iframe + 降级 window.open） */}
      {topupOpen ? (
        <div className="modal modal-open" role="dialog" aria-modal="true" aria-label="充值">
          <div className="modal-box max-w-4xl">
            <div className="flex items-center justify-between">
              <h3 className="text-lg font-bold">充值 $ {amount}</h3>
              <button
                type="button"
                className="btn btn-ghost btn-sm btn-circle"
                aria-label="关闭"
                onClick={() => { setTopupOpen(false); setCheckoutUrl(null); }}
              >
                ✕
              </button>
            </div>
            <div className="mt-3">
              {checkoutUrl ? (
                <>
                  <iframe
                    src={checkoutUrl}
                    title="充值支付"
                    className="w-full rounded border-0"
                    style={{ height: '60vh', minHeight: 400 }}
                    onError={() => {
                      // iframe 被拦截时降级为新窗口
                      try { window.open(checkoutUrl, '_blank', 'width=720,height=640'); } catch { /* ignore */ }
                    }}
                  />
                  <div className="mt-2 flex items-center gap-2 text-xs text-base-content/60">
                    如果支付窗口未加载，
                    <a href={checkoutUrl} target="_blank" rel="noreferrer" className="link link-primary">点击在新窗口打开 ↗</a>
                  </div>
                </>
              ) : null}
            </div>
            <div className="modal-action">
              <button
                type="button"
                className="btn btn-sm"
                onClick={() => { setTopupOpen(false); setCheckoutUrl(null); }}
              >
                关闭
              </button>
            </div>
          </div>
          <button
            type="button"
            className="modal-backdrop"
            aria-label="关闭"
            onClick={() => { setTopupOpen(false); setCheckoutUrl(null); }}
          />
        </div>
      ) : null}
    </div>
  );
}
