// lib/types.ts — 业务域类型；与 Supabase 表一一对应。

export type GarmentSource = 'official' | 'user';
export type GarmentParseStatus = 'pending' | 'parsing' | 'ready' | 'failed';
export type PersonaStatus = 'pending' | 'normalizing' | 'ready' | 'failed';
export type GenerationStage = 'pending' | 'quoted' | 'parsing' | 'board' | 'generating' | 'moderating' | 'done' | 'failed';
export type GenerationStatus = 'pending' | 'quoting' | 'parsing' | 'generating' | 'moderating' | 'succeeded' | 'failed';

/** 服装识别 bbox，原图像素坐标 [left, top, right, bottom]。 */
export type GarmentBBox = [number, number, number, number];

/** 服装识别返回的语义画像，字段均可为空字符串。 */
export interface GarmentSemanticHints {
  layer_hint?: string;
  color_hints?: string;
  material_hints?: string;
  texture_hints?: string;
  pattern_hints?: string;
  fit_hint?: string;
  length_hint?: string;
  sleeve_hint?: string;
  neckline_hint?: string;
  closure_hints?: string;
  detail_hints?: string;
  [key: string]: string | undefined;
}

export interface Outfit {
  id: string;
  client_id: string;
  provider_id: string;
  category: string;
  tags: string[];
  name_zh: string;
  name_en: string | null;
  description_zh: string | null;
  description_en: string | null;
  keywords: string[];
  bucket: string;
  thumb_path: string;
  preview_path: string;
  source: GarmentSource;
  user_id: string | null;
  parse_status: GarmentParseStatus;
  original_bucket: string | null;
  original_path: string | null;
  product_bucket: string | null;
  product_path: string | null;
  garment_type: string | null;
  bbox: GarmentBBox | null;
  semantic_hints: GarmentSemanticHints;
  parse_error: string | null;
  parse_attempts: number;
  sort_order: number;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface Pose {
  id: string;
  client_id: string;
  provider_id: string;
  pose_set: string;
  system: string;
  category: string;
  angle: string;
  name_zh: string;
  name_en: string | null;
  description_zh: string | null;
  description_en: string | null;
  bucket: string;
  thumb_path: string;
  preview_path: string;
  sort_order: number;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface Character {
  id: string;
  client_id: string;
  user_id: string;
  name: string;
  bucket: string;
  image_path: string;
  cover_path: string | null;
  description: string | null;
  is_public: boolean;
  persona_status: PersonaStatus;
  persona_version: number;
  persona_error: string | null;
  created_at: string;
  updated_at: string;
}

/** 提交前报价：本次未解析个人服装的解析费 + 一次换装制作费，单位均为网关 quota。 */
export interface GenerationQuote {
  parse_fee_quota: number;
  tryon_fee_quota: number;
  total_quota: number;
  unparsed_garment_ids: string[];
  item_count: number;
  [key: string]: unknown;
}

/** 与服装板格位一一对应的清单条目，cell 从 1 开始，顺序与拼板稳定排序一致。 */
export interface GarmentManifestEntry {
  cell: number;
  source: GarmentSource;
  category: string;
  name: string;
  garment_type?: string | null;
  semantic_hints: GarmentSemanticHints;
}

/** 任务冻结的人物资产快照，恢复任务时按版本取图，不读取可能已变更的现表。 */
export interface PersonaSnapshot {
  character_id: string;
  persona_version: number;
  bucket: string;
  path: string;
  [key: string]: unknown;
}

/** 任务冻结的姿势参考快照，恢复任务时按路径取图，不读取可能已变更的现表。 */
export interface PoseSnapshot {
  pose_id: string;
  bucket: string;
  path: string;
  [key: string]: unknown;
}

/** 任务冻结的单件服装选择快照。 */
export interface WearSnapshotEntry {
  outfit_id: string;
  source: GarmentSource;
  category: string;
  bucket: string;
  path: string;
  parse_version?: number;
  [key: string]: unknown;
}

/** 任务冻结的模型与生成参数快照。 */
export interface ProviderSnapshot {
  model: string;
  provider?: string;
  size?: string;
  quality?: string;
  vision_model?: string;
  [key: string]: unknown;
}

export interface OutfitGeneration {
  id: string;
  client_id: string;
  user_id: string;
  character_id: string | null;
  outfit_ids: string[];
  pose_id: string | null;
  prompt: string | null;
  model: string;
  status: GenerationStatus;
  stage: GenerationStage;
  idempotency_key: string | null;
  quote: GenerationQuote;
  quote_accepted_at: string | null;
  garment_manifest: GarmentManifestEntry[];
  board_bucket: string | null;
  board_path: string | null;
  persona_snapshot: PersonaSnapshot | null;
  pose_snapshot: PoseSnapshot | null;
  wear_snapshot: WearSnapshotEntry[] | null;
  provider_snapshot: ProviderSnapshot | null;
  pipeline_version: string | null;
  parse_fee_quota: number;
  tryon_fee_quota: number;
  charged_quota: number;
  refunded_quota: number;
  result_bucket: string | null;
  result_path: string | null;
  error: string | null;
  gateway_request_id: string | null;
  gateway_usage: unknown | null;
  created_at: string;
  updated_at: string;
}

export interface PricePlan {
  id: string;
  client_id: string;
  name_zh: string;
  name_en: string | null;
  description: string | null;
  price_quota: number;
  model: string;
  is_active: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

export interface GatewayUserSelf {
  id: string;
  username?: string;
  display_name?: string;
  email?: string;
  role: number;
  status: number;
  quota: number;
  used_quota: number;
  request_count: number;
}
