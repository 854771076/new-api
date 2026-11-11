import type { ReactNode } from 'react';
import { catalogSchema, formatPrice, formatQuota } from './profile-data';
import type { PaymentMethod, Purchase } from './profile-data';
import { useGatewayResource } from './use-gateway-resource';
import { useState } from 'react';

const durationLabels: Record<string, string> = {
  year: '年', month: '个月', week: '周', day: '天', hour: '小时', second: '秒', custom: '秒',
};
const resetLabels: Record<string, string> = {
  never: '不重置', daily: '每天', weekly: '每周', monthly: '每月', custom: '网关自定义周期',
};

function CatalogSection(props: {
  title: string;
  loading: boolean;
  error: string | null;
  empty: boolean;
  onRetry: () => void;
  children: ReactNode;
}) {
  return (
    <section className="card border border-base-300 bg-base-100 shadow-sm" aria-busy={props.loading}>
      <div className="card-body gap-4">
        <h2 className="card-title text-lg">{props.title}</h2>
        {props.loading ? (
          <div role="status" className="space-y-3">
            <p className="text-base-content/70">正在加载{props.title}…</p>
            <div className="skeleton h-24 w-full" />
          </div>
        ) : props.error ? (
          <div role="alert" className="alert alert-error alert-soft">
            <span>{props.error}</span>
            <button type="button" className="btn btn-outline" onClick={props.onRetry}>重试目录</button>
          </div>
        ) : props.empty ? (
          <div className="space-y-3">
            <p className="text-base-content/70">当前 App 暂无可购买的{props.title}，可稍后刷新目录。</p>
            <button type="button" className="btn btn-ghost" onClick={props.onRetry}>刷新目录</button>
          </div>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">{props.children}</div>
        )}
      </div>
    </section>
  );
}

export function Catalog(props: {
  paymentMethod: PaymentMethod;
  disabled: boolean;
  onPurchase: (purchase: Purchase) => void;
}) {
  const [revision, setRevision] = useState(0);
  const catalog = useGatewayResource('/api/gateway/catalog', catalogSchema, revision);
  const retry = () => setRevision((value) => value + 1);

  return (
    <div className="space-y-4">
      <p className="text-sm text-base-content/70">
        目录由 new-api 提供，仅显示当前 App 的套餐与额度商品；每次购买 1 份，限购与可用支付渠道以网关校验为准。
        当前付款方式：{props.paymentMethod === 'balance' ? 'App 余额支付（不是平台余额）' : 'Waffo Pancake 在线支付'}。
      </p>
      <CatalogSection title="套餐" loading={catalog.loading} error={catalog.error}
        empty={!catalog.data?.plans.length} onRetry={retry}>
        {catalog.data?.plans.map((plan) => (
          <article key={plan.id} className="card card-sm min-w-0 bg-base-200">
            <div className="card-body gap-3">
              <h3 className="card-title wrap-anywhere">{plan.title}</h3>
              <p className="tabular-nums">展示价：{formatPrice(plan.price_amount, plan.currency)}</p>
              <p>套餐额度：{plan.total_amount === 0 ? '不限额度（网关套餐规则）' : formatQuota(plan.total_amount)}</p>
              <p>有效期：{plan.duration_value} {durationLabels[plan.duration_unit] ?? plan.duration_unit}</p>
              <p>额度重置：{plan.quota_reset_period ? (resetLabels[plan.quota_reset_period] ?? plan.quota_reset_period) : '网关未提供'}</p>
              <p className="text-sm text-base-content/70">
                每人限购：{plan.max_purchase_per_user === 0 ? '不限' : `${plan.max_purchase_per_user} 份`}
              </p>
              {!plan.allow_balance_pay ? <p className="text-sm text-warning">此套餐不支持 App 余额支付，请选择在线支付。</p> : null}
              <div className="card-actions mt-auto">
                <button type="button" className="btn btn-outline w-full"
                  disabled={props.disabled || (props.paymentMethod === 'balance' && !plan.allow_balance_pay)}
                  onClick={() => props.onPurchase({ kind: 'plan', item: plan, payment_method: props.paymentMethod })}>
                  购买套餐
                </button>
              </div>
            </div>
          </article>
        ))}
      </CatalogSection>
      <p className="text-sm text-base-content/70">网关尚未提供 App 订阅状态接口，此处不是已订阅列表。</p>
      <CatalogSection title="额度商品" loading={catalog.loading} error={catalog.error}
        empty={!catalog.data?.products.length} onRetry={retry}>
        {catalog.data?.products.map((product) => (
          <article key={product.id} className="card card-sm min-w-0 bg-base-200">
            <div className="card-body gap-3">
              <h3 className="card-title wrap-anywhere">{product.name}</h3>
              {product.description ? <p className="whitespace-pre-wrap wrap-anywhere text-base-content/70">{product.description}</p> : null}
              <p className="tabular-nums">展示价：{formatPrice(product.price_amount, product.currency)}</p>
              <p>到账 App 额度：{formatQuota(product.quota_amount)}</p>
              <p className="text-sm text-base-content/70">
                每人限购：{product.max_purchase_per_user === 0 ? '不限' : `${product.max_purchase_per_user} 份`}
              </p>
              <div className="card-actions mt-auto">
                <button type="button" className="btn btn-outline w-full" disabled={props.disabled}
                  onClick={() => props.onPurchase({ kind: 'product', item: product, payment_method: props.paymentMethod })}>
                  购买额度商品
                </button>
              </div>
            </div>
          </article>
        ))}
      </CatalogSection>
    </div>
  );
}
