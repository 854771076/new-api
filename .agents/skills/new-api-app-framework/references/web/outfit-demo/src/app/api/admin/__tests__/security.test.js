import { afterAll, beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
mock.module('next/headers', () => ({ cookies: async () => ({ getAll: () => [], set() {} }) }));
const environment = {
  NEXT_PUBLIC_SUPABASE_URL: 'https://supabase.example', NEXT_PUBLIC_SUPABASE_ANON_KEY: 'anon-test',
  NEXT_PUBLIC_GATEWAY_BASE_URL: 'https://gateway.example', NEXT_PUBLIC_GATEWAY_CLIENT_ID: 'nac_outfit',
  SUPABASE_SECRET_KEY: 'service-test', APP_ORIGIN: 'https://outfit.example', NODE_ENV: 'test',
};
const originalEnvironment = Object.fromEntries(Object.keys(environment).map(key => [key, process.env[key]]));
Object.assign(process.env, environment);
const clientId = 'nac_outfit';
const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const ownId = 'bb2ed7f9-b315-447c-9389-fb40b2ca13e4';
const foreignId = '232402b8-5678-40da-a2cb-3b92b6d403c4';
const sharedId = '4f49ff46-b91d-4ae4-8e94-329b2d7b6654';
let user, trustedClient, scopeError, grantError, rows, operations, rpcError, signedPaths;

// Supabase Auth/DB/Storage are the external boundaries. Guards and handlers are real.
function query(table) {
  const predicates = [];
  let action = 'select', fields, head = false, single = false;
  const builder = {
    select(_columns, options) { head = options?.head ?? false; return builder; },
    eq(key, value) { predicates.push(row => row[key] === value); return builder; },
    in(key, values) { predicates.push(row => values.includes(row[key])); return builder; },
    gte(key, value) { predicates.push(row => row[key] >= value); return builder; },
    order() { return builder; }, range() { return builder; }, limit() { return builder; }, or() { return builder; },
    insert(value) { action = 'insert'; fields = value; return builder; },
    update(value) { action = 'update'; fields = value; return builder; },
    delete() { action = 'delete'; return builder; },
    single() { single = true; return execute(); }, maybeSingle() { single = true; return execute(); },
    then(resolve, reject) { return execute().then(resolve, reject); },
  };
  async function execute() {
    operations.push({ table, action, fields });
    if (table === 'app_admins' && grantError) return { data: null, error: { message: 'private database detail' } };
    if (action === 'insert') rows[table].push({ id: ownId, ...fields });
    const matched = (rows[table] ?? []).filter(row => predicates.every(predicate => predicate(row)));
    if (action === 'update') for (const row of matched) Object.assign(row, fields);
    if (action === 'delete') rows[table] = rows[table].filter(row => !matched.includes(row));
    return { data: head ? null : single ? matched[0] ?? null : matched, count: matched.length, error: null };
  }
  return builder;
}
const userDatabase = {
  auth: { getUser: async () => ({ data: { user }, error: null }) },
  from: query,
  rpc: async () => ({ data: trustedClient, error: scopeError ? { message: 'private RPC detail' } : null }),
  storage: { from: () => ({
    async createSignedUrl(path) {
      signedPaths.push(path);
      // External Storage fixture models RLS: only the current tenant's object is visible.
      return path === `${clientId}/result.png` ? { data: { signedUrl: 'https://media.example/signed' }, error: null } : { data: null, error: { message: 'private storage detail' } };
    },
    async createSignedUrls(paths) { signedPaths.push(...paths); return { data: [], error: null }; },
  }) },
};
const serviceDatabase = {
  from: query,
  async rpc(name, input) {
    operations.push({ name, input });
    if (rpcError) return { data: null, error: rpcError };
    const matched = (rows[input.p_table] ?? []).find(row => row.id === input.p_id && row.client_id === input.p_client_id);
    if (input.p_operation !== 'POST' && !matched) return { data: null, error: { code: 'P0002' } };
    return { data: { id: matched?.id ?? ownId }, error: null };
  },
  storage: { from() { throw new Error('Privileged storage signing is forbidden'); } },
};
mock.module('@supabase/ssr', () => ({ createServerClient: () => userDatabase }));
mock.module('@supabase/supabase-js', () => ({ createClient: () => serviceDatabase }));
const { NextRequest } = await import('next/server');
const { requireAdmin } = await import('../../../../lib/admin/requireAdmin');
const { GET, POST, PATCH, DELETE } = await import('../[table]/route');
const { GET: stats } = await import('../stats/route');
const { GET: sign } = await import('../signed-url/route');
const { GET: references } = await import('../generation-references/route');
const context = table => ({ params: Promise.resolve({ table }) });
function request(method, url = '/api/admin/outfits', body, origin = 'https://outfit.example') {
  return new NextRequest(`https://outfit.example${url}`, {
    method, headers: { ...(origin === null ? {} : { origin }), 'Content-Type': 'application/json' },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
}

beforeEach(() => {
  user = { id: userId, user_metadata: { aud: [clientId], role: 'owner' } };
  trustedClient = clientId;
  scopeError = false; grantError = false; rpcError = null; operations = []; signedPaths = [];
  process.env.NODE_ENV = 'test';
  rows = {
    app_admins: [{ user_id: userId, client_id: clientId, role: 'admin' }],
    outfits: [{ id: ownId, client_id: clientId, name_zh: 'Own', is_active: true }, { id: foreignId, client_id: 'other', name_zh: 'Foreign', is_active: true }],
    poses: [{ id: ownId, client_id: clientId, is_active: true }, { id: foreignId, client_id: 'other', is_active: true }],
    characters: [{ id: foreignId, client_id: 'other', name: 'Private', bucket: 'media-user', image_path: 'private/image.png' }],
    outfit_generations: [{ id: ownId, client_id: clientId, status: 'failed', created_at: new Date().toISOString(), character_id: foreignId, pose_id: foreignId, outfit_ids: [foreignId] }, { id: foreignId, client_id: 'other', status: 'failed', created_at: new Date().toISOString() }],
    ai_models: [{ id: ownId, client_id: clientId, model_id: 'own' }, { id: sharedId, client_id: '', model_id: 'shared' }, { id: foreignId, client_id: 'other', model_id: 'foreign' }],
    prompt_templates: [{ id: ownId, client_id: clientId }, { id: sharedId, client_id: '' }, { id: foreignId, client_id: 'other' }],
  };
});
afterAll(() => {
  for (const [key, value] of Object.entries(originalEnvironment)) {
    if (value === undefined) delete process.env[key]; else process.env[key] = value;
  }
});

test('unauthenticated admin API returns JSON 401, never a login redirect', async () => {
  user = null;
  const response = await GET(request('GET'), context('outfits'));
  expect(response.status).toBe(401);
  expect(response.headers.get('location')).toBeNull();
  expect(await response.json()).toEqual({ error: 'UNAUTHENTICATED' });
  expect(operations).toEqual([]);
});
test.each(['mismatch', 'empty', 'rpc-failure', 'wrong-grant', 'invalid-role', 'grant-failure'])('admin guard fails closed for %s despite forged user_metadata', async reason => {
  if (reason === 'mismatch') trustedClient = 'other';
  if (reason === 'empty') trustedClient = '';
  if (reason === 'rpc-failure') scopeError = true;
  if (reason === 'wrong-grant') rows.app_admins[0].client_id = 'other';
  if (reason === 'invalid-role') rows.app_admins[0].role = 'member';
  if (reason === 'grant-failure') grantError = true;
  const check = await requireAdmin(request('GET'));
  expect(check.ok).toBe(false);
  expect(check.response.status).toBe(403);
  expect(JSON.stringify(await check.response.json())).not.toContain('private');
});
test('valid guard returns the verified client scope and grant role', async () => {
  expect(await requireAdmin(request('GET'))).toEqual({ ok: true, userId, clientId, role: 'admin' });
});
test.each([['POST', POST], ['PATCH', PATCH], ['DELETE', DELETE]])('%s admin mutation rejects missing and foreign Origin', async (method, handler) => {
  for (const origin of [null, 'null', 'https://attacker.example', 'https://outfit.example.attacker.example']) {
    const response = await handler(request(method, `/api/admin/outfits?id=${ownId}`, { is_active: false }, origin), context('outfits'));
    expect(response.status).toBe(403);
    expect(operations.filter(operation => operation.action !== 'select')).toEqual([]);
  }
});
test.each(['outfits', 'poses', 'characters', 'outfit_generations'])('%s list excludes other tenants', async table => {
  const response = await GET(request('GET', `/api/admin/${table}`), context(table));
  expect(response.status).toBe(200);
  expect((await response.json()).data.every(row => row.client_id === clientId)).toBe(true);
});
test.each(['ai_models', 'prompt_templates'])('%s shared defaults are visible but foreign/shared mutation IDs are not writable', async table => {
  const response = await GET(request('GET', `/api/admin/${table}`), context(table));
  expect((await response.json()).data.map(row => row.client_id)).toEqual([clientId, '']);
  for (const id of [foreignId, sharedId]) {
    expect((await PATCH(request('PATCH', `/api/admin/${table}?id=${id}`, { is_default: true }), context(table))).status).toBe(404);
    expect((await DELETE(request('DELETE', `/api/admin/${table}?id=${id}`), context(table))).status).toBe(404);
  }
});
test.each(['client_id', 'user_id', 'source', 'bucket', 'thumb_path', 'preview_path', 'id', 'created_at', 'provider_id'])('outfit PATCH rejects immutable/injected %s', async field => {
  const response = await PATCH(request('PATCH', `/api/admin/outfits?id=${ownId}`, { [field]: 'other' }), context('outfits'));
  expect(response.status).toBe(400);
  expect(operations.filter(operation => operation.action === 'update')).toEqual([]);
});
test('outfit mutations only affect exact tenant rows and validated fields', async () => {
  expect((await PATCH(request('PATCH', `/api/admin/outfits?id=${foreignId}`, { is_active: false }), context('outfits'))).status).toBe(404);
  expect(rows.outfits[1].is_active).toBe(true);
  expect((await PATCH(request('PATCH', `/api/admin/outfits?id=${ownId}`, { is_active: false }), context('outfits'))).status).toBe(200);
  expect(rows.outfits[0].is_active).toBe(false);
  expect((await DELETE(request('DELETE', `/api/admin/outfits?id=${foreignId}`), context('outfits'))).status).toBe(404);
  expect(rows.outfits).toHaveLength(2);
});
test('outfit creation rejects foreign paths and assigns tenant server-side', async () => {
  const body = { name_zh: 'Dress', category: 'formal', provider_id: 'dress', bucket: 'media-official', thumb_path: `${clientId}/thumb.png`, preview_path: `${clientId}/preview.png` };
  expect((await POST(request('POST', '/api/admin/outfits', { ...body, thumb_path: 'other/thumb.png' }), context('outfits'))).status).toBe(400);
  expect((await POST(request('POST', '/api/admin/outfits', body), context('outfits'))).status).toBe(200);
  expect(rows.outfits.at(-1)).toMatchObject({ client_id: clientId, ...body });
});
test.each(['characters', 'outfit_generations'])('%s admin data remains readonly', async table => {
  for (const method of ['POST', 'PATCH', 'DELETE']) {
    const handler = { POST, PATCH, DELETE }[method];
    expect((await handler(request(method, `/api/admin/${table}?id=${ownId}`, {}), context(table))).status).toBe(405);
  }
});
test.each(['eq=client_id:other', 'keyword=a),client_id.eq.other', 'sort=client_id', 'page=NaN', 'client_id=other'])('admin list rejects unsafe query %s', async queryString => {
  expect((await GET(request('GET', `/api/admin/outfits?${queryString}`), context('outfits'))).status).toBe(400);
});
test('default mutation uses one scoped database RPC and preserves safe conflict responses', async () => {
  rpcError = { code: '23505', message: 'private duplicate detail' };
  const response = await PATCH(request('PATCH', `/api/admin/ai_models?id=${ownId}`, { model_id: 'duplicate', is_default: true }), context('ai_models'));
  expect(response.status).toBe(409);
  expect(await response.json()).toEqual({ error: 'CONFLICT' });
  expect(operations.filter(operation => operation.name)).toEqual([{ name: 'admin_mutate_configuration', input: {
    p_table: 'ai_models', p_client_id: clientId, p_actor_id: userId, p_operation: 'PATCH', p_id: ownId, p_fields: { model_id: 'duplicate', is_default: true },
  } }]);
  expect(operations.filter(operation => operation.action === 'update')).toEqual([]);
});
test('admin stats and recent failures exclude foreign records', async () => {
  const response = await stats(request('GET', '/api/admin/stats'));
  expect((await response.json()).data).toMatchObject({ generations_total: 1, generations_today: 1, generations_failed: 1, outfits_active: 1, poses_active: 1, characters_total: 0, recent_failures: [rows.outfit_generations[0]] });
});
test('admin reference lookup never signs foreign referenced rows', async () => {
  const response = await references(request('GET', `/api/admin/generation-references?id=${ownId}`));
  expect(await response.json()).toEqual({ data: { character: null, pose: null, outfits: [] } });
  expect(signedPaths).toEqual([]);
  expect((await references(request('GET', `/api/admin/generation-references?id=${foreignId}`))).status).toBe(404);
});
test('admin signing uses caller Storage authorization and hides rejected diagnostics', async () => {
  expect((await sign(request('GET', `/api/admin/signed-url?bucket=media-user&path=${clientId}/result.png`))).status).toBe(200);
  const response = await sign(request('GET', '/api/admin/signed-url?bucket=media-user&path=other/result.png'));
  expect(response.status).toBe(404);
  expect(await response.json()).toEqual({ error: 'NOT_FOUND' });
});
