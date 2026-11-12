import { beforeEach, expect, mock, test } from 'bun:test';
import sharp from 'sharp';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit', appOrigin: 'https://outfit.example' } }));
const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const garmentId = 'fef51004-ccdb-4c42-9b87-d72a59ea26e0';
let user;
let clientId;
let garment;
let calls;
const storage = {
  from(bucket) {
    return {
      async upload(path, bytes, options) { calls.push({ action: 'upload', bucket, path, bytes, options }); return { error: null }; },
      async remove(paths) { calls.push({ action: 'remove', bucket, paths }); return { error: null }; },
    };
  },
};
function query(table) {
  const operation = { table, action: 'select', filters: [] };
  const builder = {
    select() { return builder; },
    eq(key, value) { operation.filters.push([key, value]); return builder; },
    order() { return builder; },
    insert(input) { operation.action = 'insert'; operation.input = input; return builder; },
    update(input) { operation.action = 'update'; operation.input = input; return builder; },
    delete() { operation.action = 'delete'; return builder; },
    single: execute,
    maybeSingle: execute,
    then(resolve, reject) { return execute().then(resolve, reject); },
  };
  async function execute() {
    calls.push(operation);
    if (operation.action === 'insert') return { data: { id: garmentId, ...operation.input }, error: null };
    const matches = garment && operation.filters.every(([key, value]) => garment[key] === value);
    return { data: matches ? garment : null, error: null };
  }
  return builder;
}
mock.module('@/lib/supabase/server', () => ({ createServerSupabase: async () => ({
  auth: { getUser: async () => ({ data: { user }, error: null }) },
  rpc: async () => ({ data: clientId, error: null }), storage, from: query,
}) }));
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => ({ from: query, storage }) }));
const { NextRequest } = await import('next/server');
const { GET, POST, PATCH, DELETE } = await import('../route');

function request(method, body, origin = 'https://outfit.example') {
  return new NextRequest(`https://outfit.example/api/outfits?id=${garmentId}`, {
    method,
    headers: { ...(origin ? { origin } : {}), ...(body instanceof FormData ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : body instanceof FormData ? body : JSON.stringify(body),
  });
}

beforeEach(() => {
  user = { id: userId };
  clientId = 'nac_outfit';
  garment = {
    id: garmentId, client_id: clientId, source: 'user', user_id: userId,
    bucket: 'media-user', thumb_path: `nac_outfit/${userId}/outfits/${garmentId}.png`,
    preview_path: `nac_outfit/${userId}/outfits/${garmentId}.png`,
    original_bucket: 'media-user', original_path: `nac_outfit/${userId}/outfits/${garmentId}.png`,
    product_bucket: null, product_path: null,
  };
  calls = [];
});

test('personal upload stores original under the user locator and marks it pending parse', async () => {
  const jpeg = await sharp({ create: { width: 2, height: 3, channels: 3, background: '#ffffff' } }).jpeg().toBuffer();
  const form = new FormData();
  form.set('file', new File([jpeg], 'tee.jpg', { type: 'image/jpeg' }));
  form.set('name', '白T');
  form.set('category', '上装');
  const response = await POST(request('POST', form));
  expect(response.status).toBe(200);
  const uploaded = calls.find(call => call.action === 'upload');
  const inserted = calls.find(call => call.action === 'insert');
  const newId = inserted.input.id;
  expect(newId).toMatch(/^[0-9a-f-]{36}$/);
  expect(uploaded.bucket).toBe('media-user');
  expect(uploaded.path).toBe(`nac_outfit/${userId}/outfits/${newId}.png`);
  expect(uploaded.options).toEqual({ contentType: 'image/png', upsert: false });
  expect(inserted.input).toMatchObject({
    source: 'user', user_id: userId, category: '上装', name_zh: '白T',
    parse_status: 'pending', bucket: 'media-user', is_active: true,
  });
  // 原图同时写入 original_* 与展示字段，商品图留空待按需解析。
  expect(inserted.input.original_path).toBe(uploaded.path);
  expect(inserted.input.thumb_path).toBe(uploaded.path);
  expect(inserted.input.product_path).toBeUndefined();
});

test('rejects an unknown garment category without touching storage', async () => {
  const jpeg = await sharp({ create: { width: 2, height: 3, channels: 3, background: '#ffffff' } }).jpeg().toBuffer();
  const form = new FormData();
  form.set('file', new File([jpeg], 'x.jpg', { type: 'image/jpeg' }));
  form.set('name', 'x');
  form.set('category', '不存在的分类');
  const response = await POST(request('POST', form));
  expect(response.status).toBe(400);
  expect(calls).toEqual([]);
});

test.each([null, 'https://evil.example'])('rejects untrusted Origin on mutation %s', async origin => {
  expect((await PATCH(request('PATCH', { name: 'Changed' }, origin))).status).toBe(403);
  expect((await DELETE(request('DELETE', undefined, origin))).status).toBe(403);
  expect(calls).toEqual([]);
});

test('rejects a different verified App and unauthenticated upload', async () => {
  clientId = 'nac_other';
  expect((await POST(request('POST', new FormData()))).status).toBe(403);
  user = null;
  expect((await POST(request('POST', new FormData()))).status).toBe(401);
  expect(calls).toEqual([]);
});

test('GET filters to personal rows owned by the caller', async () => {
  const response = await GET();
  const select = calls.find(call => call.action === 'select');
  expect(select.filters).toEqual([
    ['client_id', 'nac_outfit'],
    ['source', 'user'],
    ['user_id', userId],
  ]);
  expect(response.status).toBe(200);
});

test('update is scoped by App, source and owner; strict schema ignores extra fields', async () => {
  const response = await PATCH(request('PATCH', { name: '改名', user_id: 'victim' }));
  expect(response.status).toBe(400);
  expect(calls).toEqual([]);
  const ok = await PATCH(request('PATCH', { name: '改名' }));
  expect(ok.status).toBe(200);
  const update = calls.find(call => call.action === 'update');
  expect(update.filters).toEqual([
    ['id', garmentId], ['client_id', clientId], ['source', 'user'], ['user_id', userId],
  ]);
});

test('a foreign-owned personal garment cannot be deleted', async () => {
  garment.user_id = '749235d0-2b36-4b01-97b1-425d0dfe0280';
  expect((await DELETE(request('DELETE'))).status).toBe(404);
  expect(calls.some(call => call.action === 'remove')).toBe(false);
});

test('delete removes the row then dedupes original/product storage paths', async () => {
  const productPath = `nac_outfit/${userId}/outfits/${garmentId}-product.png`;
  garment.product_bucket = 'media-user';
  garment.product_path = productPath;
  garment.preview_path = garment.thumb_path; // 首次解析前展示图即原图，应去重
  const response = await DELETE(request('DELETE'));
  expect(response.status).toBe(200);
  const remove = calls.find(call => call.action === 'remove');
  expect(remove.paths).toEqual([garment.thumb_path, productPath]);
  expect(new Set(remove.paths).size).toBe(remove.paths.length);
});

test('rejects non-image upload without Storage writes', async () => {
  const form = new FormData();
  form.set('file', new File(['not an image'], 'bad.png', { type: 'image/png' }));
  form.set('name', 'bad');
  form.set('category', '上装');
  expect((await POST(request('POST', form))).status).toBe(400);
  expect(calls).toEqual([]);
});
