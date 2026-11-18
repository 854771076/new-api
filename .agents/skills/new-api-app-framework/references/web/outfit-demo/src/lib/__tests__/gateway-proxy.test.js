import { afterAll, beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: {
  integrationMode: 'proxy', gatewayAppId: 7, gatewayClientId: 'proxy:7',
  supabaseUrl: 'https://native.example', gatewayBaseUrl: 'https://gateway.example', appOrigin: 'https://app.example',
} }));
const originalFetch = globalThis.fetch;
const originalPAT = process.env.GATEWAY_ADMIN_ACCESS_TOKEN;
const originalKey = process.env.GATEWAY_TOKEN_ENCRYPTION_KEY;
process.env.GATEWAY_ADMIN_ACCESS_TOKEN = 'server-pat';
process.env.GATEWAY_TOKEN_ENCRYPTION_KEY = Buffer.alloc(32, 9).toString('base64');
const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const sessionId = 'bb2ed7f9-b315-447c-9389-fb40b2ca13e4';
const issuer = 'https://native.example/auth/v1';
let user, token, active, records, requests, failSave, verified;
const jwt = (claims) => `verified.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.at-auth-boundary`;
function query(table) {
  const filters = []; let insert; let update; let ignore = false;
  const resolve = () => {
    if (insert && failSave && table === 'gateway_proxy_accounts') { failSave = false; return { data: null, error: true }; }
    const rows = records[table] ??= [];
    if (insert) {
      const prior = rows.find(row => table === 'gateway_proxy_sessions' ? row.session_id === insert.session_id
        : table === 'gateway_proxy_accounts' ? row.user_id === insert.user_id && row.app_id === insert.app_id && row.issuer === insert.issuer
          : row.request_id === insert.request_id);
      if (!prior || !ignore) rows.push(insert);
    }
    const row = rows.find(row => filters.every(([key, value]) => row[key] === value));
    if (row && update) Object.assign(row, update);
    return { data: row ?? null, error: null };
  };
  const builder = {
    select() { return builder; }, eq(k, v) { filters.push([k, v]); return builder; },
    upsert(value, options) { insert = value; ignore = options?.ignoreDuplicates; return builder; },
    update(value) { update = value; return builder; },
    maybeSingle: async () => resolve(), single: async () => resolve(),
    then(resolveFn, reject) { return Promise.resolve(resolve()).then(resolveFn, reject); },
  };
  return builder;
}
mock.module('@/lib/supabase/server', () => ({ createServerSupabase: async () => ({ auth: {
  getSession: async () => ({ data: { session: { access_token: token, user } }, error: null }),
  getUser: async exact => ({ data: { user: verified && exact === token ? user : null }, error: verified ? null : true }),
} }) }));
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => ({ from: query,
  rpc: async name => ({ data: name === 'gateway_proxy_session_active' ? active : null, error: null }),
}) }));
const { getProxyAccount, getProxyAIKey, proxyManagementRequest } = await import('../gateway-proxy');
const { requireGatewayOrigin } = await import('../gateway-bff');

test('production CSRF checks configured public origin behind a reverse proxy', () => {
  const previous = process.env.NODE_ENV;
  process.env.NODE_ENV = 'production';
  try {
    const request = origin => ({ nextUrl: new URL('http://localhost:3029/api/auth/login'), headers: new Headers({ origin, host: 'attacker.example', 'x-forwarded-host': 'attacker.example' }) });
    expect(() => requireGatewayOrigin(request('https://app.example'))).not.toThrow();
    for (const origin of ['https://attacker.example', 'http://localhost:3029', 'null', '']) {
      expect(() => requireGatewayOrigin(request(origin))).toThrow('Request origin is not allowed.');
    }
  } finally {
    if (previous === undefined) delete process.env.NODE_ENV; else process.env.NODE_ENV = previous;
  }
});

beforeEach(() => {
  user = { id: userId, identities: [{ provider: 'email' }] };
  token = jwt({ sub: userId, session_id: sessionId, iss: issuer });
  active = true; verified = true; records = {}; requests = []; failSave = false;
  globalThis.fetch = mock(async (url, options) => {
    requests.push({ url: String(url), ...options });
    if (new URL(url).pathname === '/api/app-proxy/users') return Response.json({ success: true, data: { user_id: 42, app_id: 7, identity_source: 'proxy', status: 1 } });
    if (new URL(url).pathname.endsWith('/keys')) return Response.json({ success: true, data: { id: 5, user_id: 42, app_id: 7, key: 'personal-ai-secret' } });
    if (new URL(url).pathname.endsWith('/checkout')) return Response.json({ order_id: 'APP-ORDER-1', app_id: 7, user_id: 42, status: 'pending' });
    return Response.json({ success: true, data: { user_id: 42, app_id: 7, quota: 80, used_quota: 20, credits: '8', capabilities: { subscription_supported: false } } });
  });
});
afterAll(() => {
  globalThis.fetch = originalFetch;
  if (originalPAT === undefined) delete process.env.GATEWAY_ADMIN_ACCESS_TOKEN; else process.env.GATEWAY_ADMIN_ACCESS_TOKEN = originalPAT;
  if (originalKey === undefined) delete process.env.GATEWAY_TOKEN_ENCRYPTION_KEY; else process.env.GATEWAY_TOKEN_ENCRYPTION_KEY = originalKey;
});

test('native identities recover the same App account and store only encrypted personal keys', async () => {
  const account = await getProxyAccount();
  expect(account).toMatchObject({ gateway_user_id: 42, app_id: 7, identity_source: 'proxy' });
  expect(JSON.stringify(records)).not.toContain('personal-ai-secret');
  expect(JSON.stringify(records)).not.toContain('server-pat');
  expect(await getProxyAIKey()).toBe('sk-personal-ai-secret');
  expect(requests.filter(request => request.url.endsWith('/users'))).toHaveLength(1);
  expect(requests[0].headers.Authorization).toBe('Bearer server-pat');
  expect(JSON.parse(requests[0].body)).toEqual({ app_id: 7, issuer, subject: userId });
});

test('a failed mapping save replays the same recoverable key operation', async () => {
  failSave = true;
  await expect(getProxyAccount()).rejects.toMatchObject({ status: 503 });
  expect((await getProxyAccount()).gateway_user_id).toBe(42);
  const operations = requests.filter(request => request.url.includes('/keys?'));
  expect(operations).toHaveLength(2);
  expect(operations[0].headers['Idempotency-Key']).toBe(operations[1].headers['Idempotency-Key']);
});

test.each(['unverified', 'revoked', 'other-project', 'linked-oauth'])(
  '%s identity cannot provision or read hosted keys', async reason => {
    if (reason === 'unverified') verified = false;
    if (reason === 'revoked') active = false;
    if (reason === 'other-project') token = jwt({ sub: userId, session_id: sessionId, iss: 'https://other.example/auth/v1' });
    if (reason === 'linked-oauth') user.identities.push({ provider: 'custom:newapi' });
    await expect(getProxyAIKey()).rejects.toMatchObject({ status: 401 });
    expect(requests).toHaveLength(0);
  }
);

test('proxy subscriptions are rejected and checkout association is durable before dispatch', async () => {
  await expect(proxyManagementRequest('/oauth/subscription/checkout', 'POST', '{}')).rejects.toMatchObject({ code: 'SUBSCRIPTION_UNSUPPORTED' });
  const requestId = '643d1175-d649-4624-9049-32c56d46dd38';
  const response = await proxyManagementRequest('/oauth/topup/checkout', 'POST', JSON.stringify({ amount: 10, payment_method: 'waffo_pancake' }), undefined, requestId);
  expect((await response.json()).order_id).toBe('APP-ORDER-1');
  expect(records.gateway_checkout_requests).toMatchObject([{ user_id: userId, app_id: 7, request_id: requestId, order_id: 'APP-ORDER-1' }]);
  await expect(proxyManagementRequest('/oauth/topup/checkout', 'POST', JSON.stringify({ amount: 11, payment_method: 'waffo_pancake' }), undefined, requestId)).rejects.toMatchObject({ status: 409 });
  expect(requests.filter(request => request.url.includes('/checkout?'))).toHaveLength(1);
});

test('checkout cannot select another target or forward extra parameters',async()=>{
 await expect(proxyManagementRequest('/oauth/topup/checkout','POST',JSON.stringify({amount:10,payment_method:'waffo_pancake',user_id:99}),undefined,'643d1175-d649-4624-9049-32c56d46dd38')).rejects.toMatchObject({status:400});
 expect(requests.filter(request=>request.url.includes('/checkout?'))).toHaveLength(0);
});
