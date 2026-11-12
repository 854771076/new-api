import { expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
const { buildGarmentManifest } = await import('../manifest');

function item(overrides = {}) {
  return {
    id: overrides.id ?? 'id',
    source: overrides.source ?? 'official',
    category: overrides.category ?? '上装',
    name: overrides.name ?? 'name',
    garment_type: overrides.garment_type ?? null,
    semantic_hints: overrides.semantic_hints ?? null,
  };
}

test('orders cells by stable id independent of input order and numbers from 1', () => {
  const { entries, json } = buildGarmentManifest([
    item({ id: 'c', name: 'C' }),
    item({ id: 'a', name: 'A' }),
    item({ id: 'b', name: 'B' }),
  ]);
  expect(entries.map((e) => e.name)).toEqual(['A', 'B', 'C']);
  expect(entries.map((e) => e.cell)).toEqual([1, 2, 3]);
  // JSON 紧凑（无空白）且可解析回同样的条目。
  expect(json).not.toContain(' ');
  expect(JSON.parse(json)).toEqual(entries);
});

test('defaults missing garment_type to null and missing hints to an empty object', () => {
  const { entries } = buildGarmentManifest([item({ id: 'only' })]);
  expect(entries[0].garment_type).toBeNull();
  expect(entries[0].semantic_hints).toEqual({});
});

test('passes through source, category, garment_type and semantic hints', () => {
  const hints = { layer_hint: 'outer', color_hints: 'navy' };
  const { entries } = buildGarmentManifest([
    item({ id: 'x', source: 'user', category: '外套', garment_type: 'jacket', semantic_hints: hints }),
  ]);
  expect(entries[0]).toMatchObject({
    cell: 1,
    source: 'user',
    category: '外套',
    name: 'name',
    garment_type: 'jacket',
    semantic_hints: hints,
  });
});
