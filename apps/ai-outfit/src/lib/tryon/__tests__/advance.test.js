import { beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const clientId = 'nac_outfit';
const charId = 'a1111111-1111-4111-8111-111111111111';
const outfitId = 'b2222222-2222-4222-8222-222222222222';
const recordId = 'd4444444-4444-4444-8444-444444444444';

let tables;
let updates;
let personaCalls;
let parseCalls;
let boardCalls;
let rollbackCalls;
let ensurePersonaResult;
let boardResult;
let boardShouldThrow;
let personaShouldThrow;
let parseShouldThrow;

function rowMatches(row, filters) {
  return filters.every(([key, value, op]) => {
    if (op === 'in') return Array.isArray(value) && value.includes(row[key]);
    return row[key] === value;
  });
}

function resolve(op, mode) {
  if (op.action === 'update') {
    updates.push({ table: op.table, input: op.input });
    return { data: null, error: null };
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
    update(input) { op.action = 'update'; op.input = input; return builder; },
    single: () => Promise.resolve(resolve(op, 'single')),
    maybeSingle: () => Promise.resolve(resolve(op, 'single')),
    then(resolveFn, reject) { return Promise.resolve(resolve(op, 'list')).then(resolveFn, reject); },
  };
  return builder;
}

mock.module('@/lib/config', () => ({ config: { gatewayClientId: clientId } }));
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => ({ from: query }) }));
mock.module('@/lib/tryon/persona', () => ({
  ensurePersona: mock(async () => {
    personaCalls.push(true);
    if (personaShouldThrow) {
      throw new (await import('@/lib/gateway-errors')).GatewayError(502, 'persona fail', 'AI_TRYON_PERSONA_FAILED');
    }
    return ensurePersonaResult;
  }),
}));
mock.module('@/lib/tryon/wear', () => ({
  resolveWearItems: mock(async () => []),
  OUTFIT_SELECT: 'id',
}));
mock.module('@/lib/tryon/parse-garment', () => ({
  parsePersonalGarment: mock(async (_db, garment) => {
    parseCalls.push(garment);
    if (parseShouldThrow) {
      throw new (await import('@/lib/gateway-errors')).GatewayError(502, 'parse fail', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
    }
  }),
}));
mock.module('@/lib/tryon/orchestrate', () => ({
  runBoardAndTryon: mock(async () => {
    boardCalls.push(true);
    if (boardShouldThrow) {
      throw new (await import('@/lib/gateway-errors')).GatewayError(502, 'generate fail', 'UPSTREAM_GENERATE_FAILED');
    }
    return boardResult;
  }),
}));
mock.module('@/lib/tryon/rollback', () => ({
  rollbackParsedGarments: mock(async (_db, ids) => { rollbackCalls.push(ids); }),
}));

const { advanceTryon } = await import('../advance');

function characterRow() {
  return {
    id: charId, client_id: clientId, user_id: userId, name: '我', bucket: 'media-user',
    image_path: 'src.png', cover_path: null, persona_status: 'ready', persona_version: 1,
  };
}

function outfitRow() {
  return {
    id: outfitId, client_id: clientId, source: 'user', user_id: userId, category: '外套',
    name_zh: '夹克', bucket: 'media-user', thumb_path: 't.png', preview_path: 't.png',
    parse_status: 'ready', product_bucket: 'media-user', product_path: 'prod.png',
    garment_type: 'jacket', semantic_hints: {}, original_bucket: null, original_path: null,
    parse_attempts: 0, is_active: true,
  };
}

function taskRow(overrides = {}) {
  return {
    id: recordId, client_id: clientId, user_id: userId, character_id: charId,
    outfit_ids: [outfitId], model: 'img-model', status: 'pending', stage: 'quoted',
    quote: { parse_fee_quota: 0, tryon_fee_quota: 15000, total_quota: 15000, unparsed_garment_ids: [outfitId], item_count: 1 },
    persona_snapshot: null,
    provider_snapshot: { model: 'img-model', size: '1024x1536', vision_model: 'vision-model' },
    pose_id: 'c3333333-3333-4333-8333-333333333333',
    pose_snapshot: { pose_id: 'c3333333-3333-4333-8333-333333333333', bucket: 'media-official', path: 'poses/pose-1.png' },
    result_bucket: null, result_path: null, error: null,
    ...overrides,
  };
}

beforeEach(() => {
  updates = [];
  personaCalls = [];
  parseCalls = [];
  boardCalls = [];
  rollbackCalls = [];
  boardShouldThrow = false;
  personaShouldThrow = false;
  parseShouldThrow = false;
  ensurePersonaResult = { bucket: 'media-user', path: 'p/persona.png', personaVersion: 2 };
  boardResult = {
    resultBucket: 'media-user', resultPath: 'p/result.png', boardBucket: 'media-user', boardPath: 'p/board.jpg',
    manifest: [], personaSnapshot: {}, wearSnapshot: [], providerSnapshot: {}, gatewayRequestId: 'req-1', wear: [],
  };
  tables = {
    outfit_generations: [taskRow()],
    characters: [characterRow()],
    outfits: [outfitRow()],
  };
});

test('returns immediately for a terminal task', async () => {
  tables.outfit_generations = [taskRow({ status: 'succeeded', stage: 'done', result_bucket: 'media-user', result_path: 'p/result.png' })];
  const res = await advanceTryon(recordId, userId);
  expect(res).toEqual({ status: 'succeeded', stage: 'done', result_bucket: 'media-user', result_path: 'p/result.png', error: null });
  expect(personaCalls).toHaveLength(0);
  expect(parseCalls).toHaveLength(0);
  expect(boardCalls).toHaveLength(0);
});

test('persists a persona snapshot when none exists', async () => {
  const res = await advanceTryon(recordId, userId);
  expect(res).toMatchObject({ status: 'pending', stage: 'parsing' });
  expect(personaCalls).toHaveLength(1);
  expect(parseCalls).toHaveLength(0);
  expect(boardCalls).toHaveLength(0);
  const update = updates.find((u) => u.table === 'outfit_generations');
  expect(update.input).toMatchObject({ stage: 'parsing' });
  expect(update.input.persona_snapshot).toEqual({ character_id: charId, persona_version: 2, bucket: 'media-user', path: 'p/persona.png' });
});

test('parses the next unparsed garment without finalizing', async () => {
  tables.outfit_generations = [taskRow({ persona_snapshot: { character_id: charId, persona_version: 1, bucket: 'b', path: 'p' }, stage: 'parsing' })];
  tables.outfits = [{ ...outfitRow(), parse_status: 'pending', product_bucket: null, product_path: null }];
  const res = await advanceTryon(recordId, userId);
  expect(res).toMatchObject({ status: 'pending', stage: 'parsing' });
  expect(parseCalls).toHaveLength(1);
  expect(parseCalls[0].id).toBe(outfitId);
  expect(boardCalls).toHaveLength(0);
});

test('runs board + tryon and marks the task succeeded', async () => {
  tables.outfit_generations = [taskRow({ persona_snapshot: { character_id: charId, persona_version: 1, bucket: 'b', path: 'p' }, stage: 'parsing' })];
  const res = await advanceTryon(recordId, userId);
  expect(res).toMatchObject({ status: 'succeeded', stage: 'done', result_bucket: 'media-user', result_path: 'p/result.png' });
  expect(boardCalls).toHaveLength(1);
  const update = updates.find((u) => u.table === 'outfit_generations');
  expect(update.input).toMatchObject({ status: 'succeeded', stage: 'done', result_path: 'p/result.png', charged_quota: 15000 });
});

test('rolls back parsed garments and marks failed on generation error', async () => {
  tables.outfit_generations = [taskRow({ persona_snapshot: { character_id: charId, persona_version: 1, bucket: 'b', path: 'p' }, stage: 'parsing' })];
  boardShouldThrow = true;
  const res = await advanceTryon(recordId, userId);
  expect(res.status).toBe('failed');
  expect(res.error).toBe('generate fail');
  expect(rollbackCalls).toHaveLength(1);
  expect(rollbackCalls[0]).toEqual([outfitId]);
  const update = updates.find((u) => u.table === 'outfit_generations');
  expect(update.input).toMatchObject({ status: 'failed', stage: 'failed', error: 'generate fail', refunded_quota: 15000 });
});

test('marks the task failed when persona generation fails', async () => {
  personaShouldThrow = true;
  const res = await advanceTryon(recordId, userId);
  expect(res.status).toBe('failed');
  expect(res.error).toBe('persona fail');
  expect(parseCalls).toHaveLength(0);
  expect(boardCalls).toHaveLength(0);
  const update = updates.find((u) => u.table === 'outfit_generations');
  expect(update.input).toMatchObject({ status: 'failed', stage: 'failed', error: 'persona fail', refunded_quota: 15000 });
});

test('marks the task failed and rolls back when a garment parse fails', async () => {
  tables.outfit_generations = [taskRow({ persona_snapshot: { character_id: charId, persona_version: 1, bucket: 'b', path: 'p' }, stage: 'parsing' })];
  tables.outfits = [{ ...outfitRow(), parse_status: 'pending', product_bucket: null, product_path: null }];
  parseShouldThrow = true;
  const res = await advanceTryon(recordId, userId);
  expect(res.status).toBe('failed');
  expect(res.error).toBe('parse fail');
  expect(rollbackCalls).toHaveLength(1);
  expect(rollbackCalls[0]).toEqual([outfitId]);
  expect(boardCalls).toHaveLength(0);
  const update = updates.find((u) => u.table === 'outfit_generations');
  expect(update.input).toMatchObject({ status: 'failed', stage: 'failed', error: 'parse fail', refunded_quota: 15000 });
});
