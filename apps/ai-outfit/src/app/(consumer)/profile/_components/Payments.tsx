'use client';

import { useEffect, useRef, useState } from 'react';
import { Catalog } from './Catalog';
import { checkoutSchema, formatPrice, formatQuota, gatewayRequest, safePaymentUrl } from './profile-data';
import type { GatewayCheckoutResult, PaymentMethod, Purchase } from './profile-data';

type Receipt = { title: string; result: GatewayCheckoutResult };

export function Payments(props: {
  appQuota: number | undefined;
  platformQuota: number | undefined;
  onRefresh: () => void;
}) {
  const [amount, setAmount] = useState('50');
  const [amountError, setAmountError] = useState<string | null>(null);
  const [paymentMethod, setPaymentMethod] = useState<PaymentMethod>('waffo_pancake');
  const [purchase, setPurchase] = useState<Purchase | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const [paymentOpen, setPaymentOpen] = useState(false);
  const confirmationRef = useRef<HTMLDialogElement>(null);
  const paymentRef = useRef<HTMLDialogElement>(null);
  const triggerRef = useRef<HTMLElement | null>(null);
  const requestRef = useRef<AbortController | null>(null);
  const amountRef = useRef<HTMLInputElement>(null);
  const restoreFocusRef = useRef(false);

  useEffect(() => () => requestRef.current?.abort(), []);
  useEffect(() => {
    if (purchase && !confirmationRef.current?.open) confirmationRef.current?.showModal();
  }, [purchase]);
  useEffect(() => {
    if (paymentOpen && !paymentRef.current?.open) paymentRef.current?.showModal();
  }, [paymentOpen]);

  const validAmount = /^\d+$/.test(amount) && Number.isInteger(Number(amount)) && Number(amount) >= 1 && Number(amount) <= 10000;
  const locked = submitting || purchase !== null || paymentOpen;
  useEffect(() => {
    // 先解除 fieldset / 按钮禁用，再恢复焦点；close 事件内直接 focus 会被浏览器忽略。
    if (!locked && restoreFocusRef.current) {
      restoreFocusRef.current = false;
      triggerRef.current?.focus();
    }
  }, [locked]);
  const paymentUrl = safePaymentUrl(receipt?.result.checkout_url ?? receipt?.result.payment_url);
  let purchaseTitle = 'App 充值';
  if (purchase?.kind === 'plan') purchaseTitle = `套餐：${purchase.item.title}`;
  if (purchase?.kind === 'product') purchaseTitle = `额度商品：${purchase.item.name}`;

  function confirmPurchase(next: Purchase) {
    if (locked || requestRef.current) return;
    if (next.kind === 'plan' && next.payment_method === 'balance' && !next.item.allow_balance_pay) return;
    triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setError(null);
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
      const result = await gatewayRequest(isTopup ? '/api/gateway/topup' : '/api/gateway/checkout', checkoutSchema, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      setReceipt({ title: purchaseTitle, result });
      confirmationRef.current?.close();
      if (result.status === 'success') {
        props.onRefresh();
      } else {
        setPaymentOpen(true);
      }
    } catch (cause) {
      if (!controller.signal.aborted) {
        setError(`${cause instanceof Error ? cause.message : '网络异常'}。若请求已发出但结果不明，请先刷新额度或联系管理员核对，勿重复付款。`);
        confirmationRef.current?.close();
        props.onRefresh();
      }
    } finally {
      if (!controller.signal.aborted) setSubmitting(false);
      requestRef.current = null;
    }
  }

  function closePayment() {
    restoreFocusRef.current = true;
    setPaymentOpen(false);
    props.onRefresh();
  }

  return (
    <div className="space-y-6">
      <section className="card border border-base-300 bg-base-100 shadow-sm" aria-busy={submitting}>
        <div className="card-body gap-4">
          <h2 className="card-title text-lg">App 充值与支付</h2>
          <p className="text-base-content/70">充值到当前 App 额度池，用于换装消费；平台余额不能直接用于本 App 的请求。</p>
          <fieldset className="fieldset" disabled={locked}>
            <legend className="fieldset-legend">支付方式（充值与目录购买共用）</legend>
            <label htmlFor="profile-payment-method">选择付款方式</label>
            <select id="profile-payment-method" className="select w-full sm:max-w-md" value={paymentMethod}
              onChange={(event) => setPaymentMethod(event.target.value as PaymentMethod)}>
              <option value="waffo_pancake">Waffo Pancake 在线支付</option>
              <option value="balance">余额支付：充值划转平台余额，购买扣 App 余额</option>
            </select>
            <p className="text-sm text-base-content/70">
              balance：充值从平台余额划转到 App 池；套餐 / 商品从 App 池扣款。
              waffo_pancake：在线付款，待网关确认后生效。
            </p>
          </fieldset>
          <form onSubmit={(event) => {
            event.preventDefault();
            if (!validAmount) {
              setAmountError('请输入 1 至 10000 的整数，不接受小数');
              amountRef.current?.focus();
              return;
            }
            confirmPurchase({ kind: 'topup', amount: Number(amount), payment_method: paymentMethod });
          }}>
            <fieldset className="fieldset gap-3" disabled={locked}>
              <legend className="fieldset-legend">充值数量 amount（网关配置展示单位，必填）</legend>
              <div className="flex flex-wrap gap-2">
                {[10, 50, 100, 500].map((value) => (
                  <button key={value} type="button" aria-pressed={amount === String(value)}
                    className={`btn ${amount === String(value) ? 'btn-primary' : 'btn-ghost bg-base-200'}`}
                    onClick={() => { setAmount(String(value)); setAmountError(null); }}>
                    {value}
                  </button>
                ))}
              </div>
              <label htmlFor="profile-topup-amount">自定义整数数量</label>
              <input ref={amountRef} id="profile-topup-amount" type="number" min={1} step={1}
                max={10000} required className="input w-full sm:max-w-xs" value={amount}
                aria-invalid={amountError !== null} aria-describedby="profile-amount-help profile-amount-error"
                onChange={(event) => { setAmount(event.target.value); setAmountError(null); }}
                onBlur={() => setAmountError(validAmount ? null : '请输入 1 至 10000 的整数，不接受小数')} />
              <p id="profile-amount-help" className="text-sm text-base-content/70">
                amount 是网关配置的展示单位整数（1 至 10000），不是 quota，也不固定代表美元；换算额度、渠道最低充值量和实付金额由网关决定。
              </p>
              <p id="profile-amount-error" role={amountError ? 'alert' : undefined} className="text-sm text-error">{amountError}</p>
              <div className="card-actions">
                <button type="submit" className="btn btn-primary" disabled={locked}>
                  {submitting ? <span className="loading loading-spinner loading-sm" aria-hidden="true" /> : null}
                  {submitting ? '正在提交…' : '确认充值 / 划转'}
                </button>
              </div>
            </fieldset>
          </form>
          {error ? <div role="alert" className="alert alert-error alert-soft"><span>{error}</span></div> : null}
          {receipt ? (
            <div role="status" className={`alert alert-soft ${receipt.result.status === 'success' ? 'alert-success' : 'alert-warning'}`}>
              <div className="space-y-2 wrap-anywhere">
                <p>{receipt.title}：{receipt.result.status === 'success' ? '网关确认成功，已请求刷新额度与消费日志。' : 'pending 待网关确认，尚不能认定付款成功。'}</p>
                {receipt.result.order_id ? <p>订单号：{receipt.result.order_id}</p> : null}
                <p className="text-sm">仅保留本次页面会话的返回信息，不是订单历史或订阅状态。</p>
              </div>
              {receipt.result.status === 'pending' ? (
                <button type="button" className="btn btn-outline" disabled={locked} onClick={() => {
                  triggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
                  setPaymentOpen(true);
                }}>查看待确认订单</button>
              ) : null}
            </div>
          ) : null}
        </div>
      </section>

      <Catalog paymentMethod={paymentMethod} disabled={locked} onPurchase={confirmPurchase} />

      {/* WHY: CharacterUploader / MediaCard 等现有组件没有支付确认或原生焦点管理能力，复用 DaisyUI 原生 dialog。 */}
      <dialog ref={confirmationRef} className="modal" aria-labelledby="profile-confirm-title"
        onCancel={(event) => { if (submitting) event.preventDefault(); }}
        onClose={() => { restoreFocusRef.current = true; setPurchase(null); }}>
        <div className="modal-box space-y-4">
          <h3 id="profile-confirm-title" className="wrap-anywhere text-lg font-bold">确认付款：{purchaseTitle}</h3>
          {purchase?.kind === 'topup' ? (
            <p>充值数量：{purchase.amount}（配置展示单位），到账 quota 由网关换算，不将平台 quota 当作 amount 提交。</p>
          ) : purchase ? (
            <div className="space-y-2">
              <p>购买数量：1 份；展示价：{formatPrice(purchase.item.price_amount, purchase.item.currency)}。</p>
              {purchase.kind === 'plan' ? (
                <p>套餐额度：{purchase.item.total_amount === 0 ? '不限额度（套餐规则）' : formatQuota(purchase.item.total_amount)}；不是直接充值到 App 余额。</p>
              ) : <p>商品到账额度：{formatQuota(purchase.item.quota_amount)}，计入 App 池。</p>}
            </div>
          ) : null}
          {purchase?.payment_method === 'balance' ? (
            <div className="alert alert-warning alert-soft">
              {purchase.kind === 'topup' ? (
                <span>将从平台余额扣除换算后的额度，并划转同等 quota 到当前 App 池。平台可划转余额：{formatQuota(props.platformQuota)}。不是免费赠送。</span>
              ) : (
                <span>将从当前 App 额度池扣除展示价换算的 quota，不扣平台余额。当前 App 剩余：{formatQuota(props.appQuota)}；余额不足时由网关拒绝。</span>
              )}
            </div>
          ) : (
            <div className="alert alert-warning alert-soft"><span>将创建 Waffo Pancake 在线订单，请在支付页核对实付金额与币种。pending 或浏览器返回都不代表支付成功。</span></div>
          )}
          <p className="text-sm text-base-content/70">网关校验余额、换算、渠道和限购规则；本页面不能提前确认实际扣款 quota。</p>
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

      <dialog ref={paymentRef} className="modal" aria-labelledby="profile-payment-title" onClose={closePayment}>
        <div className="modal-box max-w-4xl space-y-4">
          <h3 id="profile-payment-title" className="text-lg font-bold">在线付款 · pending 待网关确认</h3>
          <p className="wrap-anywhere">{receipt?.title} · 订单号：{receipt?.result.order_id || '网关未返回订单号，请联系管理员核对'}</p>
          <div className="alert alert-warning alert-soft"><span>网关确认前不会记为已付款。关闭（含 Escape）后重新读取 App 额度与消费日志；余额未变化时请稍后手动刷新，不要重复付款。</span></div>
          {paymentOpen && paymentUrl ? (
            <>
              <a className="btn btn-outline" href={paymentUrl} target="_blank" rel="noopener noreferrer">在新窗口打开支付页</a>
              <iframe src={paymentUrl} title="在线订单支付页" className="h-96 w-full border-0"
                sandbox="allow-forms allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox"
                referrerPolicy="no-referrer" />
              <p className="text-sm text-base-content/70">若支付页被禁止嵌入或未能加载，请手动使用上方新窗口链接；不会自动打开窗口或认定已付款。</p>
            </>
          ) : (
            <div role="alert" className="alert alert-error alert-soft"><span>网关未提供有效的 HTTPS 支付链接，已阻止打开。请保留订单号并联系管理员，不要重复付款。</span></div>
          )}
          <div className="modal-action">
            <button type="button" className="btn btn-ghost" onClick={props.onRefresh}>手动刷新额度与日志</button>
            <button type="button" className="btn" autoFocus onClick={() => paymentRef.current?.close()}>关闭并刷新</button>
          </div>
        </div>
      </dialog>
    </div>
  );
}
