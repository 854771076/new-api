import { beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit' } }));

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const recordId = 'rec-1';
const clientId = 'nac_outfit';

const stageUpdates = [];
const uploads = [];
const rollbackCalls = [];
let referenceCalls = [];
let personaResult;
let wearResult;
let generateShouldFail;
let personaShouldFail;
let boardShouldFail;

function thenable() {
  const builder = { eq: () => builder };
  builder.then = (resolve) => Promise.resolve({ error: null }).then(resolve);
  return builder;
}
const admin = {
  storage: {
    from() {
      return { upload: mock(async (path, bytes) => { uploads.push({ path, bytes }); return { error: null }; }) };
    },
  },
  from() {
    return {
      update: mock((input) => {
        if (input && typeof input === 'object' && 'stage' in input) stageUpdates.push(input.stage);
        return thenable();
      }),
    };
  },
};
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => admin }));
mock.module('@/lib/media-urls', () => ({ getSignedUrl: mock(async (_b, path) => `https://media.example/${encodeURIComponent(path)}`) }));
mock.module('@/lib/storage', () => ({ downloadStorageObject: mock(async () => Buffer.from([1, 2, 3])) }));
mock.module('@/lib/generated-image', () => ({ readGeneratedImage: mock(async () => Buffer.from([9, 9, 9])) }));
const { GatewayError: GatewayErrorClass } = await import('@/lib/gateway-errors');
mock.module('@/lib/gateway', () => ({
  GatewayError: GatewayErrorClass,
  generateReferenceImage: mock(async (input) => {
    referenceCalls.push(input);
    if (generateShouldFail) throw new Error('upstream boom');
    return { images: [{ url: 'https://gw.example/out.png' }], request_id: 'req-1', usage: { n: 1 } };
  }),
}));
mock.module('@/lib/tryon/persona', () => ({
  ensurePersona: mock(async () => {
    if (personaShouldFail) throw new (await import('@/lib/gateway-errors')).GatewayError(502, 'persona fail', 'AI_TRYON_PERSONA_FAILED');
    return personaResult;
  }),
}));
mock.module('@/lib/tryon/wear', () => ({
  resolveWearItems: mock(async () => wearResult),
  toManifestInputs: mock((wear) => wear.map((w) => ({ id: w.outfitId, source: w.source, category: w.category, name: w.name, garment_type: w.garmentType, semantic_hints: w.semanticHints }))),
}));
mock.module('@/lib/tryon/image', () => ({ buildGarmentBoard: mock(async (items) => {
  if (boardShouldFail) throw new (await import('@/lib/gateway-errors')).GatewayError(502, 'board fail', 'AI_TRYON_GARMENT_BOARD_FAILED');
  return { image: Buffer.from([4, 5, 6]), orderedIds: items.map((i) => i.id) };
}) }));
mock.module('@/lib/tryon/manifest', () => ({ buildGarmentManifest: mock(() => ({ json: '[{"cell":1}]', entries: [{ cell: 1, source: 'user', category: '外套', name: '夹克', semantic_hints: {} }] })) }));
mock.module('@/lib/tryon/rollback', () => ({ rollbackNewlyParsed: mock(async (...args) => { rollbackCalls.push(args); }) }));

const { runTryon } = await import('../orchestrate');
const { GatewayError } = await import('@/lib/gateway-errors');

function order() {
  return {
    recordId, clientId, userId,
    character: { id: 'char-1', bucket: 'media-user', image_path: 'src.png', cover_path: null, persona_status: 'pending', persona_version: 0 },
    candidates: [{ id: 'g1', source: 'user' }],
    quote: { parse_fee_quota: 0, tryon_fee_quota: 0, total_quota: 0, unparsed_garment_ids: ['g1'], item_count: 1 },
    imageModel: 'img-model', visionModel: 'vision-model', size: '1024x1536',
  };
}

beforeEach(() => {
  stageUpdates.length = 0;
  uploads.length = 0;
  rollbackCalls.length = 0;
  referenceCalls = [];
  generateShouldFail = false;
  personaShouldFail = false;
  boardShouldFail = false;
  personaResult = { bucket: 'media-user', path: `${clientId}/${userId}/characters/char-1/persona-v1.png`, personaVersion: 1 };
  wearResult = [
    { outfitId: 'g1', source: 'user', category: '外套', name: '夹克', garmentType: 'jacket', semanticHints: { layer_hint: 'outer' }, imageUrl: 'https://x/g1', bucket: 'media-user', path: `${clientId}/${userId}/outfits/products/g1.png`, newlyParsed: true },
  ];
});

test('happy path: stages, board storage, fixed two-image try-on, snapshots and result storage', async () => {
  const result = await runTryon({}, order());

  expect(stageUpdates).toEqual(['parsing', 'board', 'generating']);
  expect(uploads.map((u) => u.path)).toEqual([
    `${clientId}/${userId}/generations/${recordId}/board.jpg`,
    `${clientId}/${userId}/generations/${recordId}/result.png`,
  ]);

  expect(referenceCalls).toHaveLength(1);
  const call = referenceCalls[0];
  expect(call.model).toBe('img-model');
  expect(call.size).toBe('1024x1536');
  expect(call.reference_images).toHaveLength(2);
  expect(call.reference_images[0]).toContain('persona-v1.png');
  expect(call.reference_images[1]).toContain('board.jpg');
  expect(call.prompt).toContain('"cell":1');

  expect(result.personaSnapshot).toMatchObject({ character_id: 'char-1', persona_version: 1 });
  expect(result.wearSnapshot).toEqual([expect.objectContaining({ outfit_id: 'g1', source: 'user', path: wearResult[0].path })]);
  expect(result.providerSnapshot).toEqual({ model: 'img-model', size: '1024x1536' });
  expect(result.manifest).toHaveLength(1);
  expect(result.boardPath).toContain('/board.jpg');
});

test('try-on generation failure rolls back newly parsed garments and rethrows a GatewayError', async () => {
  generateShouldFail = true;
  await expect(runTryon({}, order())).rejects.toBeInstanceOf(GatewayError);
  expect(rollbackCalls).toHaveLength(1);
  // Board was stored before generation; the failed result is never uploaded.
  expect(uploads.map((u) => u.path)).toEqual([`${clientId}/${userId}/generations/${recordId}/board.jpg`]);
});

test('persona failure propagates without invoking wear rollback', async () => {
  personaShouldFail = true;
  await expect(runTryon({}, order())).rejects.toBeInstanceOf(GatewayError);
  expect(rollbackCalls).toHaveLength(0);
  expect(referenceCalls).toHaveLength(0);
});

test('a board-stage failure after parsing still rolls back newly parsed garments', async () => {
  boardShouldFail = true;
  await expect(runTryon({}, order())).rejects.toBeInstanceOf(GatewayError);
  // Rollback must run even though the failure happened during board build, before any generation.
  expect(rollbackCalls).toHaveLength(1);
  expect(referenceCalls).toHaveLength(0);
  expect(uploads).toHaveLength(0);
});
