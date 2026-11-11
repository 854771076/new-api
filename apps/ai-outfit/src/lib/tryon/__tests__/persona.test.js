import { beforeEach, expect, mock, test } from 'bun:test';
import sharp from 'sharp';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit', appOrigin: 'https://outfit.example' } }));

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
const characterId = 'aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa';
let calls;
let storageState;
let generateResult;
let generateError;
let signedEnabled;

const storage = {
  from() {
    return {
      async upload(path, bytes, options) {
        calls.push({ action: 'upload', path, bytes, options });
        if (!storageState.allowUpload) return { error: { message: 'denied' } };
        storageState.uploaded[path] = bytes;
        return { error: null };
      },
      async remove(paths) {
        calls.push({ action: 'remove', paths });
        for (const p of paths) delete storageState.uploaded[p];
        return { error: null };
      },
      async download(path) {
        const bytes = storageState.uploaded[path];
        return bytes ? { data: new Blob([bytes]), error: null } : { data: null, error: { message: 'missing' } };
      },
    };
  },
};

function characterTable() {
  const op = { action: 'select', input: undefined, filters: [] };
  const builder = {
    select() { return builder; },
    eq(k, v) { op.filters.push([k, v]); return builder; },
    update(input) { op.action = 'update'; op.input = input; return builder; },
    maybeSingle: execute,
    then(resolve, reject) { return execute().then(resolve, reject); },
  };
  async function execute() {
    calls.push({ table: 'characters', ...op });
    if (op.action === 'update') {
      Object.assign(storageState.character, op.input);
      return storageState.allowDb ? { data: { id: characterId }, error: null } : { data: null, error: { message: 'db down' } };
    }
    const match = op.filters.every(([k, v]) => storageState.character[k] === v);
    return { data: match ? storageState.character : null, error: null };
  }
  return builder;
}

const db = {
  from(table) {
    if (table === 'characters') return characterTable();
    throw new Error(`unexpected table ${table}`);
  },
  storage,
};

mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => db }));
mock.module('@/lib/media-urls', () => ({
  getSignedUrl: async () => (signedEnabled ? 'https://media.example/signed.png' : null),
}));
let generatedPng;
mock.module('@/lib/gateway', () => ({
  generateReferenceImage: mock(async () => {
    if (generateError) throw generateError;
    return generateResult;
  }),
}));
mock.module('@/lib/generated-image', () => ({
  readGeneratedImage: mock(async () => generatedPng),
}));
mock.module('@/lib/tryon/models', () => ({
  resolveTryonModel: mock(async () => ({ model: 'image-model', defaultSize: null })),
}));

const { normalizePersona, ensurePersona } = await import('../persona');
const { GatewayError } = await import('@/lib/gateway-errors');

function baseCharacter(overrides = {}) {
  return {
    id: characterId,
    bucket: 'media-user',
    image_path: `nac_outfit/${userId}/characters/${characterId}.png`,
    cover_path: null,
    persona_status: 'pending',
    persona_version: 0,
    ...overrides,
  };
}

const ctx = { clientId: 'nac_outfit', userId, imageModel: 'image-model' };

beforeEach(async () => {
  calls = [];
  signedEnabled = true;
  storageState = { allowUpload: true, allowDb: true, uploaded: {}, character: baseCharacter() };
  generatedPng = await sharp({ create: { width: 32, height: 48, channels: 3, background: '#888888' } }).png().toBuffer();
  generateError = null;
  generateResult = {
    images: [{ url: 'https://images.example/persona.png' }],
    usage: { total_tokens: 5 },
    request_id: 'req-1',
  };
});

test('normalizes a persona, writes cover_path and advances the version without touching the original', async () => {
  const result = await normalizePersona(db, { character: baseCharacter(), ...ctx });
  expect(result.personaVersion).toBe(1);
  expect(result.path).toBe(`nac_outfit/${userId}/characters/${characterId}/persona-v1.png`);
  const upload = calls.find((c) => c.action === 'upload');
  expect(upload.options).toEqual({ contentType: 'image/png', upsert: false });
  const meta = await sharp(upload.bytes).metadata();
  expect(meta.format).toBe('png');
  const update = calls.filter((c) => c.table === 'characters' && c.action === 'update');
  const finalUpdate = update.at(-1).input;
  expect(finalUpdate).toMatchObject({ persona_status: 'ready', persona_version: 1, persona_error: null });
  expect(finalUpdate.cover_path).toBe(result.path);
  // 原图路径保持不变。
  expect(storageState.character.image_path).toBe(baseCharacter().image_path);
});

test('ensurePersona reuses a ready persona and never calls the model', async () => {
  const ready = baseCharacter({
    persona_status: 'ready',
    persona_version: 2,
    cover_path: `nac_outfit/${userId}/characters/${characterId}/persona-v2.png`,
  });
  const result = await ensurePersona(db, { character: ready, ...ctx });
  expect(result).toMatchObject({ path: ready.cover_path, personaVersion: 2 });
  expect(calls.some((c) => c.action === 'upload')).toBe(false);
});

test('a generation failure marks persona failed and keeps the original usable', async () => {
  generateError = new GatewayError(502, 'upstream down', 'UPSTREAM_GENERATE_FAILED');
  await expect(normalizePersona(db, { character: baseCharacter(), ...ctx })).rejects.toBeInstanceOf(GatewayError);
  expect(storageState.character.persona_status).toBe('failed');
  expect(storageState.character.cover_path).toBeNull();
  expect(storageState.character.image_path).toBe(baseCharacter().image_path);
});

test('an unsigned source image fails before any model call or status flip', async () => {
  signedEnabled = false;
  await expect(normalizePersona(db, { character: baseCharacter(), ...ctx })).rejects.toMatchObject({
    code: 'AI_TRYON_PERSONA_SOURCE_MISSING',
  });
  expect(calls.some((c) => c.table === 'characters' && c.action === 'update')).toBe(false);
});

test('a DB update failure removes the orphaned object and surfaces a storage/db error', async () => {
  storageState.allowDb = false;
  await expect(normalizePersona(db, { character: baseCharacter(), ...ctx })).rejects.toMatchObject({
    code: 'UPSTREAM_DATABASE_ERROR',
  });
  // 上传成功但更新失败：孤儿对象被清理。
  expect(calls.some((c) => c.action === 'remove')).toBe(true);
  expect(Object.keys(storageState.uploaded)).toHaveLength(0);
});

test('re-normalizing replaces an older persona cover and cleans the previous version object', async () => {
  const oldPath = `nac_outfit/${userId}/characters/${characterId}/persona-v1.png`;
  storageState.uploaded[oldPath] = Buffer.from('old');
  const previous = baseCharacter({ persona_status: 'ready', persona_version: 1, cover_path: oldPath });
  const result = await normalizePersona(db, { character: previous, ...ctx });
  expect(result.personaVersion).toBe(2);
  const removed = calls.find((c) => c.action === 'remove');
  expect(removed.paths).toContain(oldPath);
  expect(storageState.uploaded[oldPath]).toBeUndefined();
});
