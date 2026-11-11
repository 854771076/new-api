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

export async function GET() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ success: false, error: { message: '未登录', code: 'UNAUTHENTICATED' } }, { status: 401 });

  const currentClientId = config.gatewayClientId;
  const { data, error } = await supabase
    .from('ai_models')
    .select('id, model_id, display_name, provider, usage, default_size, default_quality, is_default')
    .eq('is_active', true)
    .eq('usage', 'image')
    .in('client_id', [currentClientId, ''])
    .order('sort_order', { ascending: true }).order('id', { ascending: true });

  if (error) {
    return NextResponse.json({ success: false, error: { message: '生成模型暂时不可用，请稍后重试', code: 'MODELS_UNAVAILABLE' } }, { status: 503 });
  }
  const rows = (data ?? []) as AiModelRow[];

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
