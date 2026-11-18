'use client';

import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react';
import { z } from 'zod';
import { Catalog } from './Catalog';
import { checkoutSchema, formatCredits, formatPrice, gatewayRequest, paymentMethodLabels, paymentMethodsSchema, safePaymentUrl } from './profile-data';
import type { GatewayCheckoutResult, PaymentMethod, Purchase } from './profile-data';
import { useGatewayResource } from './use-gateway-resource';

type Receipt = { title: string; result: GatewayCheckoutResult; storageKey: string };
const orderSchema = z.object({ order_id: z.string(), app_id: z.number().int().nonnegative(), status: z.string() });

export function Payments(props: {
  userId: string;
  appId?: number;
  subscriptionSupported?: boolean;
  appQuota: number | undefined;
  onRefresh: () => void;
}) {
  const [amount, setAmount] = useState('50');
  const [amountError, setAmountError] = useState<string | null>(null);
  const [paymentMethod, setPaymentMethod] = useState<PaymentMethod>('balance');
  const [preview, setPreview] = useState<{money: number; currency: string; quota: number; credits: string | null} | null>(null);
  const [purchase, setPurchase] = useState<Purchase | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const receiptKey = `checkout-receipt:${props.userId}:${props.appId}`;
  const serializedReceipt = useSyncExternalStore(callback => {
    window.addEventListener('storage', callback);
    window.addEventListener('checkout-receipt', callback);
    return () => { window.removeEventListener('storage', callback); window.removeEventListener('checkout-receipt', callback); };
  }, () => sessionStorage.getItem(receiptKey), () => null);
  const receipt = useMemo<Receipt | null>(() => {
    try {
      const stored = JSON.parse(serializedReceipt ?? 'null');
      return stored && typeof stored.title === 'string' && typeof stored.storageKey === 'string' && checkoutSchema.safeParse(stored.result).success ? stored : null;
    } catch { return null; }
  }, [serializedReceipt]);
  function setReceipt(value: Receipt | null) {
    if (value) sessionStorage.setItem(receiptKey, JSON.stringify(value));
    else sessionStorage.removeItem(receiptKey);
    window.dispatchEvent(new Event('checkout-receipt'));
  }
  const [methodsRevision, setMethodsRevision] = useState(0);
  const methods = useGatewayResource('/api/gateway/payment-methods', paymentMethodsSchema, methodsRevision);
  const confirmationRef = useRef<HTMLDialogElement>(null);
  const triggerRef = useRef<HTMLElement | null>(null);
  const requestRef = useRef<AbortController | null>(null);
  const amountRef = useRef<HTMLInputElement>(null);
  const restoreFocusRef = useRef(false);

  useEffect(() => () => requestRef.current?.abort(), []);

  async function checkOrder() {
    if (!receipt?.result.order_id || submitting) return;
    setSubmitting(true); setError(null);
    try {
      const order = await gatewayRequest(`/api/gateway/orders/${encodeURIComponent(receipt.result.order_id)}`, orderSchema);
      if (order.order_id !== receipt.result.order_id || (props.appId !== undefined && order.app_id !== props.appId)) throw new Error('订单归属不匹配');
      if (['success', 'failed', 'expired', 'cancelled'].includes(order.status)) {
        sessionStorage.removeItem(receipt.storageKey);
        sessionStorage.removeItem(receiptKey);
        setReceipt(null);
        props.onRefresh();
        setError(order.status === 'success' ? '已确认付款成功，可开始新的购买。' : '订单已结束，可开始新的购买。');
      } else {
        setError('订单尚未确认，请稍后重试。');
      }
    } catch (cause) { setError(cause instanceof Error ? cause.message : '订单查询失败'); }
    finally { setSubmitting(false); }
  }
  useEffect(() => {
    if (purchase && !confirmationRef.current?.open) confirmationRef.current?.showModal();
  }, [purchase]);
  const availableMethods = methods.data?.payment_methods ?? [];
  const effectiveMethod = availableMethods.includes(paymentMethod) ? paymentMethod : availableMethods[0] ?? 'balance';
  const validAmount = /^\d+$/.test(amount) && Number.isInteger(Number(amount)) && Number(amount) >= 1 && Number(amount) <= 10000;
  const locked = submitting || purchase !== null;
  useEffect(() => {
    // 先解除 fieldset / 按钮禁用，再恢复焦点；close 事件内直接 focus 会被浏览器忽略。
    if (!locked && restoreFocusRef.current) {
      restoreFocusRef.current = false;
      triggerRef.current?.focus();
    }
  }, [locked]);
  let purchaseTitle = '余额充值';
  if (purchase?.kind === 'plan') purchaseTitle = `套餐：${purchase.item.title}`;
  if (purchase?.kind === 'product') purchaseTitle = `商品：${purchase.item.name}`;

  async function confirmPurchase(next: Purchase) {
    if (locked || requestRef.current || !availableMethods.includes(next.payment_method)) return;
    if (next.kind === 'plan' && next.payment_method === 'balance' && !next.item.allow_balance_pay) return;
    triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setError(null);
    setPreview(null);
    if (next.kind === 'topup' && next.payment_method === 'waffo_pancake') {
      setSubmitting(true);
      try {
        setPreview(await gatewayRequest('/api/gateway/topup/preview', z.object({money:z.number().positive(), currency:z.string(), quota:z.number().int().safe().nonnegative(), credits:z.string().nullable()}), {
          method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({amount:next.amount,payment_method:next.payment_method}),
        }));
      } catch (cause) { setError(cause instanceof Error ? cause.message : '无法获取充值预览'); return; }
      finally { setSubmitting(false); }
    }
    setPurchase(next);
  }

  async function submitPurchase() {
    if (!purchase || requestRef.current) return;
    const controller = new AbortController();
    requestRef.current = controller;
    setSubmitting(true);
    setError(null);
    setReceipt(null);
    const isTopup = purchase.kind === 'topup';
    const body = isTopup
      ? { amount: purchase.amount, payment_method: purchase.payment_method }
      : { kind: purchase.kind, id: purchase.item.id, payment_method: purchase.payment_method };
    try {
      const storageKey = `checkout:${props.userId}:${props.appId}:${JSON.stringify(body)}`;
      let requestId = sessionStorage.getItem(storageKey);
      if (!requestId) { requestId = crypto.randomUUID(); sessionStorage.setItem(storageKey, requestId); }
      const result = await gatewayRequest(isTopup ? '/api/gateway/topup' : '/api/gateway/checkout', checkoutSchema, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': requestId },
        body: JSON.stringify(body),
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const savedReceipt = { title: purchaseTitle, result, storageKey };
      setReceipt(savedReceipt);
      sessionStorage.setItem(receiptKey, JSON.stringify(savedReceipt));
      confirmationRef.current?.close();
      if (result.status === 'success') {
        sessionStorage.removeItem(storageKey);
        sessionStorage.removeItem(receiptKey);
        props.onRefresh();
      } else {
        const redirectUrl = safePaymentUrl(result.checkout_url ?? result.pay_link);
        if (redirectUrl) {
          window.location.assign(redirectUrl);
        } else if (result.url && result.params) {
          try {
            const epayUrl = new URL(result.url);
            if ((epayUrl.protocol === 'https:' || epayUrl.protocol === 'http:') && !epayUrl.username && !epayUrl.password) {
              const form = document.createElement('form');
              form.method = 'POST';
              form.action = epayUrl.href;
              form.target = '_blank';
              form.rel = 'noopener';
              for (const [key, value] of Object.entries(result.params)) {
                const input = document.createElement('input');
                input.type = 'hidden';
                input.name = key;
                input.value = value;
                form.appendChild(input);
              }
              document.body.appendChild(form);
              form.submit();
              form.remove();
            } else {
              setError('支付页面无法打开，请保留订单号并联系管理员核对，不要重复付款。');
            }
          } catch {
            setError('支付页面无法打开，请保留订单号并联系管理员核对，不要重复付款。');
          }
        } else {
          setError('支付页面无法打开，请保留订单号并联系管理员核对，不要重复付款。');
        }
      }
    } catch (cause) {
      if (!controller.signal.aborted) {
        setError(`${cause instanceof Error ? cause.message : '网络异常'}。若请求已发出但结果不明，请先刷新余额或联系管理员核对，勿重复付款。`);
        confirmationRef.current?.close();
        props.onRefresh();
      }
    } finally {
      if (!controller.signal.aborted) setSubmitting(false);
      requestRef.current = null;
    }
  }

  return (
    <div className="space-y-6">
      <section className="card border border-base-300 bg-base-100 shadow-sm" aria-busy={submitting}>
        <div className="card-body gap-4">
          <h2 className="card-title text-lg">余额充值</h2>
          <p className="text-base-content/70">充值后获得余额，换装消费会从余额中扣除。</p>
          <fieldset className="fieldset" disabled={locked}>
            <legend className="fieldset-legend">支付方式</legend>
            <label htmlFor="profile-payment-method">选择付款方式</label>
            {methods.loading ? (
              <div role="status" className="space-y-2">
                <p className="text-base-content/70">正在加载支付方式…</p>
                <div className="skeleton h-10 w-full sm:max-w-md" />
              </div>
            ) : methods.error ? (
              <div role="alert" className="alert alert-error alert-soft">
                <span>{methods.error}</span>
                <button type="button" className="btn btn-outline" onClick={() => setMethodsRevision((value) => value + 1)}>重试</button>
              </div>
            ) : (
              <select id="profile-payment-method" className="select w-full sm:max-w-md" value={effectiveMethod}
                onChange={(event) => setPaymentMethod(event.target.value as PaymentMethod)}>
                {availableMethods.map((method) => (
                  <option key={method} value={method}>{paymentMethodLabels[method] ?? method}</option>
                ))}
              </select>
            )}
          </fieldset>
          {!methods.loading && !methods.error && !availableMethods.length && <p role="status">当前未配置可用的付款方式。</p>}
          <form onSubmit={(event) => {
            event.preventDefault();
            if (!validAmount) {
              setAmountError('请输入 1 至 10000 的整数，不接受小数');
              amountRef.current?.focus();
              return;
            }
            confirmPurchase({ kind: 'topup', amount: Number(amount), payment_method: effectiveMethod });
          }}>
            <fieldset className="fieldset gap-3" disabled={locked}>
              <legend className="fieldset-legend">充值数量</legend>
              <div className="flex flex-wrap gap-2">
                {[10, 50, 100, 500].map((value) => (
                  <button key={value} type="button" aria-pressed={amount === String(value)}
                    className={`btn ${amount === String(value) ? 'btn-primary' : 'btn-ghost bg-base-200'}`}
                    onClick={() => { setAmount(String(value)); setAmountError(null); }}>
                    {value}
                  </button>
                ))}
              </div>
              <label htmlFor="profile-topup-amount">自定义数量（1 至 10000 的整数）</label>
              <input ref={amountRef} id="profile-topup-amount" type="number" min={1} step={1}
                max={10000} required className="input w-full sm:max-w-xs" value={amount}
                aria-invalid={amountError !== null} aria-describedby="profile-amount-help profile-amount-error"
                onChange={(event) => { setAmount(event.target.value); setAmountError(null); }}
                onBlur={() => setAmountError(validAmount ? null : '请输入 1 至 10000 的整数，不接受小数')} />
              <p id="profile-amount-help" className="text-sm text-base-content/70">
                实际到账金额与支付金额以网关结算结果为准。
              </p>
              <p id="profile-amount-error" role={amountError ? 'alert' : undefined} className="text-sm text-error">{amountError}</p>
              <div className="card-actions">
                <button type="submit" className="btn btn-primary" disabled={locked || !availableMethods.length}>
                  {submitting ? <span className="loading loading-spinner loading-sm" aria-hidden="true" /> : null}
                  {submitting ? '正在提交…' : '确认充值'}
                </button>
              </div>
            </fieldset>
          </form>
          {error ? <div role="alert" className="alert alert-error alert-soft"><span>{error}</span></div> : null}
          {receipt ? (
            <div role="status" className={`alert alert-soft ${receipt.result.status === 'success' ? 'alert-success' : 'alert-warning'}`}>
              <div className="space-y-2 wrap-anywhere">
                <p>{receipt.title}：{receipt.result.status === 'success' ? '支付成功，余额已更新。' : '支付处理中，请稍后在本页刷新确认，暂时不要重复付款。'}</p>
                {receipt.result.order_id ? <><p>订单号：{receipt.result.order_id}</p><button type="button" className="btn btn-outline" disabled={submitting} onClick={checkOrder}>查询订单状态</button></> : null}
              </div>
            </div>
          ) : null}
        </div>
      </section>

      <Catalog subscriptionSupported={props.subscriptionSupported} paymentMethod={availableMethods.length ? effectiveMethod : null} disabled={locked || !availableMethods.length} onPurchase={confirmPurchase} />

      {/* WHY: CharacterUploader / MediaCard 等现有组件没有支付确认或原生焦点管理能力，复用 DaisyUI 原生 dialog。 */}
      <dialog ref={confirmationRef} className="modal" aria-labelledby="profile-confirm-title"
        onCancel={(event) => { if (submitting) event.preventDefault(); }}
        onClose={() => { restoreFocusRef.current = true; setPurchase(null); }}>
        <div className="modal-box space-y-4">
          <h3 id="profile-confirm-title" className="wrap-anywhere text-lg font-bold">确认付款：{purchaseTitle}</h3>
          {purchase?.kind === 'topup' ? (
            <p>{preview ? `支付 ${formatPrice(preview.money, preview.currency)}；预计到账 ${formatCredits(preview.quota, preview.credits)}` : `充值数量：${purchase.amount}，请在收银台核对实际金额。`}</p>
          ) : purchase ? (
            <div className="space-y-2">
              <p>购买数量：1 份；价格：{formatPrice(purchase.item.price_amount, purchase.item.currency)}。</p>
              {purchase.kind === 'plan' ? (
                <p>套餐到账：{purchase.item.total_amount === 0 ? '不限' : formatCredits(purchase.item.total_amount, purchase.item.credits)}。</p>
              ) : <p>到账金额：{formatCredits(purchase.item.quota_amount, purchase.item.credits)}。</p>}
            </div>
          ) : null}
          {purchase?.payment_method === 'balance' ? (
            <div className="alert alert-warning alert-soft">
              {purchase.kind === 'topup' ? (
                <span>将使用账户余额充值，余额不足时会被拒绝。</span>
              ) : (
                <span>将从余额中扣款，当前剩余 {formatCredits(props.appQuota)}，余额不足时会被拒绝。</span>
              )}
            </div>
          ) : (
            <div className="alert alert-warning alert-soft"><span>将创建在线支付订单，请在支付页核对金额；未完成支付前不要重复提交。</span></div>
          )}
          <div className="modal-action">
            <button type="button" className="btn btn-ghost" autoFocus disabled={submitting}
              onClick={() => confirmationRef.current?.close()}>取消</button>
            <button type="button" className="btn btn-primary" disabled={submitting || !purchase} onClick={submitPurchase}>
              {submitting ? <span className="loading loading-spinner loading-sm" aria-hidden="true" /> : null}
              {submitting ? '正在提交…' : '确认付款'}
            </button>
          </div>
        </div>
      </dialog>
    </div>
  );
}
