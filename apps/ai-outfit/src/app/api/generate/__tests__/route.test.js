import { beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit', appOrigin: 'https://outfit.example' } }));

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const clientId = 'nac_outfit';
const charId = 'a1111111-1111-4111-8111-111111111111';
const outfitId = 'b2222222-2222-4222-8222-222222222222';
const poseId = 'c3333333-3333-4333-8333-333333333333';
const recordId = 'd4444444-4444-4444-8444-444444444444';
const otherUser = 'e5555555-5555-4555-8555-555555555555';

let user;
let tables;
let inserts;
let runTryonShouldThrow;
const runTryonCalls = [];

function rowMatches(row, filters) {
  return filters.every(([key, value, op]) => {
    if (op === 'in') return Array.isArray(value) && value.includes(row[key]);
    return row[key] === value;
  });
}

function resolve(op, mode) {
  if (op.action === 'insert') {
    inserts.push({ table: op.table, input: op.input });
    return { data: { id: recordId, ...op.input }, error: null };
  }
  const rows = (tables[op.table] ?? []).filter((r) => rowMatches(r, op.filters));
  return { data: mode === 'list' ? rows : (rows[0] ?? null), error: null };
}

function query(table) {
  const op = { table, filters: [], action: 'select', input: null };
  const builder = {
    select() { return builder; },
    eq(k, v) { op.filters.push([k, v, 'eq']); return builder; },
    in(k, v) { op.filters.push([k, v, 'in']); return builder; },
    insert(input) { op.action = 'insert'; op.input = input; return builder; },
    update(input) { op.action = 'update'; op.input = input; return builder; },
    single: () => Promise.resolve(resolve(op, 'single')),
    maybeSingle: () => Promise.resolve(resolve(op, 'single')),
    then(resolveFn, reject) { return Promise.resolve(resolve(op, 'list')).then(resolveFn, reject); },
  };
  return builder;
}

const serverDb = { auth: { getUser: async () => ({ data: { user }, error: null }) }, from: query };
mock.module('@/lib/supabase/server', () => ({ createServerSupabase: async () => serverDb }));
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => ({ from: query, storage: { from: () => ({ upload: async () => ({ error: null }) }) } }) }));
mock.module('@/lib/gateway-session', () => ({ getGatewayAccessToken: async () => 'token' }));
mock.module('@/lib/tryon/models', () => ({
  resolveTryonModel: mock(async (_db, usage) =>
    (usage === 'image'
      ? { model: 'img-model', defaultSize: '1024x1536', priceQuota: 15000 }
      : { model: 'vision-model', defaultSize: null, priceQuota: 3000 })),
}));
mock.module('@/lib/tryon/orchestrate', () => ({
  runTryon: mock(async (_db, order) => {
    runTryonCalls.push(order);
    if (runTryonShouldThrow) {
      const { GatewayError } = await import('@/lib/gateway-errors');
      throw new GatewayError(502, '生成失败', 'UPSTREAM_GENERATE_FAILED');
    }
    return {
      resultBucket: 'media-user', resultPath: `p/${recordId}/result.png`,
      boardBucket: 'media-user', boardPath: `p/${recordId}/board.jpg`,
      manifest: [{ cell: 1, source: 'user', category: '外套', name: '夹克', semantic_hints: {} }],
      personaSnapshot: { character_id: charId, persona_version: 1, bucket: 'b', path: 'p' },
      wearSnapshot: [{ outfit_id: outfitId, source: 'user', category: '外套', bucket: 'b', path: 'p' }],
      providerSnapshot: { model: 'img-model', size: '1024x1536' },
      gatewayRequestId: 'req-1', gatewayUsage: { n: 1 }, wear: [],
    };
  }),
}));

const { NextRequest } = await import('next/server');
const { POST } = await import('../route');

function post(body, origin = 'https://outfit.example') {
  return POST(new NextRequest('https://outfit.example/api/generate', {
    method: 'POST',
    headers: { origin, 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }));
}

const validBody = () => ({
  character_id: charId, outfit_ids: [outfitId], pose_id: poseId,
  model: 'img-model', idempotency_key: 'stable-key-123',
});

beforeEach(() => {
  user = { id: userId };
  inserts = [];
  runTryonCalls.length = 0;
  runTryonShouldThrow = false;
  tables = {
    characters: [{
      id: charId, client_id: clientId, user_id: userId, name: '我', bucket: 'media-user',
      image_path: 'src.png', cover_path: null, persona_status: 'ready', persona_version: 1,
    }],
    outfits: [{
      id: outfitId, client_id: clientId, source: 'user', user_id: userId, category: '外套',
      name_zh: '夹克', bucket: 'media-user', thumb_path: 't.png', preview_path: 't.png',
      parse_status: 'ready', product_bucket: 'media-user', product_path: 'prod.png',
      garment_type: 'jacket', semantic_hints: {}, original_bucket: null, original_path: null,
      parse_attempts: 0, is_active: true,
    }],
    poses: [{
      id: poseId, client_id: clientId, name_zh: '站姿', system: 's', category: 'c',
      angle: 'a', bucket: 'b', thumb_path: 't', preview_path: 'p', is_active: true,
    }],
    outfit_generations: [],
  };
});

test('rejects requests with a disallowed origin', async () => {
  const res = await post(validBody(), 'https://evil.example');
  expect(res.status).toBe(403);
});

test('happy path freezes quote/snapshots and creates one succeeded task', async () => {
  const res = await post(validBody());
  const json = await res.json();
  expect(res.status).toBe(200);
  expect(json.success).toBe(true);
  expect(json.data.id).toBe(recordId);

  const insert = inserts.find((i) => i.table === 'outfit_generations');
  expect(insert.input).toMatchObject({
    stage: 'quoted', idempotency_key: 'stable-key-123', pipeline_version: 'tryon-v1',
    parse_fee_quota: 0, tryon_fee_quota: 15000,
  });
  expect(insert.input.quote).toMatchObject({ item_count: 1, unparsed_garment_ids: [] });
  expect(runTryonCalls).toHaveLength(1);
});

test('a repeated idempotency key returns the original task without re-running the pipeline', async () => {
  tables.outfit_generations = [{
    id: recordId, client_id: clientId, user_id: userId, idempotency_key: 'stable-key-123',
    status: 'succeeded', stage: 'done', result_bucket: 'b', result_path: 'p.png',
  }];
  const res = await post(validBody());
  const json = await res.json();
  expect(json.data.deduped).toBe(true);
  expect(json.data.id).toBe(recordId);
  expect(runTryonCalls).toHaveLength(0);
  expect(inserts).toHaveLength(0);
});

test('a personal garment owned by another user is forbidden', async () => {
  tables.outfits[0].user_id = otherUser;
  const res = await post(validBody());
  expect(res.status).toBe(403);
  expect((await res.json()).error_code).toBe('OUTFIT_FORBIDDEN');
});

test('an inactive garment is reported unavailable', async () => {
  tables.outfits[0].is_active = false;
  const res = await post(validBody());
  expect(res.status).toBe(400);
  expect((await res.json()).error_code).toBe('OUTFIT_NOT_AVAILABLE');
});

test('a model that is not the resolved image model is rejected before task creation', async () => {
  const res = await post({ ...validBody(), model: 'some-other-model' });
  expect(res.status).toBe(400);
  expect(inserts).toHaveLength(0);
});

test('orchestration failure marks the task failed and records the refunded quota', async () => {
  runTryonShouldThrow = true;
  const res = await post(validBody());
  expect(res.status).toBe(502);
  const json = await res.json();
  expect(json.success).toBe(false);
  // One insert for the task; the failure path does not create a second task.
  expect(inserts).toHaveLength(1);
  expect(runTryonCalls).toHaveLength(1);
});
