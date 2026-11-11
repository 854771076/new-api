import { redirect } from 'next/navigation';
import { createServerSupabase } from '@/lib/supabase/server';
import { StudioClient } from './_components/StudioClient';
import { getSignedUrls } from '@/lib/media-urls';
import { config } from '@/lib/config';

export const dynamic = 'force-dynamic';

const LIMIT = 24;

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
    .eq('client_id', config.gatewayClientId)
    .eq('user_id', user.id)
    .order('created_at', { ascending: false })
    .limit(LIMIT);

  // 2. 服装：RLS 已并集返回官方服装 + 当前用户自己的个人服装（source 区分归属）。
  let outfitQuery = supabase
    .from('outfits')
    .select('id, name_zh, name_en, description_zh, description_en, category, bucket, thumb_path, source, parse_status')
    .eq('client_id', config.gatewayClientId)
    .eq('is_active', true);
  if (outfitCat) outfitQuery = outfitQuery.eq('category', outfitCat);
  if (outfitQ) {
    // ilike 组合 name_zh + name_en + description_zh + description_en
    const pattern = `%${outfitQ}%`.replaceAll('\\', '\\\\').replaceAll('"', '\\"');
    outfitQuery = outfitQuery.or(
      `name_zh.ilike."${pattern}",name_en.ilike."${pattern}",description_zh.ilike."${pattern}",description_en.ilike."${pattern}"`,
    );
  }
  const { data: outfits } = await outfitQuery.order('sort_order', { ascending: true }).order('id', { ascending: true }).limit(LIMIT);

  // 3. 姿势
  let poseQuery = supabase
    .from('poses')
    .select('id, name_zh, system, category, angle, bucket, thumb_path')
    .eq('client_id', config.gatewayClientId)
    .eq('is_active', true);
  if (poseSys) poseQuery = poseQuery.eq('system', poseSys);
  if (poseCat) poseQuery = poseQuery.eq('category', poseCat);
  if (poseAngle) poseQuery = poseQuery.eq('angle', poseAngle);
  if (poseQ) {
    const pattern = `%${poseQ}%`.replaceAll('\\', '\\\\').replaceAll('"', '\\"');
    poseQuery = poseQuery.or(
      `name_zh.ilike."${pattern}",name_en.ilike."${pattern}",description_zh.ilike."${pattern}",description_en.ilike."${pattern}"`,
    );
  }
  const { data: poses } = await poseQuery.order('sort_order', { ascending: true }).order('id', { ascending: true }).limit(LIMIT);

  // 4. 批量 signed URL（人物优先用封面图，减少列表流量）
  // 服装按行内 bucket 分组签名：官方在 media-official，个人服装在 media-user。
  const charPaths = (characters ?? []).map((c) => c.cover_path?.trim() || c.image_path);
  const posePaths = (poses ?? []).map((p) => p.thumb_path);
  const officialOutfitPaths = (outfits ?? []).filter((o) => o.bucket === 'media-official').map((o) => o.thumb_path);
  const personalOutfitPaths = (outfits ?? []).filter((o) => o.bucket !== 'media-official').map((o) => o.thumb_path);

  const [charUrls, officialOutfitUrls, personalOutfitUrls, poseUrls] = await Promise.all([
    getSignedUrls('media-user', charPaths),
    getSignedUrls('media-official', officialOutfitPaths),
    getSignedUrls('media-user', personalOutfitPaths),
    getSignedUrls('media-official', posePaths),
  ]);
  const outfitUrls: Record<string, string> = { ...officialOutfitUrls, ...personalOutfitUrls };

  // 5. category/system/angle 列表（供筛选 chips）
  const [catRows, sysRows, angleRows] = await Promise.all([
    supabase.from('outfits').select('category').eq('client_id', config.gatewayClientId).eq('is_active', true).not('category', 'is', null),
    supabase.from('poses').select('system').eq('client_id', config.gatewayClientId).eq('is_active', true).not('system', 'is', null),
    supabase.from('poses').select('angle').eq('client_id', config.gatewayClientId).eq('is_active', true).not('angle', 'is', null),
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
    .eq('usage', 'image')
    .in('client_id', [currentClientId, ''])
    .order('sort_order', { ascending: true }).order('id', { ascending: true });
  const models = modelError ? [] : (modelRows ?? []).map((m) => ({
        id: m.id as string,
        model_id: m.model_id as string,
        display_name: m.display_name as string,
        provider: m.provider as string | null,
        is_default: Boolean(m.is_default),
      }));

  return (
    <>
      {models.length === 0 ? (
        <div role="alert" className="alert alert-warning alert-soft mb-4">
          <span>{modelError ? '生成模型暂时不可用，请刷新重试；当前无法生成。' : '当前没有启用的生成模型，请联系管理员；当前无法生成。'}</span>
        </div>
      ) : null}
    <StudioClient
      characters={(characters ?? []).map((c) => ({
        id: c.id,
        name: c.name ?? '未命名',
        signedUrl: charUrls[c.cover_path?.trim() || c.image_path],
      }))}
      outfits={(outfits ?? []).map((o) => ({
        id: o.id, name: o.name_zh ?? o.name_en ?? '未命名',
        subtitle: o.category ?? undefined, signedUrl: outfitUrls[o.thumb_path],
        source: (o.source === 'user' ? 'user' : 'official') as 'official' | 'user',
        parseStatus: o.parse_status as 'pending' | 'parsing' | 'ready' | 'failed',
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
    </>
  );
}
