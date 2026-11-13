import { beforeEach, expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
let nextContent;
let nextError;
let capturedMessages;
mock.module('@/lib/gateway', () => ({
  visionChatCompletion: mock(async (_model, messages) => {
    capturedMessages = messages;
    if (nextError) throw nextError;
    return nextContent;
  }),
}));
const { recognizeGarment } = await import('../recognize');
const { GatewayError } = await import('@/lib/gateway-errors');

const baseParams = {
  model: 'vision-1',
  imageUrl: 'https://example.test/signed.png',
  expectedCategory: '上装',
  width: 400,
  height: 600,
};

function payload(overrides = {}) {
  return {
    category: '上装',
    garment_type: 'T-shirt',
    bbox: [10, 20, 200, 300],
    semantic_hints: { layer_hint: 'inner', color_hints: 'white' },
    ...overrides,
  };
}

beforeEach(() => {
  nextContent = JSON.stringify(payload());
  nextError = null;
  capturedMessages = null;
});

test('parses a valid response, rounds bbox and forces the expected category', async () => {
  nextContent = '```json\n' + JSON.stringify(payload({ bbox: [10.4, 20.6, 200, 300] })) + '\n```';
  const result = await recognizeGarment(baseParams);
  expect(result).toMatchObject({
    category: '上装',
    garment_type: 'T-shirt',
    bbox: [10, 21, 200, 300],
  });
  expect(result.semantic_hints.layer_hint).toBe('inner');
  expect(result.semantic_hints.color_hints).toBe('white');
  // 多模态消息携带图片且坐标尺寸写入提示词。
  const user = capturedMessages[1];
  expect(user.content[1]).toEqual({ type: 'image_url', image_url: { url: baseParams.imageUrl } });
  expect(user.content[0].text).toContain('400');
  expect(user.content[0].text).toContain('600');
});

test('extracts JSON even when the model wraps it in prose and fences', async () => {
  nextContent = 'Here is the result:\n```json\n' + JSON.stringify(payload()) + '\n```\nDone.';
  const result = await recognizeGarment(baseParams);
  expect(result.bbox).toEqual([10, 20, 200, 300]);
});

test('rejects unparseable model output with the recognition error code', async () => {
  nextContent = 'not json at all';
  expect(recognizeGarment(baseParams)).rejects.toMatchObject({ code: 'AI_TRYON_GARMENT_RECOGNITION_FAILED' });
});

test.each([
  [[-1, 20, 200, 300]],
  [[10, 20, 401, 300]],
  [[10, 20, 200, 601]],
  [[200, 20, 10, 300]],
  [[10, 300, 200, 100]],
])('rejects out-of-bounds or inverted bbox %p', async (bbox) => {
  nextContent = JSON.stringify(payload({ bbox }));
  expect(recognizeGarment(baseParams)).rejects.toBeInstanceOf(GatewayError);
});

test('maps an invalid layer and socks to unknown', async () => {
  nextContent = JSON.stringify(payload({
    garment_type: 'socks',
    semantic_hints: { layer_hint: 'lower' },
  }));
  const result = await recognizeGarment(baseParams);
  expect(result.semantic_hints.layer_hint).toBe('unknown');

  nextContent = JSON.stringify(payload({ semantic_hints: { layer_hint: 'not-a-layer' } }));
  const second = await recognizeGarment(baseParams);
  expect(second.semantic_hints.layer_hint).toBe('unknown');
});

test('surfaces a gateway failure with the recognition error code', async () => {
  nextError = new Error('network down');
  expect(recognizeGarment(baseParams)).rejects.toMatchObject({ code: 'AI_TRYON_GARMENT_RECOGNITION_FAILED' });
});

test('rejects invalid source dimensions before calling the model', async () => {
  expect(recognizeGarment({ ...baseParams, width: 0 })).rejects.toBeInstanceOf(GatewayError);
  expect(capturedMessages).toBeNull();
});
