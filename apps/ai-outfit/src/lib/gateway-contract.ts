import { z } from 'zod';

export const paymentMethodSchema = z.enum(['balance', 'stripe', 'creem', 'epay', 'waffo_pancake']);
export const gatewayTopupSchema = z.object({
  amount: z.number().int().min(1).max(10_000),
  payment_method: paymentMethodSchema,
}).strict();
export const gatewayCheckoutSchema = z.object({
  kind: z.enum(['plan', 'product']),
  id: z.number().int().positive().max(Number.MAX_SAFE_INTEGER),
  payment_method: paymentMethodSchema,
}).strict();

const quota = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
const checkoutUrl = z.string().url().refine(value => {
  const url = new URL(value);
  return url.protocol === 'https:' && !url.username && !url.password;
});
// 易支付网关可能使用 http 地址；仅校验协议与凭据，不强制 https。
const paymentFormUrl = z.string().url().refine(value => {
  const url = new URL(value);
  return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password;
});
export const gatewayCheckoutResultSchema = z.object({
  status: z.enum(['success', 'pending']),
  payment_method: paymentMethodSchema.optional(),
  checkout_url: checkoutUrl.optional(),
  pay_link: checkoutUrl.optional(),
  url: paymentFormUrl.optional(),
  params: z.record(z.string(), z.string()).optional(),
  order_id: z.string().min(1).optional(),
  expires_at: z.union([z.string(), z.number().finite()]).optional(),
  quota: quota.optional(),
  quota_amount: quota.optional(),
}).refine(value => value.status !== 'pending' || !!value.checkout_url || !!value.pay_link || !!value.url, {
  message: 'Pending checkout requires a checkout URL',
});

export const gatewayPlanSchema = z.object({
  id: z.number().int().positive(),
  app_id: z.number().int().positive(),
  title: z.string(),
  price_amount: z.number().nonnegative(),
  currency: z.string(),
  duration_unit: z.string(),
  duration_value: z.number().int().nonnegative(),
  total_amount: quota,
  allow_balance_pay: z.boolean(),
  allow_wallet_overflow: z.boolean().optional(),
  max_purchase_per_user: z.number().int().nonnegative(),
  upgrade_group: z.string().optional(),
  quota_reset_period: z.string().optional(),
});
export const gatewayProductSchema = z.object({
  id: z.number().int().positive(),
  name: z.string(),
  description: z.string(),
  price_amount: z.number().nonnegative(),
  currency: z.string(),
  quota_amount: quota,
  max_purchase_per_user: z.number().int().nonnegative(),
});

export const gatewayPaymentMethodsSchema = z.object({
  payment_methods: z.array(z.string()),
});

export type GatewayPaymentMethod = z.infer<typeof paymentMethodSchema>;
export type GatewayCheckoutInput = z.infer<typeof gatewayCheckoutSchema>;
export type GatewayCheckoutResult = z.infer<typeof gatewayCheckoutResultSchema>;
export type GatewayPlan = z.infer<typeof gatewayPlanSchema>;
export type GatewayProduct = z.infer<typeof gatewayProductSchema>;
