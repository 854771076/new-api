import { beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));

const removed = [];
const updates = [];
const failStorage = [];

const admin = {
  storage: {
    from() {
      return {
        remove: mock(async (paths) => {
          for (const p of paths) {
            if (failStorage.includes(p)) throw new Error('storage down');
            removed.push(p);
          }
          return { error: null };
        }),
      };
    },
  },
  from() {
    return {
      eq() { return this; },
      update: mock(async (input) => { updates.push(input); return { error: null }; }),
    };
  },
};
mock.module('@/lib/supabase/admin', () => ({ createAdminSupabase: () => admin }));

const { rollbackNewlyParsed } = await import('../rollback');

const ctx = { clientId: 'nac_outfit', userId: 'u1' };

function wear(over) {
  return {
    outfitId: 'x', source: 'user', category: '外套', bucket: 'media-user',
    path: 'p/x.png', newlyParsed: false, ...over,
  };
}

beforeEach(() => {
  removed.length = 0;
  updates.length = 0;
  failStorage.length = 0;
});

test('resets only newly parsed personal garments and deletes their product objects', async () => {
  await rollbackNewlyParsed(admin, [
    wear({ outfitId: 'new1', newlyParsed: true, path: 'p/new1.png' }),
    wear({ outfitId: 'old1', newlyParsed: false, path: 'p/old1.png' }),
    wear({ outfitId: 'new2', source: 'user', newlyParsed: true, path: 'p/new2.png' }),
  ], ctx);
  expect(removed).toEqual(['p/new1.png', 'p/new2.png']);
  expect(updates).toHaveLength(2);
  expect(updates[0]).toMatchObject({ parse_status: 'pending', product_bucket: null, product_path: null, bbox: null });
});

test('official garments are never rolled back even if flagged', async () => {
  await rollbackNewlyParsed(admin, [
    wear({ outfitId: 'off', source: 'official', newlyParsed: true, path: 'official/x.webp' }),
  ], ctx);
  expect(removed).toEqual([]);
  expect(updates).toHaveLength(0);
});

test('a storage failure on one garment does not abort the rest', async () => {
  failStorage.push('p/bad.png');
  await expect(rollbackNewlyParsed(admin, [
    wear({ outfitId: 'bad', newlyParsed: true, path: 'p/bad.png' }),
    wear({ outfitId: 'good', newlyParsed: true, path: 'p/good.png' }),
  ], ctx)).resolves.toBeUndefined();
  expect(removed).toEqual(['p/good.png']);
});

test('empty wear list is a no-op', async () => {
  await rollbackNewlyParsed(admin, [], ctx);
  expect(removed).toEqual([]);
  expect(updates).toEqual([]);
});
