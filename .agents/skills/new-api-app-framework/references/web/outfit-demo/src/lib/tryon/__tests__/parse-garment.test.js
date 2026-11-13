import { beforeEach, expect, mock, test } from 'bun:test';
import sharp from 'sharp';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit', appOrigin: 'https://outfit.example' } }));

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const garmentId = 'bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb';
const originalPath = `nac_outfit/${userId}/outfits/${garmentId}.png`;
let calls;
let store;
let recognition;
let recognitionError;
let productError;
let signFailure;

const storage = {
  from() {
    return {
      async upload(path, bytes) {
        calls.push({ action: 'upload', path });
        store.objects[path] = bytes;
        return { error: null };
      },
      async remove(paths) {
        calls.push({ action: 'remove', paths });
        for (const p of paths) delete store.objects[p];
        return { error: null };
      },
      async download(path) {
        const bytes = store.objects[path];
        return bytes ? { data: new Blob([bytes]), error: null } : { data: null, error: { message: 'missing' } };
      },
    };
  },
};

function outfitsTable() {
  const op = { action: 'select', input: undefined, filters: [] };
  const builder = {
    select() { return builder; },
    eq(k, v) { op.filters.push([k, v]); return builder; },
    update(input) { op.action = 'update'; op.input = input; return builder; },
    maybeSingle: execute,
    then(resolve, reject) { return execute().then(resolve, reject); },
  };
  async function execute() {
    calls.push({ table: 'outfits', ...op });
    if (op.action === 'update') {
      Object.assign(store.row, op.input);
      return store.dbOk ? { data: { id: garmentId }, error: null } : { data: null, error: { message: 'db down' } };
    }
    return { data: store.row, error: null };
  }
  return builder;
}
const db = { from: (table) => (table === 'outfits' ? outfitsTable() : (() => { throw new Error(`unexpected ${table}`); })()), storage };

let generatedPng;
let originalPng;
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => db }));
mock.module('@/lib/media-urls', () => ({
  getSignedUrl: mock(async () => (signFailure ? null : 'https://media.example/signed.png')),
}));
mock.module('@/lib/gateway', () => ({
  generateReferenceImage: mock(async () => {
    if (productError) throw productError;
    return { images: [{ url: 'https://images.example/product.png' }], request_id: 'req-p' };
  }),
}));
mock.module('@/lib/generated-image', () => ({ readGeneratedImage: mock(async () => generatedPng) }));
mock.module('@/lib/tryon/models', () => ({
  resolveTryonModel: mock(async (supabase, usage) => ({ model: usage === 'vision' ? 'vision-model' : 'image-model', defaultSize: null })),
}));
mock.module('@/lib/tryon/recognize', () => ({
  recognizeGarment: mock(async () => {
    if (recognitionError) throw recognitionError;
    return recognition;
  }),
}));

const { parsePersonalGarment } = await import('../parse-garment');
const { GatewayError } = await import('@/lib/gateway-errors');

function garment(overrides = {}) {
  return {
    id: garmentId,
    category: '上装',
    name_zh: '白T',
    bucket: 'media-user',
    original_bucket: 'media-user',
    original_path: originalPath,
    thumb_path: originalPath,
    parse_status: 'pending',
    parse_attempts: 0,
    ...overrides,
  };
}
const ctx = { clientId: 'nac_outfit', userId, visionModel: 'vision-model', imageModel: 'image-model' };

beforeEach(async () => {
  calls = [];
  originalPng = await sharp({ create: { width: 100, height: 100, channels: 3, background: '#224466' } }).png().toBuffer();
  generatedPng = await sharp({ create: { width: 64, height: 64, channels: 3, background: '#ffffff' } }).png().toBuffer();
  store = { dbOk: true, objects: { [originalPath]: originalPng }, row: garment() };
  recognition = {
    category: '上装',
    garment_type: 'T-shirt',
    bbox: [20, 20, 60, 60],
    semantic_hints: { layer_hint: 'inner', color_hints: 'white' },
  };
  recognitionError = null;
  productError = null;
  signFailure = false;
});

test('parses a personal garment end to end and commits product locator + semantic profile', async () => {
  const result = await parsePersonalGarment(db, garment(), ctx);
  expect(result).toMatchObject({
    id: garmentId,
    productBucket: 'media-user',
    garmentType: 'T-shirt',
    bbox: [20, 20, 60, 60],
  });
  expect(result.productPath).toBe(`nac_outfit/${userId}/outfits/products/${garmentId}.png`);
  const finalUpdate = calls.filter((c) => c.table === 'outfits' && c.action === 'update').at(-1).input;
  expect(finalUpdate).toMatchObject({
    parse_status: 'ready',
    product_path: result.productPath,
    garment_type: 'T-shirt',
    bbox: [20, 20, 60, 60],
  });
  // 临时裁剪图上传后被清理。
  expect(calls.some((c) => c.action === 'upload' && c.path.includes('/outfits/tmp/'))).toBe(true);
  expect(Object.keys(store.objects).some((p) => p.includes('/outfits/tmp/'))).toBe(false);
});

test('a recognition failure marks the row failed, increments attempts, and writes no product', async () => {
  recognitionError = new GatewayError(502, 'bad bbox', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  await expect(parsePersonalGarment(db, garment(), ctx)).rejects.toBeInstanceOf(GatewayError);
  expect(store.row.parse_status).toBe('failed');
  expect(store.row.parse_attempts).toBe(1);
  expect(store.row.product_path).toBeUndefined();
});

test('a product-image failure keeps the original and records the failure', async () => {
  productError = new GatewayError(502, 'upstream', 'UPSTREAM_GENERATE_FAILED');
  await expect(parsePersonalGarment(db, garment(), ctx)).rejects.toBeInstanceOf(GatewayError);
  expect(store.row.parse_status).toBe('failed');
  expect(store.objects[originalPath]).toBeDefined();
});

test('an unsigned or missing original image fails before the model runs', async () => {
  signFailure = true;
  await expect(parsePersonalGarment(db, garment(), ctx)).rejects.toMatchObject({
    code: 'AI_TRYON_GARMENT_SOURCE_MISSING',
  });
  expect(store.row.parse_status).toBe('failed');
});

test('a failed DB commit removes the orphaned product object', async () => {
  store.dbOk = false;
  await expect(parsePersonalGarment(db, garment(), ctx)).rejects.toMatchObject({ code: 'UPSTREAM_DATABASE_ERROR' });
  const productPath = `nac_outfit/${userId}/outfits/products/${garmentId}.png`;
  expect(store.objects[productPath]).toBeUndefined();
  expect(store.row.parse_status).toBe('failed');
});
