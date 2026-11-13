import { expect, mock, test } from 'bun:test';

mock.module('server-only', () => ({}));
import sharp from 'sharp';
const { cropGarmentReference, buildGarmentBoard, BOARD_WIDTH, BOARD_HEIGHT, MAX_BOARD_ITEMS } = await import('../image');
const { GatewayError } = await import('@/lib/gateway-errors');

async function png(width, height, background = '#123456') {
  return sharp({ create: { width, height, channels: 3, background } }).png().toBuffer();
}

test('crop adds 16% padding, clamps to image bounds and emits JPEG', async () => {
  const source = await png(100, 100);
  // bbox 20,20 -> 60,60：边长 40，padding=round(40*0.16)=6，夹到边界后 14..66。
  const out = await cropGarmentReference(source, [20, 20, 60, 60]);
  const meta = await sharp(out).metadata();
  expect(meta.format).toBe('jpeg');
  expect([meta.width, meta.height]).toEqual([52, 52]);
});

test('crop clamps a padded box that would overflow the original', async () => {
  const source = await png(50, 50);
  // bbox 贴着右下：padding 后右/下被夹到 50，左/上不小于 0。
  const out = await cropGarmentReference(source, [40, 40, 50, 50]);
  const meta = await sharp(out).metadata();
  expect(meta.width).toBeLessThanOrEqual(50);
  expect(meta.height).toBeLessThanOrEqual(50);
  expect(meta.width).toBeGreaterThan(0);
  expect(meta.height).toBeGreaterThan(0);
});

test('crop rejects an inverted bbox with the recognition error code', async () => {
  const source = await png(60, 60);
  expect(cropGarmentReference(source, [50, 50, 10, 10])).rejects.toBeInstanceOf(GatewayError);
});

test('board rejects empty and over-capacity item sets', async () => {
  const items = await Promise.all(
    Array.from({ length: MAX_BOARD_ITEMS + 1 }, (_, i) => png(40, 40, '#abcdef').then(image => ({ id: `id-${i}`, image }))),
  );
  expect(buildGarmentBoard([])).rejects.toMatchObject({ code: 'AI_TRYON_GARMENT_BOARD_FAILED' });
  expect(buildGarmentBoard(items)).rejects.toMatchObject({ code: 'AI_TRYON_GARMENT_BOARD_FAILED' });
});

test('board is 1536x1024, white background, and orders cells by stable id', async () => {
  const ids = ['c', 'a', 'b'];
  const items = await Promise.all(ids.map(id => png(80, 120, '#336699').then(image => ({ id, image }))));
  const result = await buildGarmentBoard(items);
  const meta = await sharp(result.image).metadata();
  expect([meta.width, meta.height, meta.format]).toEqual([BOARD_WIDTH, BOARD_HEIGHT, 'jpeg']);
  // 稳定排序：a,b,c，与传入顺序无关。
  expect(result.orderedIds).toEqual(['a', 'b', 'c']);
  // 用原始像素断言（本环境 libvips 的 extract().stats() 通道最小值不可靠）：
  // 3 件只占第一行，第二行左侧留白格位应为接近纯白。
  const { data, info } = await sharp(result.image).raw().toBuffer({ resolveWithObject: true });
  const px = (x, y) => {
    const i = (y * info.width + x) * info.channels;
    return [data[i], data[i + 1], data[i + 2]];
  };
  const white = px(40, 600);
  expect(Math.min(white[0], white[1], white[2])).toBeGreaterThan(245);
});

test('a single item still fills one cell on a full-size board', async () => {
  const items = [{ id: 'only', image: await png(100, 100, '#ff0000') }];
  const result = await buildGarmentBoard(items);
  const meta = await sharp(result.image).metadata();
  expect([meta.width, meta.height]).toEqual([BOARD_WIDTH, BOARD_HEIGHT]);
  expect(result.orderedIds).toEqual(['only']);
});
