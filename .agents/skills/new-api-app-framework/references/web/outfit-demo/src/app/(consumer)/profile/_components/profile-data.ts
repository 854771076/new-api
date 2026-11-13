import { z } from 'zod';
import { QUOTA_PER_UNIT } from '@/lib/constants';

const quotaSchema = z.number().int().safe().nonnegative();

export const userinfoSchema = z.object({
  id: z.string(),
  username: z.string().optional(),
  display_name: z.string().optional(),
  email: z.string().optional(),
  provider: z.string().optional(),
  quota: quotaSchema.optional(),
  app_quota: quotaSchema.optional(),
  app_used_quota: quotaSchema.optional(),
  request_count: quotaSchema.optional(),
  group: z.string().optional(),
  subscription: z.object({
    plan_title: z.string().optional(),
    total_amount: quotaSchema,
    end_time: quotaSchema,
  }).nullable().optional(),
});

export const logsSchema = z.object({
  data: z.array(z.object({
    id: z.union([z.number(), z.string()]).optional(),
    model: z.string().optional(),
    model_name: z.string().optional(),
    prompt_tokens: quotaSchema.optional(),
    completion_tokens: quotaSchema.optional(),
    quota: quotaSchema.optional(),
    channel_name: z.string().optional(),
    request_id: z.string().optional(),
    created_at: z.string().optional(),
    app_id: quotaSchema.optional(),
  })),
  total: quotaSchema,
  page: quotaSchema.positive(),
  page_size: quotaSchema.positive(),
});

export const catalogSchema = z.object({
  plans: z.array(z.object({
    id: quotaSchema.positive(),
    title: z.string(),
    price_amount: z.number().nonnegative(),
    currency: z.string(),
    total_amount: quotaSchema,
    duration_unit: z.string(),
    duration_value: quotaSchema,
    allow_balance_pay: z.boolean(),
    max_purchase_per_user: quotaSchema,
    quota_reset_period: z.string().optional(),
  })),
  products: z.array(z.object({
    id: quotaSchema.positive(),
    name: z.string(),
    description: z.string(),
    price_amount: z.number().nonnegative(),
    currency: z.string(),
    quota_amount: quotaSchema,
    max_purchase_per_user: quotaSchema,
  })),
});

export const checkoutSchema = z.object({
  status: z.enum(['success', 'pending']),
  payment_method: z.string().optional(),
  order_id: z.string().optional(),
  checkout_url: z.string().optional(),
  payment_url: z.string().optional(),
  pay_link: z.string().optional(),
  url: z.string().optional(),
  params: z.record(z.string(), z.string()).optional(),
  expires_at: z.union([z.string(), z.number()]).optional(),
  quota: quotaSchema.optional(),
  quota_amount: quotaSchema.optional(),
});

export type GatewayPlan = z.infer<typeof catalogSchema>['plans'][number];
export type GatewayProduct = z.infer<typeof catalogSchema>['products'][number];
export type GatewayCheckoutResult = z.infer<typeof checkoutSchema>;
export type PaymentMethod = string;
export type Purchase =
  | { kind: 'topup'; amount: number; payment_method: PaymentMethod }
  | { kind: 'plan'; item: GatewayPlan; payment_method: PaymentMethod }
  | { kind: 'product'; item: GatewayProduct; payment_method: PaymentMethod };

export interface SupabaseUserLite {
  id: string;
  email?: string | null;
  app_metadata: { provider?: string };
  user_metadata: { name?: string; full_name?: string };
}

// 网关 quota 为基本记账单位；new-api 默认 $1 = 500,000 quota，此处换算为美元价格显示。
export function formatCredits(value: number | undefined): string {
  if (value === undefined) return '—';
  return `$${(value / QUOTA_PER_UNIT).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

export function formatPrice(amount: number, currency: string): string {
  return `${amount.toLocaleString('zh-CN', { maximumFractionDigits: 20 })} ${currency}`;
}

export function safePaymentUrl(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (url.protocol !== 'https:' || url.username || url.password) return null;
    return url.href;
  } catch {
    return null;
  }
}

export const paymentMethodsSchema = z.object({
  payment_methods: z.array(z.string()),
});

export const paymentMethodLabels: Record<string, string> = {
  balance: '余额支付',
  stripe: 'Stripe',
  creem: 'Creem',
  epay: '易支付',
  waffo_pancake: 'Waffo Pancake',
};

const envelopeSchema = z.object({
  success: z.boolean(),
  message: z.string().optional(),
  error_code: z.string().nullable().optional(),
  data: z.unknown().optional(),
});

export async function gatewayRequest<T>(
  path: string,
  schema: z.ZodType<T>,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(path, { ...options, cache: 'no-store' });
  if (options.signal?.aborted) throw new DOMException('请求已取消', 'AbortError');
  if (response.status === 401) {
    window.location.assign('/login?error=session_expired');
    throw new Error('登录状态已失效，请重新登录');
  }
  const envelope = envelopeSchema.safeParse(await response.json().catch(() => null));
  if (!envelope.success) throw new Error(`网关响应格式异常（HTTP ${response.status}），请刷新重试`);
  if (!response.ok || !envelope.data.success) {
    const message = envelope.data.message || `请求失败（HTTP ${response.status}）`;
    const code = envelope.data.error_code;
    throw new Error(code ? `${message}（${code}）` : message);
  }
  const result = schema.safeParse(envelope.data.data);
  if (!result.success) throw new Error('网关返回的数据不完整，请刷新重试');
  return result.data;
}
