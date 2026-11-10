import { redirect } from 'next/navigation';
import Link from 'next/link';
import { createServerSupabase } from '@/lib/supabase/server';
import { StudioClient } from './_components/StudioClient';
import { getSignedUrls } from '@/lib/media-urls';
import { config } from '@/lib/config';

export const dynamic = 'force-dynamic';

const LIMIT = 24;

const FALLBACK_MODELS = [
  { id: 'fallback', model_id: 'gpt-image-1', display_name: 'GPT Image 1', provider: 'openai', is_default: true },
];

export default async function StudioPage({
  searchParams,
}: {
  searchParams: Promise<{
    outfit_q?: string;
    outfit_cat?: string;
    pose_q?: string;
    pose_sys?: string;
    pose_cat?: string;
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
  const poseCat = sp.pose_cat?.trim();
  const poseAngle = sp.pose_angle?.trim();

  // 1. 人物（当前用户自己上传的）
  const { data: characters } = await supabase
    .from('characters')
    .select('id, name, image_path, cover_path, bucket')
    .eq('user_id', user.id)
    .order('created_at', { ascending: false })
    .limit(LIMIT);

  // 2. 服装
  let outfitQuery = supabase
    .from('outfits')
    .select('id, name_zh, name_en, description_zh, description_en, category, bucket, thumb_path')
    .eq('is_active', true);
  if (outfitCat) outfitQuery = outfitQuery.eq('category', outfitCat);
  if (outfitQ) {
    // ilike 组合 name_zh + name_en + description_zh + description_en
    const pattern = `%${outfitQ}%`;
    outfitQuery = outfitQuery.or(
      `name_zh.ilike.${pattern},name_en.ilike.${pattern},description_zh.ilike.${pattern},description_en.ilike.${pattern}`,
    );
  }
  const { data: outfits } = await outfitQuery.order('sort_order', { ascending: true }).limit(LIMIT);

  // 3. 姿势
  let poseQuery = supabase
    .from('poses')
    .select('id, name_zh, system, category, angle, bucket, thumb_path')
    .eq('is_active', true);
  if (poseSys) poseQuery = poseQuery.eq('system', poseSys);
  if (poseCat) poseQuery = poseQuery.eq('category', poseCat);
  if (poseAngle) poseQuery = poseQuery.eq('angle', poseAngle);
  if (poseQ) {
    const pattern = `%${poseQ}%`;
    poseQuery = poseQuery.or(
      `name_zh.ilike.${pattern},name_en.ilike.${pattern},description_zh.ilike.${pattern},description_en.ilike.${pattern}`,
    );
  }
  const { data: poses } = await poseQuery.order('sort_order', { ascending: true }).limit(LIMIT);

  // 4. 批量 signed URL（人物优先用封面图，减少列表流量）
  const charPaths = (characters ?? []).map((c) => c.cover_path?.trim() || c.image_path);
  const outfitPaths = (outfits ?? []).map((o) => o.thumb_path);
  const posePaths = (poses ?? []).map((p) => p.thumb_path);

  const [charUrls, outfitUrls, poseUrls] = await Promise.all([
    getSignedUrls('media-user', charPaths),
    getSignedUrls('media-official', outfitPaths),
    getSignedUrls('media-official', posePaths),
  ]);

  // 5. category/system/angle 列表（供筛选 chips）
  const [catRows, sysRows, angleRows] = await Promise.all([
    supabase.from('outfits').select('category').eq('is_active', true).not('category', 'is', null),
    supabase.from('poses').select('system').eq('is_active', true).not('system', 'is', null),
    supabase.from('poses').select('angle').eq('is_active', true).not('angle', 'is', null),
  ]);
  const outfitCats = Array.from(new Set((catRows.data ?? []).map((r) => r.category).filter(Boolean))) as string[];
  const poseSystems = Array.from(new Set((sysRows.data ?? []).map((r) => r.system).filter(Boolean))) as string[];
  const poseAngles = Array.from(new Set((angleRows.data ?? []).map((r) => r.angle).filter(Boolean))) as string[];

  // 6. 可配置生成模型（当前 client + 全局默认行）
  const currentClientId = config.gatewayClientId;
  const { data: modelRows, error: modelError } = await supabase
    .from('ai_models')
    .select('id, model_id, display_name, provider, is_default')
    .eq('is_active', true)
    .in('client_id', [currentClientId, ''])
    .order('sort_order', { ascending: true });
  const models = !modelError && (modelRows ?? []).length > 0
    ? (modelRows ?? []).map((m) => ({
        id: m.id as string,
        model_id: m.model_id as string,
        display_name: m.display_name as string,
        provider: m.provider as string | null,
        is_default: Boolean(m.is_default),
      }))
    : FALLBACK_MODELS;

  return (
    <StudioClient
      characters={(characters ?? []).map((c) => ({
        id: c.id,
        name: c.name ?? '未命名',
        signedUrl: charUrls[c.cover_path?.trim() || c.image_path],
      }))}
      outfits={(outfits ?? []).map((o) => ({
        id: o.id, name: o.name_zh ?? o.name_en ?? '未命名',
        subtitle: o.category ?? undefined, signedUrl: outfitUrls[o.thumb_path],
      }))}
      poses={(poses ?? []).map((p) => ({
        id: p.id, name: p.name_zh ?? '未命名',
        subtitle: `${p.system ?? ''}${p.category ? ' · ' + p.category : ''}${p.angle ? ' · ' + p.angle : ''}`.trim(),
        signedUrl: poseUrls[p.thumb_path],
      }))}
      outfitCats={outfitCats}
      poseSystems={poseSystems}
      poseAngles={poseAngles}
      initialOutfitQ={outfitQ ?? ''}
      initialOutfitCat={outfitCat ?? ''}
      initialPoseQ={poseQ ?? ''}
      initialPoseSys={poseSys ?? ''}
      initialPoseCat={poseCat ?? ''}
      initialPoseAngle={poseAngle ?? ''}
      models={models}
    />
  );
}

// 避免 ESLint 抱怨未使用的 Link
void Link;
