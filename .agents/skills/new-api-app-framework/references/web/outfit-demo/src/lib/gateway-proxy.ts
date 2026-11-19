import 'server-only';
import { createCipheriv, createDecipheriv, createHash, randomBytes } from 'node:crypto';
import { z } from 'zod';
import { config } from '@/lib/config';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { GatewayError, readGatewayError } from '@/lib/gateway-errors';

const accountSchema = z.object({
  user_id: z.string().uuid(), issuer: z.string(), app_id: z.number().int().positive(),
  client_id: z.string(), identity_source: z.literal('proxy'),
  gateway_user_id: z.number().int().positive().safe(), token_id: z.number().int().positive().safe(),
  encrypted_key: z.string().min(1),
});
type Account = z.infer<typeof accountSchema>;

function unavailable() { return new GatewayError(503, 'Proxy account service is unavailable.', 'PROXY_UNAVAILABLE'); }
function unauthorized() { return new GatewayError(401, 'Please sign in again.', 'AUTH_SESSION_EXPIRED'); }

export async function usesProxyMode(): Promise<boolean> {
  if (config.integrationMode === 'proxy') return true;
  if (config.integrationMode === 'oauth') return false;
  if (config.integrationMode !== 'dual') throw unavailable();
  const supabase = await createServerSupabase();
  const { data, error } = await supabase.auth.getUser();
  if (error || !data.user?.identities?.length) throw unauthorized();
  const identities = data.user.identities;
  const oauth = identities.some(identity => identity.provider === 'custom:newapi');
  if (oauth && identities.some(identity => identity.provider !== 'custom:newapi')) throw unauthorized();
  return !oauth;
}

async function bindSession(account: Account, sessionId: string): Promise<Account> {
  const admin = createAdminSupabase();
  const binding = { session_id: sessionId, user_id: account.user_id, issuer: account.issuer, app_id: account.app_id };
  const saved = await admin.from('gateway_proxy_sessions').upsert(binding, { onConflict: 'session_id', ignoreDuplicates: true });
  if (saved.error) throw unavailable();
  const canonical = await admin.from('gateway_proxy_sessions').select('user_id,issuer,app_id').eq('session_id', sessionId).single();
  if (canonical.error) throw unavailable();
  if (canonical.data.user_id !== account.user_id || canonical.data.issuer !== account.issuer || canonical.data.app_id !== account.app_id) throw unauthorized();
  if (process.env.INITIAL_ADMIN_USER_ID === account.user_id) {
    const initialized = await admin.rpc('bootstrap_proxy_owner', { p_user_id: account.user_id, p_client_id: account.client_id });
    if (initialized.error) throw unavailable();
  }
  return account;
}
function encryptionKey() {
  const encoded = process.env.GATEWAY_TOKEN_ENCRYPTION_KEY ?? '';
  const key = Buffer.from(encoded, 'base64');
  if (key.length !== 32 || key.toString('base64') !== encoded) throw unavailable();
  return key;
}
function aad(account: Pick<Account, 'user_id' | 'issuer' | 'app_id' | 'gateway_user_id'>) {
  return Buffer.from(JSON.stringify([account.user_id, account.issuer, account.app_id, account.gateway_user_id, 'proxy']));
}
function encrypt(key: string, account: Parameters<typeof aad>[0]) {
  const nonce = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', encryptionKey(), nonce);
  cipher.setAAD(aad(account));
  const bytes = Buffer.concat([cipher.update(key, 'utf8'), cipher.final()]);
  return Buffer.concat([nonce, cipher.getAuthTag(), bytes]).toString('base64');
}
function decrypt(account: Account) {
  try {
    const bytes = Buffer.from(account.encrypted_key, 'base64');
    const cipher = createDecipheriv('aes-256-gcm', encryptionKey(), bytes.subarray(0, 12));
    cipher.setAAD(aad(account));
    cipher.setAuthTag(bytes.subarray(12, 28));
    return Buffer.concat([cipher.update(bytes.subarray(28)), cipher.final()]).toString('utf8');
  } catch { throw unavailable(); }
}

export async function adminRequest(path: string, method = 'GET', body?: unknown, requestId?: string) {
  const token = process.env.GATEWAY_ADMIN_ACCESS_TOKEN ?? '';
  if (!token) throw unavailable();
  try {
    const response = await fetch(`${config.gatewayBaseUrl}${path}`, {
      method, cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(15_000),
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', ...(requestId ? { 'Idempotency-Key': requestId } : {}) },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) throw await readGatewayError(response);
    return response;
  } catch (error) {
    if (error instanceof GatewayError) throw error;
    throw unavailable();
  }
}

/** Verify the exact signed cookie token before using any decoded claims. */
export async function getProxyAccount(): Promise<Account> {
  const appId = config.gatewayAppId;
  const issuer = `${config.supabaseUrl.replace(/\/$/, '')}/auth/v1`;
  if (!Number.isSafeInteger(appId) || appId <= 0 || !config.gatewayClientId || !issuer.startsWith('https://')) throw unavailable();
  const supabase = await createServerSupabase();
  const { data, error } = await supabase.auth.getSession();
  if (error || !data.session) throw unauthorized();
  const verified = await supabase.auth.getUser(data.session.access_token);
  const user = verified.data.user;
  if (verified.error || !user || user.id !== data.session.user.id || !user.identities?.length ||
    user.identities.some(identity => !['email', 'phone'].includes(identity.provider))) throw unauthorized();
  let claims: { sub: string; session_id: string; iss: string };
  try {
    claims = z.object({ sub: z.string().uuid(), session_id: z.string().uuid(), iss: z.literal(issuer) })
      .parse(JSON.parse(Buffer.from(data.session.access_token.split('.')[1], 'base64url').toString('utf8')));
  } catch { throw unauthorized(); }
  if (claims.sub !== user.id) throw unauthorized();
  const admin = createAdminSupabase();
  const active = await admin.rpc('gateway_proxy_session_active', { p_session_id: claims.session_id, p_user_id: user.id });
  if (active.error) throw unavailable();
  if (active.data !== true) throw unauthorized();
  const existing = await admin.from('gateway_proxy_accounts').select('*')
    .eq('user_id', user.id).eq('app_id', appId).eq('issuer', issuer).maybeSingle();
  if (existing.error) throw unavailable();
  if (existing.data) {
    const account = accountSchema.parse(existing.data);
    if (account.client_id !== config.gatewayClientId) throw unauthorized();
    return bindSession(account, claims.session_id);
  }
  const provision = await (await adminRequest('/api/app-proxy/users', 'POST', { app_id: appId, issuer, subject: user.id })).json();
  const identity = z.object({ success: z.literal(true), data: z.object({ user_id: z.number().int().positive().safe(), app_id: z.literal(appId), identity_source: z.literal('proxy'), status: z.literal(1) }) }).safeParse(provision);
  if (!identity.success) throw unavailable();
  const operation = createHash('sha256').update(JSON.stringify([issuer, user.id, appId, 'initial-key-v1'])).digest('hex');
  const keyResponse = await (await adminRequest(`/api/app-proxy/users/${identity.data.data.user_id}/keys?app_id=${appId}`, 'POST', {
    name: 'Hosted App key', expired_time: -1, unlimited_quota: true,
  }, operation)).json();
  const key = z.object({ success: z.literal(true), data: z.object({ id: z.number().int().positive().safe(), key: z.string().min(1), app_id: z.literal(appId), user_id: z.literal(identity.data.data.user_id) }) }).safeParse(keyResponse);
  if (!key.success) throw unavailable();
  const account = { user_id: user.id, issuer, app_id: appId, client_id: config.gatewayClientId, identity_source: 'proxy' as const, gateway_user_id: identity.data.data.user_id, token_id: key.data.data.id };
  const saved = await admin.from('gateway_proxy_accounts').upsert({ ...account, encrypted_key: encrypt(key.data.data.key, account) }, { onConflict: 'user_id,issuer,app_id', ignoreDuplicates: true });
  if (saved.error) throw unavailable();
  const canonical = await admin.from('gateway_proxy_accounts').select('*').eq('user_id', user.id).eq('issuer', issuer).eq('app_id', appId).single();
  if (canonical.error) throw unavailable();
  return bindSession(accountSchema.parse(canonical.data), claims.session_id);
}

export async function getProxyAIKey() {
  const key = decrypt(await getProxyAccount());
  return key.startsWith('sk-') ? key : `sk-${key}`;
}

/** Fixed management allowlist; browser input never selects user, App or source. */
export async function proxyManagementRequest(path: string, method: string, body?: string, query?: Record<string, string | number | undefined>, requestId?: string): Promise<Response> {
  const account = await getProxyAccount();
  const routes: Record<string, string> = {
    '/oauth/userinfo': 'wallet', '/oauth/wallet': 'wallet', '/oauth/pricing': 'pricing',
    '/oauth/products': 'catalog', '/oauth/payment/methods': 'catalog', '/oauth/logs': 'logs',
    '/oauth/orders': 'orders', '/oauth/topup/preview': 'topup-preview',
  };
  let endpoint = routes[path];
  const orderMatch = /^\/oauth\/orders\/([A-Za-z0-9_-]{1,128})$/.exec(path);
  if (orderMatch) endpoint = `orders/${orderMatch[1]}`;
  let payload: Record<string, unknown> | undefined;
  if (path === '/oauth/subscription/plans') return Response.json({ plans: [] });
  if (path === '/oauth/subscription/checkout') throw new GatewayError(400, 'Proxy mode does not support subscriptions.', 'SUBSCRIPTION_UNSUPPORTED');
  if (path === '/oauth/topup/checkout' || path === '/oauth/products/checkout') {
    let input: { amount?: number; product_id?: number; payment_method: 'waffo_pancake' };
    try {
      const schema = path === '/oauth/topup/checkout'
        ? z.object({ amount: z.number().int().positive().max(10000), payment_method: z.literal('waffo_pancake') }).strict()
        : z.object({ product_id: z.number().int().positive().safe(), payment_method: z.literal('waffo_pancake') }).strict();
      input = schema.parse(JSON.parse(body ?? '{}'));
    } catch { throw new GatewayError(400, 'Invalid checkout input or payment method.', 'INVALID_BODY'); }
    if (!requestId || !z.string().uuid().safeParse(requestId).success) throw new GatewayError(400, 'A checkout request ID is required.', 'INVALID_REQUEST_ID');
    endpoint = 'checkout';
    payload = { ...input, app_id: account.app_id, purchase_type: path === '/oauth/topup/checkout' ? 'topup' : 'product' };
    const hash = createHash('sha256').update(JSON.stringify(payload)).digest('hex');
    const admin = createAdminSupabase();
    const saved = await admin.from('gateway_checkout_requests').upsert({ user_id: account.user_id, app_id: account.app_id, request_id: requestId, input_hash: hash }, { onConflict: 'user_id,app_id,request_id', ignoreDuplicates: true });
    if (saved.error) throw unavailable();
    const original = await admin.from('gateway_checkout_requests').select('input_hash').eq('user_id', account.user_id).eq('app_id', account.app_id).eq('request_id', requestId).single();
    if (original.error) throw unavailable();
    if (original.data.input_hash !== hash) throw new GatewayError(409, 'Checkout request changed.', 'REQUEST_CONFLICT');
  }
  if (path === '/oauth/topup/preview') {
    try {
      const input = z.object({ amount: z.number().int().positive().max(10000), payment_method: z.literal('waffo_pancake') }).strict().parse(JSON.parse(body ?? '{}'));
      payload = { ...input, app_id: account.app_id };
    } catch { throw new GatewayError(400, 'Invalid top-up preview.', 'INVALID_BODY'); }
  }
  if (!endpoint || !['GET', 'POST'].includes(method)) throw new GatewayError(400, 'Unsupported proxy operation.', 'UNSUPPORTED');
  const params = new URLSearchParams({ app_id: String(account.app_id) });
  for (const name of ['page', 'page_size', 'keyword']) if (query?.[name] !== undefined) params.set(name === 'page' && endpoint === 'orders' ? 'p' : name, String(query[name]));
  const response = await adminRequest(`/api/app-proxy/users/${account.gateway_user_id}/${endpoint}?${params}`, method, payload, requestId);
  if (endpoint === 'wallet') {
    const value = await response.json();
    if (!value.success || !value.data) throw unavailable();
    return Response.json({ ...value.data, id: String(account.gateway_user_id), app_quota: value.data.quota, app_used_quota: value.data.used_quota, subscription: null });
  }
  if (endpoint === 'checkout') {
    const value = await response.json();
    if (typeof value.order_id === 'string') {
      const saved = await createAdminSupabase().from('gateway_checkout_requests').update({ order_id: value.order_id })
        .eq('user_id', account.user_id).eq('app_id', account.app_id).eq('request_id', requestId!);
      if (saved.error) throw unavailable();
    }
    return Response.json(value, { status: response.status });
  }
  return response;
}
