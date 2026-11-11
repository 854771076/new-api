import { beforeEach, expect, mock, test } from 'bun:test';
import sharp from 'sharp';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit', appOrigin: 'https://outfit.example' } }));
const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const characterId = 'fef51004-ccdb-4c42-9b87-d72a59ea26e0';
let user;
let clientId;
let character;
let calls;
let signed;
const storage = {
  from(bucket) {
    return {
      async upload(path, bytes, options) { calls.push({ action: 'upload', bucket, path, bytes, options }); return { error: null }; },
      async remove(paths) { calls.push({ action: 'remove', bucket, paths }); return { error: null }; },
      async createSignedUrl(path) { calls.push({ action: 'sign', bucket, path }); return signed; },
      async createSignedUrls(paths) { calls.push({ action: 'signs', bucket, paths }); return { data: [], error: null }; },
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
    if (operation.action === 'insert') return { data: operation.input, error: null };
    const matches = character && operation.filters.every(([key, value]) => character[key] === value);
    return { data: matches ? character : null, error: null };
  }
  return builder;
}
mock.module('@/lib/supabase/server', () => ({ createServerSupabase: async () => ({
  auth: { getUser: async () => ({ data: { user }, error: null }) },
  rpc: async () => ({ data: clientId, error: null }), storage, from: query,
}) }));
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => ({ from: query, storage: {
  from(bucket) {
    const client = storage.from(bucket);
    return { upload: client.upload, remove: client.remove };
  },
} }) }));
const { NextRequest } = await import('next/server');
const { POST, PATCH, DELETE } = await import('../../app/api/characters/route');
const { GET: media } = await import('../../app/api/media/[bucket]/[...path]/route');
const { getSignedUrls } = await import('../media-urls');

function request(method, body, origin = 'https://outfit.example') {
  return new NextRequest(`https://outfit.example/api/characters?id=${characterId}`, {
    method, headers: { ...(origin ? { origin } : {}), ...(body instanceof FormData ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : body instanceof FormData ? body : JSON.stringify(body),
  });
}
beforeEach(() => {
  user = { id: userId };
  clientId = 'nac_outfit';
  character = { id: characterId, client_id: clientId, user_id: userId, bucket: 'media-user', image_path: `characters/${characterId}.jpg` };
  calls = [];
  signed = { data: null, error: { message: 'private diagnostic' } };
});

test('normalizes a real image and stores only the verified App/user locator', async () => {
  const jpeg = await sharp({ create: { width: 2, height: 3, channels: 3, background: '#ffffff' } }).jpeg().toBuffer();
  const form = new FormData();
  form.set('file', new File([jpeg], 'portrait.jpg', { type: 'image/jpeg' }));
  form.set('name', 'Portrait');
  const response = await POST(request('POST', form));
  expect(response.status).toBe(200);
  const uploaded = calls.find(call => call.action === 'upload');
  const inserted = calls.find(call => call.action === 'insert');
  expect(uploaded.path).toBe(`nac_outfit/${userId}/characters/${inserted.input.id}.png`);
  expect(uploaded.options).toEqual({ contentType: 'image/png', upsert: false });
  const metadata = await sharp(uploaded.bytes).metadata();
  expect([metadata.format, metadata.width, metadata.height]).toEqual(['png', 2, 3]);
  expect(inserted.input.user_id).toBe(userId);
});

test.each([null, 'https://evil.example'])('rejects untrusted Origin before privileged mutation %s', async origin => {
  const response = await PATCH(request('PATCH', { name: 'Changed' }, origin));
  expect(response.status).toBe(403);
  expect(calls).toEqual([]);
});

test('rejects a different verified App and unauthenticated upload', async () => {
  clientId = 'nac_other';
  expect((await POST(request('POST', {}))).status).toBe(403);
  user = null;
  expect((await POST(request('POST', {}))).status).toBe(401);
  expect(calls).toEqual([]);
});

test('never accepts caller-owned image locators in PATCH', async () => {
  expect((await PATCH(request('PATCH', { name: 'Changed', image_path: 'victim/image.png' }))).status).toBe(400);
  expect(calls).toEqual([]);
});

test('clears descriptions and scopes the update by both App and owner', async () => {
  expect((await PATCH(request('PATCH', { description: null }))).status).toBe(200);
  expect(calls[0].input.description).toBeNull();
  expect(calls[0].filters).toEqual([['id', characterId], ['client_id', clientId], ['user_id', userId]]);
});

test('a foreign-owned row cannot be edited or deleted', async () => {
  character.user_id = '749235d0-2b36-4b01-97b1-425d0dfe0280';
  expect((await PATCH(request('PATCH', { name: 'Changed' }))).status).toBe(404);
  expect((await DELETE(request('DELETE'))).status).toBe(404);
  expect(calls.some(call => call.action === 'remove')).toBe(false);
});

test.each(['victim/private.png', 'nac_other/victim/characters/a.png'])('does not delete an arbitrary legacy locator %s', async path => {
  character.image_path = path;
  expect((await DELETE(request('DELETE'))).status).toBe(409);
  expect(calls.some(call => call.action === 'delete' || call.action === 'remove')).toBe(false);
});

test('preserves deletion of an owner verified legacy character', async () => {
  expect((await DELETE(request('DELETE'))).status).toBe(200);
  expect(calls.find(call => call.action === 'delete').filters).toEqual([['id', characterId], ['client_id', clientId], ['user_id', userId]]);
  expect(calls.find(call => call.action === 'remove').paths).toEqual([character.image_path]);
});

test('media route and SSR signatures enforce user Storage RLS without service role', async () => {
  const response = await media(new NextRequest('https://outfit.example/api/media/media-user/private.png'), {
    params: Promise.resolve({ bucket: 'media-user', path: ['private.png'] }),
  });
  expect(response.status).toBe(404);
  expect(await response.json()).toEqual({ error: 'MEDIA_NOT_FOUND' });
  expect(await getSignedUrls('media-user', ['private.png'])).toEqual({});
  expect(calls.map(call => call.action)).toEqual(['sign', 'signs']);
});

test('rejects oversized and non-image upload without Storage writes', async () => {
  const form = new FormData();
  form.set('file', new File([new Uint8Array(4 * 1024 * 1024 + 1)], 'large.png', { type: 'image/png' }));
  expect((await POST(request('POST', form))).status).toBe(413);
  form.set('file', new File(['not an image'], 'bad.png', { type: 'image/png' }));
  expect((await POST(request('POST', form))).status).toBe(400);
  expect(calls).toEqual([]);
});
