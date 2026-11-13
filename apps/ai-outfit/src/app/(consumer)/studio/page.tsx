import { redirect } from 'next/navigation';
import { createServerSupabase } from '@/lib/supabase/server';
import { StudioClient } from './_components/StudioClient';
import { config } from '@/lib/config';

export const dynamic = 'force-dynamic';

export default async function StudioPage({
  searchParams,
}: {
  searchParams: Promise<{
    outfit_q?: string;
    outfit_cat?: string;
    pose_q?: string;
    pose_sys?: string;
    pose_angle?: string;
  }>;
}) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const sp = await searchParams;
  const outfitQ = sp.outfit_q?.trim();
  const outfitCat = sp.outfit_cat?.trim();
  const poseQ = sp.pose_q?.trim();
  const poseSys = sp.pose_sys?.trim();
  const poseAngle = sp.pose_angle?.trim();

  // 1. category/system/angle 列表（供筛选 chips）
  const [catRows, sysRows, angleRows] = await Promise.all([
    supabase.from('outfits').select('category').eq('client_id', config.gatewayClientId).eq('is_active', true).not('category', 'is', null),
    supabase.from('poses').select('system').eq('client_id', config.gatewayClientId).eq('is_active', true).not('system', 'is', null),
    supabase.from('poses').select('angle').eq('client_id', config.gatewayClientId).eq('is_active', true).not('angle', 'is', null),
  ]);
  const outfitCats = Array.from(new Set((catRows.data ?? []).map((r) => r.category).filter(Boolean))) as string[];
  const poseSystems = Array.from(new Set((sysRows.data ?? []).map((r) => r.system).filter(Boolean))) as string[];
  const poseAngles = Array.from(new Set((angleRows.data ?? []).map((r) => r.angle).filter(Boolean))) as string[];

  // 2. 可配置生成模型（当前 client + 全局默认行）
  const currentClientId = config.gatewayClientId;
  const { data: modelRows, error: modelError } = await supabase
    .from('ai_models')
    .select('id, model_id, display_name, provider, is_default, default_size, default_quality')
    .eq('is_active', true)
    .eq('usage', 'image')
    .in('client_id', [currentClientId, ''])
    .order('sort_order', { ascending: true }).order('id', { ascending: true });
  const models = modelError ? [] : (modelRows ?? []).map((m) => ({
        id: m.id as string,
        model_id: m.model_id as string,
        display_name: m.display_name as string,
        provider: m.provider as string | null,
        is_default: Boolean(m.is_default),
        default_size: m.default_size as string | null,
        default_quality: m.default_quality as string | null,
      }));

  return (
    <>
      {models.length === 0 ? (
        <div role="alert" className="alert alert-warning alert-soft mb-4">
          <span>{modelError ? '生成模型暂时不可用，请刷新重试；当前无法生成。' : '当前没有启用的生成模型，请联系管理员；当前无法生成。'}</span>
        </div>
      ) : null}
      <StudioClient
        outfitCats={outfitCats}
        poseSystems={poseSystems}
        poseAngles={poseAngles}
        initialOutfitQ={outfitQ ?? ''}
        initialOutfitCat={outfitCat ?? ''}
        initialPoseQ={poseQ ?? ''}
        initialPoseSys={poseSys ?? ''}
        initialPoseAngle={poseAngle ?? ''}
        models={models}
      />
    </>
  );
}
