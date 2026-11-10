// GET /api/models — 可配置生成模型列表（按 client_id + sort_order）。
import 'server-only';
import { NextResponse } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { config } from '@/lib/config';

interface AiModelRow {
  id: string;
  model_id: string;
  display_name: string;
  provider: string | null;
  usage: string;
  default_size: string | null;
  default_quality: string | null;
  is_default: boolean;
}

// 迁移未执行导致表为空时的内置兜底。
const FALLBACK_MODELS = [
  {
    model_id: 'gpt-image-1',
    display_name: 'GPT Image 1',
    provider: 'openai',
    usage: 'outfit',
    is_default: true,
  },
];

export async function GET() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ success: false, error: { message: '未登录', code: 'UNAUTHENTICATED' } }, { status: 401 });

  const currentClientId = config.gatewayClientId;
  const { data, error } = await supabase
    .from('ai_models')
    .select('id, model_id, display_name, provider, usage, default_size, default_quality, is_default')
    .eq('is_active', true)
    .in('client_id', [currentClientId, ''])
    .order('sort_order', { ascending: true });

  const rows = (data ?? []) as AiModelRow[];
  if (error || rows.length === 0) {
    return NextResponse.json({ success: true, data: FALLBACK_MODELS });
  }

  return NextResponse.json({
    success: true,
    data: rows.map((m) => ({
      id: m.id,
      model_id: m.model_id,
      display_name: m.display_name,
      provider: m.provider,
      usage: m.usage,
      default_size: m.default_size,
      default_quality: m.default_quality,
      is_default: m.is_default,
    })),
  });
}
