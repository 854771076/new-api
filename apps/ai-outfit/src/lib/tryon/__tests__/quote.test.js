import { expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
mock.module('@/lib/config', () => ({ config: { gatewayClientId: 'nac_outfit' } }));

const { buildQuote, needsParsing } = await import('../quote');

const userId = '48e59578-1c61-489b-8a71-40a2019b28bd';

function official(id) {
  return { id, source: 'official', user_id: null, parse_status: 'ready', product_bucket: null, product_path: null };
}
function readyPersonal(id) {
  return { id, source: 'user', user_id: userId, parse_status: 'ready', product_bucket: 'media-user', product_path: `p/${id}.png` };
}
function pendingPersonal(id) {
  return { id, source: 'user', user_id: userId, parse_status: 'pending', product_bucket: null, product_path: null };
}
function failedPersonal(id) {
  return { id, source: 'user', user_id: userId, parse_status: 'failed', product_bucket: null, product_path: null };
}

test('only unparsed personal garments need parsing', () => {
  expect(needsParsing(official('o1'))).toBe(false);
  expect(needsParsing(readyPersonal('r1'))).toBe(false);
  expect(needsParsing(pendingPersonal('p1'))).toBe(true);
  expect(needsParsing(failedPersonal('f1'))).toBe(true);
});

test('a ready personal garment missing its product path is treated as unparsed', () => {
  expect(needsParsing({ source: 'user', parse_status: 'ready', product_bucket: 'media-user', product_path: null })).toBe(true);
});

test('quote counts only unparsed personal garments and records item count', () => {
  const quote = buildQuote([official('o1'), readyPersonal('r1'), pendingPersonal('p1'), failedPersonal('f1')]);
  expect(quote.item_count).toBe(4);
  expect(quote.unparsed_garment_ids).toEqual(['p1', 'f1']);
  // Default configured unit fees are zero; structure stays stable when operators raise them.
  expect(quote).toMatchObject({ parse_fee_quota: 0, tryon_fee_quota: 0, total_quota: 0 });
});

test('all-official or all-ready selections carry no parse fee', () => {
  const quote = buildQuote([official('o1'), readyPersonal('r1')]);
  expect(quote.unparsed_garment_ids).toEqual([]);
  expect(quote.parse_fee_quota).toBe(0);
});
