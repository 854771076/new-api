// lib/types.ts — 业务域类型；与 Supabase 表一一对应。

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
  source: 'official' | 'user';
  user_id: string | null;
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
  created_at: string;
  updated_at: string;
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
  status: 'pending' | 'succeeded' | 'failed';
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
