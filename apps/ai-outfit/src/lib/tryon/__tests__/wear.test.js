import { beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit' } }));

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';
let signFailPaths;
let parsedIds;

mock.module('@/lib/media-urls', () => ({
  getSignedUrl: mock(async (_bucket, path) => (signFailPaths.includes(path) ? null : `https://media.example/${encodeURIComponent(path)}`)),
}));
mock.module('@/lib/tryon/parse-garment', () => ({
  parsePersonalGarment: mock(async (_db, garment) => {
    parsedIds.push(garment.id);
    return {
      id: garment.id,
      productBucket: 'media-user',
      productPath: `nac_outfit/${userId}/outfits/products/${garment.id}.png`,
      garmentType: 'jacket',
      bbox: [1, 2, 3, 4],
      semanticHints: { layer_hint: 'outer' },
    };
  }),
}));

const { resolveWearItems, toManifestInputs } = await import('../wear');
const { GatewayError } = await import('@/lib/gateway-errors');

const ctx = { clientId: 'nac_outfit', userId };

function official(id) {
  return {
    id, source: 'official', user_id: null, category: '外套', name_zh: '官方夹克',
    bucket: 'media-official', thumb_path: `official/${id}.webp`, preview_path: `official/${id}-hd.webp`,
    parse_status: 'ready', product_bucket: null, product_path: null,
    garment_type: null, semantic_hints: null, original_bucket: null, original_path: null, parse_attempts: 0,
  };
}
function personal(id, status = 'pending') {
  const productPath = `nac_outfit/${userId}/outfits/products/${id}.png`;
  return {
    id, source: 'user', user_id: userId, category: '外套', name_zh: '我的夹克',
    bucket: 'media-user', thumb_path: `nac_outfit/${userId}/outfits/${id}.png`,
    preview_path: `nac_outfit/${userId}/outfits/${id}.png`,
    parse_status: status,
    product_bucket: status === 'ready' ? 'media-user' : null,
    product_path: status === 'ready' ? productPath : null,
    garment_type: status === 'ready' ? 'jacket' : null,
    semantic_hints: status === 'ready' ? { layer_hint: 'outer' } : null,
    original_bucket: 'media-user', original_path: `nac_outfit/${userId}/outfits/${id}.png`, parse_attempts: 0,
  };
}

beforeEach(() => {
  signFailPaths = [];
  parsedIds = [];
});

test('official garments use their production image directly without parsing', async () => {
  const [wear] = await resolveWearItems({}, [official('o1')], ctx);
  expect(wear).toMatchObject({ source: 'official', newlyParsed: false, bucket: 'media-official' });
  expect(wear.path).toBe('official/o1-hd.webp');
  expect(parsedIds).toHaveLength(0);
});

test('a ready personal garment reuses its product image and never re-parses', async () => {
  const [wear] = await resolveWearItems({}, [personal('p1', 'ready')], ctx);
  expect(wear).toMatchObject({ source: 'user', newlyParsed: false, garmentType: 'jacket' });
  expect(wear.path).toContain('/outfits/products/p1.png');
  expect(parsedIds).toHaveLength(0);
});

test('an unparsed personal garment is parsed on demand and flagged newlyParsed', async () => {
  const [wear] = await resolveWearItems({}, [personal('p2', 'pending')], ctx);
  expect(parsedIds).toEqual(['p2']);
  expect(wear).toMatchObject({ source: 'user', newlyParsed: true, garmentType: 'jacket' });
});

test('mixed official + reused + newly parsed garments all resolve in one order', async () => {
  const wear = await resolveWearItems({}, [personal('p2', 'pending'), official('o1'), personal('p1', 'ready')], ctx);
  expect(parsedIds).toEqual(['p2']);
  expect(wear.map((w) => w.outfitId)).toEqual(['p2', 'o1', 'p1']);
  expect(wear.map((w) => w.newlyParsed)).toEqual([true, false, false]);
});

test('a missing official production image fails closed with a tryon source error', async () => {
  const o = official('o9');
  signFailPaths = [o.preview_path];
  await expect(resolveWearItems({}, [o], ctx)).rejects.toBeInstanceOf(GatewayError);
});

test('manifest inputs carry source, category, garment type and hints keyed by outfit id', async () => {
  const wear = await resolveWearItems({}, [personal('p1', 'ready'), official('o1')], ctx);
  const inputs = toManifestInputs(wear);
  expect(inputs).toEqual([
    expect.objectContaining({ id: 'p1', source: 'user', garment_type: 'jacket' }),
    expect.objectContaining({ id: 'o1', source: 'official', garment_type: null }),
  ]);
});
