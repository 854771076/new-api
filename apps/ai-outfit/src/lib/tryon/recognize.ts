// lib/tryon/recognize.ts — 个人服装按需识别：视觉 LLM 返回结构化 JSON，服务端校验坐标与类别。
import 'server-only';
import { z } from 'zod';
import { visionChatCompletion, type VisionMessage } from '@/lib/gateway';
import { GatewayError } from '@/lib/gateway-errors';
import {
  GARMENT_RECOGNITION_SYSTEM_PROMPT,
  GARMENT_RECOGNITION_USER_TEMPLATE,
} from '@/lib/tryon/prompts';
import type { GarmentBBox, GarmentSemanticHints } from '@/lib/types';

const LAYER_HINTS = new Set(['inner', 'middle', 'outer', 'one_piece', 'lower', 'footwear', 'unknown']);

const hintValue = z.string().trim().max(200);
const recognitionSchema = z.object({
  category: z.string().trim().min(1).max(50),
  garment_type: z.string().trim().min(1).max(100),
  bbox: z.tuple([z.number(), z.number(), z.number(), z.number()]),
  semantic_hints: z.object({
    layer_hint: z.string().trim().max(40).optional(),
    color_hints: hintValue.optional(),
    material_hints: hintValue.optional(),
    texture_hints: hintValue.optional(),
    pattern_hints: hintValue.optional(),
    fit_hint: hintValue.optional(),
    length_hint: hintValue.optional(),
    sleeve_hint: hintValue.optional(),
    neckline_hint: hintValue.optional(),
    closure_hints: hintValue.optional(),
    detail_hints: hintValue.optional(),
  }).passthrough(),
}).passthrough();

export interface RecognitionResult {
  category: string;
  garment_type: string;
  bbox: GarmentBBox;
  semantic_hints: GarmentSemanticHints;
}

/** 去掉模型常加的 ```json 围栏，截取首个 JSON 对象。 */
function extractJson(text: string): unknown {
  const cleaned = text.replace(/```json/gi, '').replace(/```/g, '').trim();
  const start = cleaned.indexOf('{');
  const end = cleaned.lastIndexOf('}');
  if (start < 0 || end <= start) throw new Error('no json object');
  return JSON.parse(cleaned.slice(start, end + 1));
}

/** 已知易错点：socks 不得标记为下装；非法 layer 归一为 unknown。 */
function normalizeLayer(layer: string | undefined, garmentType: string): GarmentSemanticHints['layer_hint'] {
  const type = garmentType.toLowerCase();
  if (type.includes('sock')) return 'unknown';
  if (!layer || !LAYER_HINTS.has(layer)) return 'unknown';
  return layer;
}

/**
 * 调用视觉 LLM 识别单件服装。
 * @param model usage='vision' 的模型 id
 * @param imageUrl 原图的 HTTPS signed URL（Storage RLS 已限定 owner/管理员可读）
 * @param expectedCategory 用户选择的粗分类（写入提示词并回填，避免模型漂移）
 * @param dimensions 原图宽高，用于 bbox 坐标边界校验
 */
export async function recognizeGarment(params: {
  model: string;
  imageUrl: string;
  expectedCategory: string;
  width: number;
  height: number;
}): Promise<RecognitionResult> {
  const { model, imageUrl, expectedCategory, width, height } = params;
  if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
    throw new GatewayError(400, '服装原图尺寸无效，无法识别', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }
  const userPrompt = GARMENT_RECOGNITION_USER_TEMPLATE
    .replace('{category}', expectedCategory)
    .replaceAll('{width}', String(width))
    .replaceAll('{height}', String(height));

  const messages: VisionMessage[] = [
    { role: 'system', content: GARMENT_RECOGNITION_SYSTEM_PROMPT },
    {
      role: 'user',
      content: [
        { type: 'text', text: userPrompt },
        { type: 'image_url', image_url: { url: imageUrl } },
      ],
    },
  ];

  let content: string;
  try {
    content = await visionChatCompletion(model, messages);
  } catch {
    throw new GatewayError(502, '服装识别服务暂时不可用，请稍后重试', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }

  let parsed: z.infer<typeof recognitionSchema>;
  try {
    const candidate = recognitionSchema.safeParse(extractJson(content));
    if (!candidate.success) throw new Error('schema mismatch');
    parsed = candidate.data;
  } catch {
    throw new GatewayError(502, '服装识别结果无法解析，请更换图片后重试', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }

  const [left, top, right, bottom] = parsed.bbox;
  const integers: number[] = [left, top, right, bottom].map(Number);
  const validBox = integers.every((n) => Number.isFinite(n)) &&
    integers[0] >= 0 && integers[1] >= 0 &&
    integers[2] <= width && integers[3] <= height &&
    integers[0] < integers[2] && integers[1] < integers[3];
  if (!validBox) {
    throw new GatewayError(502, '服装识别坐标越界，请更换图片后重试', 'AI_TRYON_GARMENT_RECOGNITION_FAILED');
  }
  const bbox = integers.map((n) => Math.round(n)) as GarmentBBox;

  const hints: GarmentSemanticHints = {};
  for (const [key, value] of Object.entries(parsed.semantic_hints)) {
    if (typeof value === 'string' && value.trim()) hints[key] = value.trim();
  }
  hints.layer_hint = normalizeLayer(hints.layer_hint, parsed.garment_type);

  return {
    category: expectedCategory,
    garment_type: parsed.garment_type.trim(),
    bbox,
    semantic_hints: hints,
  };
}
