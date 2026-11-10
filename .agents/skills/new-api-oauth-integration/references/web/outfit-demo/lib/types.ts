// lib/types.ts — 与 supabase/schema.sql 对应的业务行类型。
// description / tags / image_url 在库里可空，这里如实标注，避免用字符串型
// 强转掩盖空值。

export interface Outfit {
  id: string;
  name: string;
  category: string;
  description: string | null;
  image_url: string | null;
  tags: string[] | null;
  price_credits: number;
  is_active: boolean;
}

/** 换装页只需要展示这几个字段（server 端也按这几个列 select）。 */
export type OutfitSummary = Pick<
  Outfit,
  'id' | 'name' | 'category' | 'description' | 'price_credits'
>;

export interface AiGeneration {
  id: string;
  model: string;
  prompt: string | null;
  response: string | null;
  created_at: string;
}
