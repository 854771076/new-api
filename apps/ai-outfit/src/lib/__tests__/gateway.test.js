import { afterAll, afterEach, beforeEach, expect, mock, spyOn, test } from 'bun:test';
import * as dns from 'node:dns/promises';
import * as https from 'node:https';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import sharp from 'sharp';

// Mock only external Auth/DB/network/cookie boundaries, not gateway production code.
mock.module('server-only', () => ({}));
mock.module('next/headers', () => ({ cookies: async () => ({ getAll: () => [], set() {}, delete() {} }) }));
mock.module('node:timers/promises', () => ({ setTimeout: async () => {} }));

const testEnv = {
  NEXT_PUBLIC_SUPABASE_URL: 'https://supabase.example',
  NEXT_PUBLIC_SUPABASE_ANON_KEY: 'test-anon',
  SUPABASE_SECRET_KEY: 'test-service-role',
  NEXT_PUBLIC_GATEWAY_BASE_URL: 'https://gateway.example',
  NEXT_PUBLIC_GATEWAY_CLIENT_ID: 'nac_outfit',
  APP_ORIGIN: 'https://outfit.example',
  GATEWAY_TOKEN_ENCRYPTION_KEY: Buffer.alloc(32, 7).toString('base64'),
  GATEWAY_CLIENT_SECRET: '',
  NODE_ENV: 'test',
};
const originalEnv = Object.fromEntries(Object.keys(testEnv).map(key => [key, process.env[key]]));
Object.assign(process.env, testEnv);
const originalFetch = globalThis.fetch;

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const sessionId = 'bb2ed7f9-b315-447c-9389-fb40b2ca13e4';
function jwt(claims) {
  return `${Buffer.from('{}').toString('base64url')}.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.verified-at-auth-boundary`;
}
function providerToken(exp, jti = 'original') {
  return jwt({ sub: '42', client_id: 'nac_outfit', exp, jti });
}
const supabaseToken = jwt({ sub: userId, session_id: sessionId });
let user;
let session;
let row;
let signouts;
let authCalls;
let onLockedRead;
let rejectSave;
let authFailure;
let callbackMode;
let exchanges;
let sanitizedCredentials;
let rejectSanitization;
let rejectInsert;
let databaseTime;
const databaseNow = () => databaseTime ?? Date.now();

const auth = {
  async getUser(token) {
    authCalls++;
    if (callbackMode && !token) return { data: { user: null }, error: null };
    if (authFailure || (token && token !== supabaseToken)) return { data: { user: null }, error: { message: 'secret diagnostic' } };
    return { data: { user }, error: null };
  },
  async getSession() { return { data: { session }, error: null }; },
  async signOut() { signouts++; return { error: null }; },
  async exchangeCodeForSession() { exchanges++; return { data: { session, user }, error: null }; },
  async setSession(credentials) {
    sanitizedCredentials = credentials;
    return { data: { session: rejectSanitization ? null : credentials }, error: rejectSanitization ? { message: 'secret sanitizer failure' } : null };
  },
};
mock.module('@supabase/ssr', () => ({ createServerClient: () => ({ auth }) }));

function query() {
  const filters = [];
  let action = 'read';
  let input;
  const builder = {
    select() { return builder; },
    eq(key, value) { filters.push([key, value]); return builder; },
    upsert(value) { action = 'insert'; input = value; return builder; },
    delete() { action = 'delete'; return builder; },
    abortSignal() { return builder; },
    maybeSingle() { return execute(); },
    then(resolve, reject) { return execute().then(resolve, reject); },
  };
  async function execute() {
    if (action === 'insert') {
      if (!row) row = { ...input, version: 0, lease_owner: null, lease_until: null };
      // Simulate an ambiguous insert that reached the DB before its response failed.
      return { error: rejectInsert ? { message: 'secret database detail' } : null, data: null };
    }
    const matches = row && filters.every(([key, value]) => row[key] === value);
    if (action === 'delete') {
      if (matches) row = null;
      return { error: null, data: null };
    }
    if (matches && row.lease_owner && onLockedRead) onLockedRead();
    return { error: null, data: matches ? { ...row } : null };
  }
  return builder;
}
const database = {
  from: () => query(),
  rpc(name, input) {
    let data = [];
    const matches = row && row.session_id === input.p_session_id && row.user_id === input.p_user_id && row.client_id === input.p_client_id;
    if (name === 'gateway_session_acquire' && matches && !row.lease_owner && row.version === input.p_version) {
      row = { ...row, lease_owner: input.p_owner, lease_until: new Date(databaseNow() + 30_000).toISOString() };
      data = [{ ...row }];
    }
    if (name === 'gateway_session_expire' && matches && row.lease_owner === input.p_owner && Date.parse(row.lease_until) <= databaseNow()) {
      data = [{ ...row }];
      row = null;
    }
    if (name === 'gateway_session_rotate' && matches && row && row.lease_owner === input.p_owner && Date.parse(row.lease_until) > databaseNow() && rejectSave !== 'before') {
      row = { ...row, encrypted_pair: input.p_encrypted_pair, version: row.version + 1, lease_owner: null, lease_until: null };
      data = [{ ...row }];
    }
    return { abortSignal: async () => ({ data, error: name === 'gateway_session_rotate' && rejectSave === 'after' ? { message: 'secret response failure after commit' } : null }) };
  },
};
mock.module('@supabase/supabase-js', () => ({ createClient: () => database }));

const { NextRequest } = await import('next/server');
const { persistGatewaySession, getGatewayAccessToken } = await import('../gateway-session');
const { fetchGatewayCatalog, fetchGatewayUserinfo, fetchGatewayCheckout, generateImage, GatewayError } = await import('../gateway');
const { readGatewayError } = await import('../gateway-errors');
const { POST: topup } = await import('../../app/api/gateway/topup/route');
const { POST: checkout } = await import('../../app/api/gateway/checkout/route');
const { POST: logout } = await import('../../app/api/auth/logout/route');
const { GET: logs } = await import('../../app/api/gateway/logs/route');
const { GET: callback } = await import('../../app/auth/callback/route');

function request(path, body, origin = 'https://outfit.example') {
  return new NextRequest(`https://outfit.example${path}`, {
    method: 'POST', headers: { ...(origin === null ? {} : { origin }), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}
function jsonFetch(payload, status = 200) {
  globalThis.fetch = mock(async () => Response.json(payload, { status }));
  return globalThis.fetch;
}

beforeEach(() => {
  process.env.NODE_ENV = 'test';
  process.env.GATEWAY_TOKEN_ENCRYPTION_KEY = Buffer.alloc(32, 7).toString('base64');
  user = { id: userId, app_metadata: {}, user_metadata: { aud: ['evil-client'] }, identities: [
    { provider: 'custom:newapi', identity_data: { sub: '42', aud: ['nac_outfit'] } },
  ] };
  session = { access_token: supabaseToken, refresh_token: 'supabase-refresh', user,
    provider_token: providerToken(Math.floor(Date.now() / 1000) + 3600), provider_refresh_token: 'provider-refresh-original' };
  row = null;
  signouts = 0;
  authCalls = 0;
  authFailure = false;
  callbackMode = false;
  exchanges = 0;
  sanitizedCredentials = null;
  onLockedRead = null;
  rejectSave = false;
  rejectSanitization = false;
  rejectInsert = false;
  databaseTime = null;
  globalThis.fetch = mock(async () => { throw new Error('Unexpected external request'); });
});
afterEach(() => { mock.restore(); globalThis.fetch = originalFetch; });
afterAll(() => {
  for (const [key, value] of Object.entries(originalEnv)) {
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
});

test('callback exchanges once, encrypts the provider pair, and strips provider tokens from the cookie session', async () => {
  callbackMode = true;
  const response = await callback(new NextRequest('https://outfit.example/auth/callback?code=one-use-code'));
  expect(response.headers.get('location')).toBe('https://outfit.example/studio');
  expect(exchanges).toBe(1);
  expect(row.encrypted_pair).not.toContain(session.provider_refresh_token);
  expect(sanitizedCredentials).toEqual({ access_token: supabaseToken, refresh_token: 'supabase-refresh' });
  callbackMode = false;
  await callback(new NextRequest('https://outfit.example/auth/callback?code=one-use-code'));
  expect(exchanges).toBe(1);
});

test.each(['sanitize', 'persist'])('callback %s failure revokes the verified exchange pair and removes any ambiguous vault', async failure => {
  callbackMode = true;
  rejectSanitization = failure === 'sanitize';
  rejectInsert = failure === 'persist';
  const upstream = jsonFetch({});
  const response = await callback(new NextRequest('https://outfit.example/auth/callback?code=one-use-code'));
  expect(response.headers.get('location')).toBe('https://outfit.example/login?error=oauth_failed');
  expect(exchanges).toBe(1);
  expect(row).toBeNull();
  expect(signouts).toBe(1);
  expect(new URL(upstream.mock.calls[0][0]).pathname).toBe('/oauth/revoke');
  expect(new URLSearchParams(upstream.mock.calls[0][1].body).get('token')).toBe('provider-refresh-original');
});

test('vault is keyed by validated Supabase session and trusted provider audience, not editable user_metadata', async () => {
  await persistGatewaySession(session, user);
  expect(await getGatewayAccessToken()).toBe(session.provider_token);
  user.identities[0].identity_data.aud = ['different-app'];
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401 });
  user.identities[0].identity_data.aud = ['nac_outfit'];
  session.access_token = jwt({ sub: userId, session_id: '4f49ff46-b91d-4ae4-8e94-329b2d7b6654' });
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401 });
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test.each(['missing', 'ambiguous', 'malformed'])('vault rejects %s protected identities without an app-metadata fallback', async kind => {
  await persistGatewaySession(session, user);
  user.app_metadata.client_id = 'nac_outfit';
  if (kind === 'missing') user.identities = [];
  if (kind === 'ambiguous') user.identities.push({ ...user.identities[0] });
  if (kind === 'malformed') user.identities[0].identity_data.aud = 'nac_outfit';
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401, code: 'AUTH_SESSION_EXPIRED' });
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test.each([undefined, 'not-base64', Buffer.alloc(31).toString('base64')])('missing or invalid encryption key never falls back to public credentials', async key => {
  if (key === undefined) delete process.env.GATEWAY_TOKEN_ENCRYPTION_KEY;
  else process.env.GATEWAY_TOKEN_ENCRYPTION_KEY = key;
  await expect(persistGatewaySession(session, user)).rejects.toMatchObject({ status: 503 });
  expect(row).toBeNull();
});

test('a missing vault clears the Supabase session so re-login cannot loop back to studio', async () => {
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401, code: 'AUTH_SESSION_EXPIRED' });
  expect(signouts).toBe(1);
});

test('AES-GCM authentication rejects a tampered vault instead of using cookie provider tokens', async () => {
  await persistGatewaySession(session, user);
  const bytes = Buffer.from(row.encrypted_pair, 'base64');
  bytes[12] ^= 1; // Corrupt the authentication tag.
  row.encrypted_pair = bytes.toString('base64');
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 503 });
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test.each([1, 10_000])('topup accepts integer boundary %s and sends payment_method, not channel or app_id', async amount => {
  await persistGatewaySession(session, user);
  const upstream = jsonFetch({ status: 'success', payment_method: 'balance', quota: 500_000 });
  const response = await topup(request('/api/gateway/topup', { amount, payment_method: 'balance' }));
  expect(await response.json()).toEqual({ success: true, message: '', data: { status: 'success', payment_method: 'balance', quota: 500_000 }, error_code: null });
  expect(JSON.parse(upstream.mock.calls[0][1].body)).toEqual({ amount, payment_method: 'balance' });
});

test.each([0, -1, 10_001, 1.5, '10', null])('topup rejects invalid amount %s before upstream billing', async amount => {
  const response = await topup(request('/api/gateway/topup', { amount, payment_method: 'balance' }));
  expect(response.status).toBe(400);
  expect((await response.json()).error_code).toBe('BAD_REQUEST_INVALID_BODY');
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test.each([
  { amount: 10, payment_method: 'stripe' },
  { amount: 10, channel: 'balance' },
  { amount: 10, payment_method: 'balance', app_id: 99 },
])('topup rejects unsupported method and untrusted app selection', async body => {
  expect((await topup(request('/api/gateway/topup', body))).status).toBe(400);
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test.each(['https://attacker.example', null, 'null', 'https://outfit.example.attacker.example'])('checkout and logout reject CSRF origin %s before Auth or network', async origin => {
  const purchase = await checkout(request('/api/gateway/checkout', { kind: 'plan', id: 1, payment_method: 'balance' }, origin));
  const signedOut = await logout(request('/api/auth/logout', {}, origin));
  expect(purchase.status).toBe(403);
  expect(signedOut.status).toBe(403);
  expect(authCalls).toBe(0);
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('production requires APP_ORIGIN to match request URL even when supplied Origin matches a spoofed host', async () => {
  process.env.NODE_ENV = 'production';
  const response = await checkout(new NextRequest('https://spoofed.example/api/gateway/checkout', {
    method: 'POST', headers: { origin: 'https://spoofed.example' }, body: '{}',
  }));
  expect(response.status).toBe(403);
  expect(authCalls).toBe(0);
});

test.each([
  ['plan', '/oauth/subscription/checkout', { plan_id: 5, payment_method: 'balance' }, { status: 'success', payment_method: 'balance' }],
  ['product', '/oauth/products/checkout', { product_id: 5, payment_method: 'balance' }, { status: 'success', order_id: 'order-5', quota_amount: 20 }],
])('%s checkout maps id to the correct OAuth contract', async (kind, path, body, result) => {
  await persistGatewaySession(session, user);
  const upstream = jsonFetch(result);
  const response = await checkout(request('/api/gateway/checkout', { kind, id: 5, payment_method: 'balance' }));
  expect(response.status).toBe(200);
  expect((await response.json()).data).toEqual({ ...result, payment_method: 'balance' });
  expect(new URL(upstream.mock.calls[0][0]).pathname).toBe(path);
  expect(JSON.parse(upstream.mock.calls[0][1].body)).toEqual(body);
});

test.each([
  { kind: 'plan', id: 0, payment_method: 'balance' },
  { kind: 'plan', id: -1, payment_method: 'balance' },
  { kind: 'product', id: 1.5, payment_method: 'balance' },
  { kind: 'product', id: '5', payment_method: 'balance' },
  { kind: 'product', id: Number.MAX_SAFE_INTEGER + 1, payment_method: 'balance' },
  { kind: 'unknown', id: 5, payment_method: 'balance' },
  { kind: 'plan', id: 5 },
  { kind: 'plan', id: 5, payment_method: 'balance', app_id: 99 },
])('checkout rejects invalid identifiers, missing payment, and injected app selection before Auth', async body => {
  const response = await checkout(request('/api/gateway/checkout', body));
  expect(response.status).toBe(400);
  expect((await response.json()).error_code).toBe('BAD_REQUEST_INVALID_BODY');
  expect(authCalls).toBe(0);
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('pending checkout exposes only a validated HTTPS payment link and safe order fields', async () => {
  await persistGatewaySession(session, user);
  jsonFetch({ status: 'pending', checkout_url: 'https://pay.example/order', order_id: 'order-1', expires_at: '2026-11-11T00:00:00Z', token: 'must-not-leak' });
  const result = await fetchGatewayCheckout({ kind: 'product', id: 1, payment_method: 'waffo_pancake' });
  expect(result).toEqual({ status: 'pending', payment_method: 'waffo_pancake', checkout_url: 'https://pay.example/order', order_id: 'order-1', expires_at: '2026-11-11T00:00:00Z' });
});

test.each([
  { status: 'pending' },
  { status: 'pending', checkout_url: 'javascript:alert(1)' },
  { status: 'success', payment_method: 'waffo_pancake' },
  { status: 'success', quota_amount: -1 },
])('malformed or inconsistent checkout results fail closed', async result => {
  await persistGatewaySession(session, user);
  jsonFetch(result);
  await expect(fetchGatewayCheckout({ kind: 'product', id: 1, payment_method: 'balance' })).rejects.toMatchObject({ status: 502 });
});

test('OAuth checkout errors map to safe Chinese reasons and never leak arbitrary error bodies', async () => {
  await persistGatewaySession(session, user);
  jsonFetch({ error: 'checkout_failed', error_description: 'Insufficient balance' }, 400);
  const response = await checkout(request('/api/gateway/checkout', { kind: 'plan', id: 1, payment_method: 'balance' }));
  expect(await response.json()).toMatchObject({ success: false, error_code: 'UPSTREAM_CHECKOUT_FAILED' });

  const cases = [
    { desc: 'Waffo Pancake configuration is incomplete', code: 'UPSTREAM_PAYMENT_NOT_CONFIGURED' },
    { desc: 'Top-up amount cannot be less than 10', code: 'UPSTREAM_CHECKOUT_AMOUNT_INVALID' },
    { desc: 'Payment, redemption, subscription features are disabled until compliance confirmed', code: 'UPSTREAM_PAYMENT_DISABLED' },
    { desc: 'product is not enabled', code: 'UPSTREAM_CHECKOUT_UNAVAILABLE' },
  ];
  for (const c of cases) {
    jsonFetch({ error: 'checkout_failed', error_description: c.desc }, 400);
    await expect(fetchGatewayCheckout({ kind: 'product', id: 1, payment_method: 'waffo_pancake' }))
      .rejects.toMatchObject({ status: 400, code: c.code });
  }

  // Unrecognized descriptions fall back to a generic message and never surface raw upstream text.
  jsonFetch({ error: 'checkout_failed', error_description: 'secret-token-must-not-leak' }, 400);
  const leaked = await fetchGatewayCheckout({ kind: 'plan', id: 1, payment_method: 'balance' }).catch(e => e);
  expect(leaked).toBeInstanceOf(GatewayError);
  expect(leaked.message).not.toContain('secret-token-must-not-leak');
  globalThis.fetch = mock(async () => new Response('<html>secret credentials</html>', { status: 502 }));
  await expect(fetchGatewayCheckout({ kind: 'plan', id: 1, payment_method: 'balance' })).rejects.toBeInstanceOf(GatewayError);
});

test('relay nested errors from /v1/image generation surface a readable Chinese reason', async () => {
  const relay = await readGatewayError(new Response(
    JSON.stringify({ error: { code: 'model_not_found', message: 'No available channel for model gpt-image-2 under group', type: 'new_api_error' } }),
    { status: 404, headers: { 'Content-Type': 'application/json' } },
  ));
  expect(relay).toMatchObject({ status: 404, code: 'UPSTREAM_RELAY_MODEL_NOT_FOUND' });
  // The raw upstream message (which may carry internal request ids) is not forwarded.
  expect(relay.message).not.toContain('No available channel');

  const unknown = await readGatewayError(new Response(
    JSON.stringify({ error: { code: 'some_unknown_relay_code', message: 'internal host https://10.0.0.5 detail' } }),
    { status: 500, headers: { 'Content-Type': 'application/json' } },
  ));
  expect(unknown).toBeInstanceOf(GatewayError);
  expect(unknown.message).not.toContain('10.0.0.5');

  // Non-JSON gateway error pages fall back to a safe status-based message, never the HTML body.
  const html = await readGatewayError(new Response('<html>secret credentials</html>', { status: 502 }));
  expect(html).toBeInstanceOf(GatewayError);
  expect(html.message).not.toContain('secret credentials');
});

test('relay codes with corrected keys and empty/null codes map by reason or status', async () => {
  const cases = [
    { code: 'channel:no_available_key', codeToken: 'UPSTREAM_RELAY_CHANNEL:NO_AVAILABLE_KEY', hint: '模型渠道暂不可用' },
    { code: 'channel:response_time_exceeded', codeToken: 'UPSTREAM_RELAY_CHANNEL:RESPONSE_TIME_EXCEEDED', hint: '模型响应超时' },
    { code: 'insufficient_user_quota', codeToken: 'UPSTREAM_RELAY_INSUFFICIENT_USER_QUOTA', hint: '网关额度不足' },
    { code: 'model_price_error', codeToken: 'UPSTREAM_RELAY_MODEL_PRICE_ERROR', hint: '模型价格配置有误' },
  ];
  for (const c of cases) {
    const err = await readGatewayError(new Response(
      JSON.stringify({ error: { code: c.code, message: 'internal 10.0.0.5 detail', type: 'new_api_error' } }),
      { status: 400, headers: { 'Content-Type': 'application/json' } },
    ));
    expect(err).toMatchObject({ status: 400, code: c.codeToken });
    expect(err.message).toContain(c.hint);
    expect(err.message).not.toContain('10.0.0.5');
  }

  // code: null (upstream passthrough) must not be mistaken for a relay error.
  const nullCode = await readGatewayError(new Response(
    JSON.stringify({ error: { code: null, message: 'internal host detail', type: 'new_api_error' } }),
    { status: 400, headers: { 'Content-Type': 'application/json' } },
  ));
  expect(nullCode).toBeInstanceOf(GatewayError);
  expect(nullCode.message).toBe('请求参数有误，请检查后重试。');
  expect(nullCode.message).not.toContain('生成请求被网关拒绝');

  // Unknown 4xx falls back to an explicit HTTP status in the message.
  const unknown4xx = await readGatewayError(new Response('{}', { status: 422 }));
  expect(unknown4xx.message).toContain('HTTP 422');

  // 413 maps to an oversized-upload hint.
  const tooLarge = await readGatewayError(new Response('<html>413</html>', { status: 413 }));
  expect(tooLarge.message).toContain('素材过大');
});

test('catalog unwraps plan wrappers and account responses explicitly include app quota', async () => {
  await persistGatewaySession(session, user);
  const plan = { id: 1, app_id: 3, title: 'Outfit plan', price_amount: 1, currency: 'USD', duration_unit: 'month', duration_value: 1, total_amount: 0, allow_balance_pay: true, max_purchase_per_user: 0 };
  const product = { id: 2, name: 'Image credits', description: '', price_amount: 1, currency: 'USD', quota_amount: 500_000, max_purchase_per_user: 0 };
  globalThis.fetch = mock(async url => {
    const path = new URL(url).pathname;
    if (path === '/oauth/subscription/plans') return Response.json({ plans: [{ plan }] });
    if (path === '/oauth/products') return Response.json({ products: [product] });
    return Response.json({ sub: '42', quota: 100, used_quota: 20, app_quota: 4, app_used_quota: 2 });
  });
  expect(await fetchGatewayCatalog()).toEqual({ plans: [plan], products: [product] });
  expect(await fetchGatewayUserinfo()).toMatchObject({ app_quota: 4, app_used_quota: 2, quota: 100 });
});

test('logs BFF validates pagination, maps gateway app_id, and never sends a client app filter', async () => {
  const invalid = await logs(new NextRequest('https://outfit.example/api/gateway/logs?page_size=101'));
  expect(invalid.status).toBe(400);
  expect(authCalls).toBe(0);
  await persistGatewaySession(session, user);
  const upstream = jsonFetch({ items: [{ id: 1, model_name: 'outfit', quota: 11, prompt_tokens: 0, completion_tokens: 7, created_at: 0,
    app_id: 7, token: 'secret provider token', content: 'secret upstream diagnostics' }], total: 1 });
  const response = await logs(new NextRequest('https://outfit.example/api/gateway/logs?app_id=99'));
  const body = await response.json();
  expect(body).toMatchObject({ success: true, message: '', error_code: null, data: { total: 1, page: 1, page_size: 20 } });
  expect(body.data.data[0]).toEqual({ id: 1, model: 'outfit', quota: 11, prompt_tokens: 0, completion_tokens: 7,
    created_at: '1970-01-01T00:00:00.000Z', app_id: 7 });
  expect(JSON.stringify(body)).not.toContain('secret');
  // App scoping is enforced server-side from the OAuth token; the BFF forwards
  // only pagination and drops any client-supplied app_id.
  const requestUrl = new URL(upstream.mock.calls[0][0]);
  expect(requestUrl.pathname).toBe('/oauth/logs');
  expect(requestUrl.searchParams.get('app_id')).toBeNull();
});

test('simultaneous refreshes share the database lease and persist only the rotated pair', async () => {
  session.provider_token = providerToken(Math.floor(Date.now() / 1000) - 1);
  await persistGatewaySession(session, user);
  const nextToken = providerToken(Math.floor(Date.now() / 1000) + 3600, 'rotated');
  let resolveRefresh;
  const refresh = new Promise(resolve => { resolveRefresh = resolve; });
  onLockedRead = () => resolveRefresh(Response.json({ access_token: nextToken, refresh_token: 'refresh-rotated' }));
  const upstream = globalThis.fetch = mock(async () => refresh);
  const [first, second] = await Promise.all([getGatewayAccessToken(), getGatewayAccessToken()]);
  expect([first, second]).toEqual([nextToken, nextToken]);
  expect(upstream).toHaveBeenCalledTimes(1);
  expect(row.version).toBe(1);
  expect(row.lease_owner).toBeNull();
  expect(await getGatewayAccessToken()).toBe(nextToken);
});

test('401 with an explicit server token refreshes exactly once and concurrent force refresh reuses new token', async () => {
  await persistGatewaySession(session, user);
  const oldToken = session.provider_token;
  const nextToken = providerToken(Math.floor(Date.now() / 1000) + 3600, '401-rotation');
  globalThis.fetch = mock(async (url) => {
    if (new URL(url).pathname === '/oauth/token') return Response.json({ access_token: nextToken, refresh_token: 'refresh-rotated' });
    return Response.json({ error: 'invalid_token' }, { status: 401 });
  });
  await expect(fetchGatewayUserinfo({ accessToken: oldToken })).rejects.toMatchObject({ status: 401 });
  expect(globalThis.fetch).toHaveBeenCalledTimes(3); // API + refresh + one API retry.
  expect(await getGatewayAccessToken({ force: true, expectedToken: oldToken })).toBe(nextToken);
  expect(globalThis.fetch).toHaveBeenCalledTimes(3);
});

test('invalid_grant deletes the vault, signs out, and never resurrects old cookie provider tokens', async () => {
  session.provider_token = providerToken(Math.floor(Date.now() / 1000) - 1);
  await persistGatewaySession(session, user);
  const upstream = jsonFetch({ error: 'invalid_grant', error_description: 'secret reason' }, 400);
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401 });
  expect(row).toBeNull();
  expect(signouts).toBe(1);
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401 });
  expect(upstream).toHaveBeenCalledTimes(1);
});

test.each(['network', 'save', 'committed-save'])('uncertain refresh %s failures return 503 and never replay the old refresh token', async failure => {
  session.provider_token = providerToken(Math.floor(Date.now() / 1000) - 1);
  await persistGatewaySession(session, user);
  rejectSave = failure === 'save' ? 'before' : failure === 'committed-save' ? 'after' : false;
  const upstream = failure !== 'network'
    ? jsonFetch({ access_token: providerToken(Math.floor(Date.now() / 1000) + 3600, 'unsaved'), refresh_token: 'refresh-unsaved' })
    : globalThis.fetch = mock(async () => { throw new Error('secret transport diagnostic'); });
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 503 });
  expect(row).toBeNull();
  expect(signouts).toBe(1);
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401 });
  expect(upstream).toHaveBeenCalledTimes(1);
});

test('active lease contention times out with 503 without replaying tokens', async () => {
  await persistGatewaySession(session, user);
  const now = Date.now();
  row.lease_owner = '232402b8-5678-40da-a2cb-3b92b6d403c4';
  row.lease_until = new Date(now + 30_000).toISOString();
  let current = now;
  spyOn(Date, 'now').mockImplementation(() => current);
  onLockedRead = () => { current += 12_001; };
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 503 });
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('expired worker lease fails closed rather than replaying a possibly rotated token', async () => {
  await persistGatewaySession(session, user);
  row.lease_owner = '232402b8-5678-40da-a2cb-3b92b6d403c4';
  row.lease_until = new Date(Date.now() - 1).toISOString();
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401 });
  expect(row).toBeNull();
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('worker clock skew cannot delete a lease still active by database time', async () => {
  await persistGatewaySession(session, user);
  const now = Date.now();
  databaseTime = now;
  row.lease_owner = '232402b8-5678-40da-a2cb-3b92b6d403c4';
  row.lease_until = new Date(now + 30_000).toISOString();
  spyOn(Date, 'now').mockReturnValue(now + 60_000);
  let reads = 0;
  onLockedRead = () => {
    if (++reads === 2) row = { ...row, version: 1, lease_owner: null, lease_until: null };
  };
  expect(await getGatewayAccessToken()).toBe(session.provider_token);
  expect(row).not.toBeNull();
  expect(signouts).toBe(0);
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('logout racing refresh waits for rotation and revokes only the current pair', async () => {
  session.provider_token = providerToken(Math.floor(Date.now() / 1000) - 1);
  await persistGatewaySession(session, user);
  const nextToken = providerToken(Math.floor(Date.now() / 1000) + 3600, 'logout-race');
  let resolveRefresh;
  const refreshing = new Promise(resolve => { resolveRefresh = resolve; });
  onLockedRead = () => resolveRefresh(Response.json({ access_token: nextToken, refresh_token: 'refresh-latest' }));
  const revokedTokens = [];
  globalThis.fetch = mock(async (url, init) => {
    if (new URL(url).pathname === '/oauth/token') return refreshing;
    revokedTokens.push(new URLSearchParams(init.body).get('token'));
    return Response.json({});
  });
  const [token, response] = await Promise.all([getGatewayAccessToken(), logout(request('/api/auth/logout', {}))]);
  expect(token).toBe(nextToken);
  expect(response.status).toBe(200);
  expect(revokedTokens).toEqual(['refresh-latest']);
  expect(row).toBeNull();
  expect(signouts).toBe(1);
});

test('failed upstream revocation still deletes the vault and signs out with a safe explicit error', async () => {
  await persistGatewaySession(session, user);
  globalThis.fetch = mock(async () => { throw new Error('secret upstream revocation detail'); });
  const response = await logout(request('/api/auth/logout', {}));
  expect(response.status).toBe(503);
  expect(await response.json()).toEqual({ success: false, message: 'Signed out locally, but gateway revocation could not be confirmed.', data: null, error_code: 'UPSTREAM_REVOCATION_FAILED' });
  expect(row).toBeNull();
  expect(signouts).toBe(1);
  await expect(getGatewayAccessToken()).rejects.toMatchObject({ status: 401 });
});

const imageInput = {
  model: 'gpt-image-1', prompt: 'Dress this person in the supplied clothing.',
  reference_images: ['character', 'outfit', 'pose'].map(name => `https://media.example/${name}?token=signed-secret`),
};
const imageResponse = { data: [{ b64_json: 'aW1hZ2U=' }] };

// Exercise the real SSRF-checked downloader and sharp normalization. Only the
// DNS/HTTPS boundaries are replaced, with per-test spies restored afterEach.
function referenceDownloads(sources, gated = false) {
  const started = [];
  const gates = sources.map(() => Promise.withResolvers());
  const responses = [];
  const pinned = [];
  spyOn(dns, 'lookup').mockResolvedValue([{ address: '8.8.8.8', family: 4 }]);
  const network = spyOn(https, 'request').mockImplementation((url, options, callback) => {
    const index = started.length;
    started.push(url.href);
    options.lookup(url.hostname, { all: false }, (error, address, family) => {
      expect(error).toBeNull();
      pinned.push({ address, family });
    });
    gates[index]?.resolve();
    const req = new EventEmitter();
    req.setTimeout = () => req;
    req.end = () => { if (!gated) queueMicrotask(() => deliver(index)); };
    req.destroy = error => req.emit('error', error);
    responses[index] = callback;
    return req;
  });
  function deliver(index) {
    const response = new PassThrough();
    response.statusCode = 200;
    response.headers = { 'content-type': 'image/jpeg' };
    responses[index](response);
    response.end(sources[index]);
  }
  return { started, pinned, network, deliver, waitFor: index => gates[index].promise };
}

async function referenceJpeg(width = 2) {
  return sharp({ create: { width, height: 2, channels: 3, background: '#336699' } }).jpeg().toBuffer();
}

test('image edits upload ordered normalized JPEG files with multipart headers and prompt constraints', async () => {
  await persistGatewaySession(session, user);
  const sources = await Promise.all([referenceJpeg(2), referenceJpeg(3), referenceJpeg(4)]);
  const downloads = referenceDownloads(sources);
  const upstream = jsonFetch(imageResponse);
  const result = await generateImage({ ...imageInput, size: '1024x1536', negative_prompt: 'blur, extra limbs' });
  expect(result.images).toEqual(imageResponse.data);
  expect(downloads.started).toEqual(imageInput.reference_images);
  expect(downloads.pinned).toEqual(Array(3).fill({ address: '8.8.8.8', family: 4 }));
  const [url, init] = upstream.mock.calls[0];
  expect(new URL(url).pathname).toBe('/v1/images/edits');
  expect(init.method).toBe('POST');
  expect(init.body).toBeInstanceOf(FormData);
  expect(new Headers(init.headers).get('content-type')).toBeNull();
  expect(new Headers(init.headers).get('authorization')).toBe(`Bearer ${session.provider_token}`);
  expect(init.cache).toBe('no-store');
  expect(init.redirect).toBe('error');
  const wireRequest = new Request(url, init);
  expect(wireRequest.headers.get('content-type')).toMatch(/^multipart\/form-data; boundary=/);
  const form = await wireRequest.formData();
  expect(form.get('model')).toBe('gpt-image-1');
  expect(form.get('size')).toBe('1024x1536');
  expect(form.get('n')).toBe('1');
  expect(form.get('prompt')).toContain(imageInput.prompt);
  expect(form.get('prompt')).toMatch(/first image.*identity/i);
  expect(form.get('prompt')).toMatch(/middle images.*clothing/i);
  expect(form.get('prompt')).toMatch(/last image.*pose/i);
  expect(form.get('prompt')).toContain('Negative constraints: Do not include');
  expect(form.get('prompt')).toContain('blur, extra limbs');
  expect(form.has('negative_prompt')).toBe(false);
  expect(form.has('extra_body')).toBe(false);
  const files = form.getAll('image[]');
  expect(files).toHaveLength(3);
  for (let index = 0; index < files.length; index++) {
    expect(files[index].name).toBe(`reference-${index + 1}.jpg`);
    expect(files[index].type).toBe('image/jpeg');
    const bytes = Buffer.from(await files[index].arrayBuffer());
    expect(bytes.subarray(0, 3)).toEqual(Buffer.from([255, 216, 255]));
    expect(await sharp(bytes).metadata()).toMatchObject({ format: 'jpeg', width: index + 2, height: 2 });
  }
});

test('eight references download in batches of two and retain order despite reversed completions', async () => {
  await persistGatewaySession(session, user);
  const sources = await Promise.all(Array.from({ length: 8 }, (_, index) => referenceJpeg(index + 2)));
  const downloads = referenceDownloads(sources, true);
  const upstream = jsonFetch(imageResponse);
  const refs = Array.from({ length: 8 }, (_, index) => `https://media.example/reference-${index}`);
  const pending = generateImage({ ...imageInput, reference_images: refs });
  for (let index = 0; index < 8; index += 2) {
    await downloads.waitFor(index + 1);
    expect(downloads.started).toEqual(refs.slice(0, index + 2));
    downloads.deliver(index + 1);
    await Promise.resolve();
    expect(downloads.started).toHaveLength(index + 2);
    downloads.deliver(index);
  }
  await pending;
  const form = upstream.mock.calls[0][1].body;
  const files = form.getAll('image[]');
  expect(files).toHaveLength(8);
  for (let index = 0; index < files.length; index++) {
    expect((await sharp(Buffer.from(await files[index].arrayBuffer())).metadata()).width).toBe(index + 2);
  }
});

test.each([200, 401])('multipart 401 replay preserves every file and refreshes exactly once when retry returns %s', async retryStatus => {
  await persistGatewaySession(session, user);
  const downloads = referenceDownloads(Array(3).fill(await referenceJpeg()));
  const oldToken = session.provider_token;
  const nextToken = providerToken(Math.floor(Date.now() / 1000) + 3600, 'image-401');
  const uploads = [];
  let refreshes = 0;
  globalThis.fetch = mock(async (url, init) => {
    if (new URL(url).pathname === '/oauth/token') {
      refreshes++;
      return Response.json({ access_token: nextToken, refresh_token: 'image-refresh-rotated' });
    }
    const wire = new Request(url, init);
    const form = await wire.formData(); // Consume the first serialized body before retrying.
    uploads.push({ token: wire.headers.get('authorization'), prompt: form.get('prompt'),
      files: await Promise.all(form.getAll('image[]').map(async file => ({
        name: file.name, type: file.type, bytes: Buffer.from(await file.arrayBuffer()),
      }))) });
    return uploads.length === 1 || retryStatus === 401
      ? Response.json({ error: 'invalid_token' }, { status: 401 })
      : Response.json(imageResponse);
  });
  const pending = generateImage({ ...imageInput, accessToken: oldToken });
  if (retryStatus === 401) await expect(pending).rejects.toMatchObject({ status: 401 });
  else expect((await pending).images).toEqual(imageResponse.data);
  expect(refreshes).toBe(1);
  expect(uploads).toHaveLength(2);
  expect(uploads.map(value => value.token)).toEqual([`Bearer ${oldToken}`, `Bearer ${nextToken}`]);
  expect(uploads[1].files).toEqual(uploads[0].files);
  expect(uploads[0].files).toHaveLength(3);
  expect(uploads[1].prompt).toBe(uploads[0].prompt);
  expect(downloads.network).toHaveBeenCalledTimes(3);
});

test.each(['auto', '1024x1024', '1024x1536', '1536x1024'])('image edits accept supported size %s and URL-optional GPT image results', async size => {
  await persistGatewaySession(session, user);
  referenceDownloads(Array(3).fill(await referenceJpeg()));
  const upstream = jsonFetch({ ...imageResponse, request_id: 'image-request-1', token: 'must-not-leak',
    usage: { input_tokens: 3, output_tokens: 7, total_tokens: 10, input_tokens_details: { image_tokens: 2, text_tokens: 1 }, token: 'must-not-leak' } });
  const result = await generateImage({ ...imageInput, size });
  expect(result).toEqual({ images: imageResponse.data, request_id: 'image-request-1',
    usage: { input_tokens: 3, output_tokens: 7, total_tokens: 10, input_tokens_details: { image_tokens: 2, text_tokens: 1 } } });
  expect(upstream.mock.calls[0][1].body.get('size')).toBe(size);
});

test('image responses retain validated HTTPS URLs, revised prompts, and explicit zero legacy usage', async () => {
  await persistGatewaySession(session, user);
  referenceDownloads(Array(3).fill(await referenceJpeg()));
  const payload = { data: [{ url: 'https://images.example/generated.png?signature=signed', revised_prompt: 'A dressed person.' }],
    usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }, request_id: 'req-image-1' };
  jsonFetch(payload);
  expect(await generateImage(imageInput)).toEqual({ images: payload.data, usage: payload.usage, request_id: payload.request_id });
});

test.each([
  { model: '' }, { model: ' '.repeat(2) }, { model: 'm'.repeat(201) },
  { prompt: '' }, { prompt: ' '.repeat(2) }, { prompt: 'p'.repeat(32_001) },
  { prompt: 'p'.repeat(31_999), negative_prompt: 'n'.repeat(4_000) },
  { negative_prompt: 'n'.repeat(4_001) },
  { size: '2048x2048' }, { size: null },
  { n: 0 }, { n: -1 }, { n: 2 }, { n: 1.5 }, { n: '1' }, { n: null }, { n: Number.MAX_SAFE_INTEGER },
  { reference_images: undefined }, { reference_images: [] }, { reference_images: imageInput.reference_images.slice(0, 2) },
  { reference_images: Array(9).fill(imageInput.reference_images[0]) },
  { reference_images: ['http://media.example/a', ...imageInput.reference_images.slice(1)] },
  { reference_images: ['https://user:pass@media.example/a', ...imageInput.reference_images.slice(1)] },
  { reference_images: ['https://media.example:444/a', ...imageInput.reference_images.slice(1)] },
  { reference_images: ['data:image/png;base64,aW1hZ2U=', ...imageInput.reference_images.slice(1)] },
  { reference_images: ['not-a-url-with-secret', ...imageInput.reference_images.slice(1)] },
  { reference_images: ['', ...imageInput.reference_images.slice(1)] },
  { character_image: 'legacy-unsupported' },
])('invalid image edit input fails safely before network or auth (case %#)', async invalid => {
  await expect(generateImage({ ...imageInput, ...invalid })).rejects.toMatchObject({ status: 400, code: 'BAD_REQUEST_INVALID_BODY' });
  expect(authCalls).toBe(0);
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('multi-megabyte base64 PNG results remain usable without validation stack overflow', async () => {
  await persistGatewaySession(session, user);
  referenceDownloads(Array(3).fill(await referenceJpeg()));
  const png = await sharp({ create: { width: 2000, height: 1000, channels: 3, background: '#336699' } })
    .png({ compressionLevel: 0 }).toBuffer();
  const b64_json = png.toString('base64');
  jsonFetch({ data: [{ b64_json }] });
  expect((await generateImage(imageInput)).images).toEqual([{ b64_json }]);
});

test.each([
  null, {}, { data: [] }, { data: [{}] }, { data: [{ revised_prompt: 'no image' }] },
  { data: [{ url: '' }] }, { data: [{ b64_json: '' }] }, { data: [{ b64_json: 'not-base64!' }] },
  { data: [{ url: 'javascript:alert(1)' }] }, { data: [{ url: 'https://user:secret@media.example/a' }] },
  { data: [{ b64_json: 'aW1hZ2U=' }, {}] },
  { ...imageResponse, usage: { total_tokens: -1 } }, { ...imageResponse, usage: { total_tokens: 1.5 } },
  { ...imageResponse, usage: { total_tokens: '10' } }, { ...imageResponse, usage: { total_tokens: Number.MAX_SAFE_INTEGER + 1 } },
  { ...imageResponse, usage: {} }, { ...imageResponse, usage: null },
  { ...imageResponse, usage: { input_tokens: 1, input_tokens_details: { image_tokens: -1 } } },
  { ...imageResponse, request_id: { secret: 'must-not-leak' } },
  { ...imageResponse, request_id: 'https://media.example/image?token=secret' },
])('empty or malformed successful image responses fail closed: %j', async payload => {
  await persistGatewaySession(session, user);
  referenceDownloads(Array(3).fill(await referenceJpeg()));
  jsonFetch(payload);
  await expect(generateImage(imageInput)).rejects.toMatchObject({ status: 502, code: 'UPSTREAM_INVALID_RESPONSE' });
});

test('non-JSON image success and failed reference downloads expose no raw body or signed URL', async () => {
  await persistGatewaySession(session, user);
  const source = await referenceJpeg();
  referenceDownloads(Array(3).fill(source));
  globalThis.fetch = mock(async () => new Response('<html>secret-token</html>'));
  await expect(generateImage(imageInput)).rejects.toMatchObject({ status: 502, message: 'Invalid gateway image response.' });
  mock.restore();
  const downloads = referenceDownloads([Buffer.from('secret-token-invalid-image'), source, source]);
  globalThis.fetch = mock(async () => { throw new Error('Must not upload failed references'); });
  await expect(generateImage(imageInput)).rejects.toMatchObject({ status: 502, message: 'Reference images could not be prepared.', code: 'UPSTREAM_REFERENCE_IMAGE_ERROR' });
  expect(downloads.started).toHaveLength(2);
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('aggregate normalized reference uploads above 40 MiB are rejected before upstream generation', async () => {
  await persistGatewaySession(session, user);
  // Reference images are now re-encoded to bounded JPEGs, so force a deterministic
  // per-image size and let the aggregate byte cap (40 MiB) be the only guard tripped.
  const source = await referenceJpeg();
  const large = Buffer.alloc(15 * 1024 * 1024, 7);
  spyOn(sharp.prototype, 'jpeg').mockReturnValue({ toBuffer: async () => large });
  referenceDownloads(Array(3).fill(source));
  await expect(generateImage(imageInput)).rejects.toMatchObject({ status: 413, code: 'REFERENCE_IMAGES_TOO_LARGE' });
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test('JSON gateway requests retain JSON Content-Type while bodyless calls omit it', async () => {
  await persistGatewaySession(session, user);
  const upstream = jsonFetch({ status: 'success' });
  await fetchGatewayCheckout({ kind: 'product', id: 1, payment_method: 'balance' });
  expect(new Headers(upstream.mock.calls[0][1].headers).get('content-type')).toBe('application/json');
  jsonFetch({ sub: '42' });
  await fetchGatewayUserinfo();
  expect(new Headers(globalThis.fetch.mock.calls[0][1].headers).get('content-type')).toBeNull();
});

test('logout revokes the current stored refresh token, deletes its vault, and signs out', async () => {
  await persistGatewaySession(session, user);
  const upstream = jsonFetch({});
  const response = await logout(request('/api/auth/logout', {}));
  expect(response.status).toBe(200);
  expect(new URL(upstream.mock.calls[0][0]).pathname).toBe('/oauth/revoke');
  expect(new URLSearchParams(upstream.mock.calls[0][1].body).get('token')).toBe('provider-refresh-original');
  expect(row).toBeNull();
  expect(signouts).toBe(1);
});
