// lib/tryon/image.ts — 服装裁剪与 3×2 服装板拼接（sharp 实现，等价于文档的 Python 参考算法）。
import 'server-only';
import sharp, { type OverlayOptions } from 'sharp';
import { GatewayError } from '@/lib/gateway-errors';
import type { GarmentBBox } from '@/lib/types';

export const BOARD_WIDTH = 1536;
export const BOARD_HEIGHT = 1024;
export const BOARD_COLUMNS = 3;
export const BOARD_ROWS = 2;
export const MAX_BOARD_ITEMS = BOARD_COLUMNS * BOARD_ROWS;

/** 按识别框加 16% 边距裁剪，限制在原图边界内，输出 JPEG q95。 */
export async function cropGarmentReference(imageBytes: Buffer, bbox: GarmentBBox): Promise<Buffer> {
  const [left, top, right, bottom] = bbox;
  try {
    const meta = await sharp(imageBytes, { limitInputPixels: 40_000_000, animated: false }).metadata();
    const width = meta.width ?? 0;
    const height = meta.height ?? 0;
    if (!width || !height) throw new Error('missing dimensions');
    const padding = Math.round(Math.max(right - left, bottom - top) * 0.16);
    const cropLeft = Math.max(0, left - padding);
    const cropTop = Math.max(0, top - padding);
    const cropRight = Math.min(width, right + padding);
    const cropBottom = Math.min(height, bottom + padding);
    const cropWidth = cropRight - cropLeft;
    const cropHeight = cropBottom - cropTop;
    if (cropWidth <= 0 || cropHeight <= 0) throw new Error('empty crop');
    return await sharp(imageBytes, { limitInputPixels: 40_000_000, animated: false })
      .rotate()
      .extract({ left: cropLeft, top: cropTop, width: cropWidth, height: cropHeight })
      .jpeg({ quality: 95 })
      .toBuffer();
  } catch {
    throw new GatewayError(502, '服装裁剪失败，请更换图片后重试', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }
}

export interface BoardItem {
  /** 稳定排序键（用服装 UUID 的整型/字符串均可，调用方保证可比较且唯一）。 */
  id: string;
  image: Buffer;
}

export interface BoardResult {
  image: Buffer;
  /** 与格位从左到右、从上到下一致的稳定 ID 顺序，cell 从 1 开始。 */
  orderedIds: string[];
}

/**
 * 把 1–6 件服装按稳定 ID 排序后等比缩放、居中贴入 3×2 白底服装板。
 * 未使用格位留白；同一报价重跑输入顺序不变，避免模型输入漂移。
 */
export async function buildGarmentBoard(
  items: BoardItem[],
  preset: { width?: number; height?: number } = {},
): Promise<BoardResult> {
  const width = preset.width ?? BOARD_WIDTH;
  const height = preset.height ?? BOARD_HEIGHT;
  if (width <= 0 || height <= 0 || items.length < 1 || items.length > MAX_BOARD_ITEMS) {
    throw new GatewayError(400, '服装板素材数量须在 1–6 件之间', 'AI_TRYON_GARMENT_BOARD_FAILED');
  }
  const slotWidth = Math.floor(width / BOARD_COLUMNS);
  const slotHeight = Math.floor(height / BOARD_ROWS);
  const ordered = [...items].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));

  try {
    const composites: OverlayOptions[] = [];
    for (let index = 0; index < ordered.length; index += 1) {
      // fit:'inside' 只缩放到格位内、不拉伸；再居中贴到独立白底色块上，
      // 避免 sharp 0.35 对不透明输入的 contain 直接填满整个格位。
      const inner = await sharp(ordered[index].image, { limitInputPixels: 40_000_000, animated: false })
        .rotate()
        .resize(slotWidth, slotHeight, { fit: 'inside' })
        .png()
        .toBuffer();
      const innerMeta = await sharp(inner).metadata();
      const innerWidth = innerMeta.width ?? 0;
      const innerHeight = innerMeta.height ?? 0;
      const cellX = (index % BOARD_COLUMNS) * slotWidth;
      const cellY = Math.floor(index / BOARD_COLUMNS) * slotHeight;
      const cell = await sharp({
        create: { width: slotWidth, height: slotHeight, channels: 3, background: { r: 255, g: 255, b: 255 } },
      })
        .composite([
          {
            input: inner,
            left: Math.floor((slotWidth - innerWidth) / 2),
            top: Math.floor((slotHeight - innerHeight) / 2),
          },
        ])
        .png()
        .toBuffer();
      composites.push({ input: cell, left: cellX, top: cellY });
    }
    const image = await sharp({
      create: { width, height, channels: 3, background: { r: 255, g: 255, b: 255 } },
    })
      .composite(composites)
      .jpeg({ quality: 92 })
      .toBuffer();
    return { image, orderedIds: ordered.map((item) => item.id) };
  } catch (error) {
    if (error instanceof GatewayError) throw error;
    throw new GatewayError(502, '服装板生成失败，请稍后重试', 'AI_TRYON_GARMENT_BOARD_FAILED');
  }
}
