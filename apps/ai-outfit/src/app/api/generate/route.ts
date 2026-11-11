// POST /api/generate — 换装生成核心。
// 校验 → 校验模型 → 读取人物/服装/姿势 → 签参考图 URL → 渲染 prompt → 落 pending → 调网关 → 下载 → 上传 Storage → 写记录 → 返回结果。
import 'server-only';
import { type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { getSignedUrl } from '@/lib/media-urls';
import { config } from '@/lib/config';
import { generateImage, GatewayError } from '@/lib/gateway';
import { DEFAULT_NEGATIVE_PROMPT, DEFAULT_OUTFIT_TEMPLATE, renderPrompt } from '@/lib/prompt';
import type { OutfitGeneration } from '@/lib/types';
import { getGatewayAccessToken } from '@/lib/gateway-session';
import { gatewaySuccess, gatewayFailure, requireGatewayOrigin } from '@/lib/gateway-bff';
import { readGeneratedImage } from '@/lib/generated-image';
import { z } from 'zod';

export const maxDuration = 300;

const bodySchema = z.object({
  character_id: z.string().uuid(),
  outfit_ids: z.array(z.string().uuid()).min(1).max(6).refine(ids => new Set(ids).size === ids.length),
  pose_id: z.string().uuid(),
  model: z.string().min(1).max(200),
  size: z.enum(['auto', '1024x1024', '1024x1536', '1536x1024']).optional(),
  prompt_extra: z.string().max(500).optional(),
}).strict();

const SIGNED_URL_EXPIRES_IN = 3600;

interface AiModelRow {
  model_id: string;
  client_id: string;
  default_size: string | null;
}

interface PromptTemplateRow {
  template: string;
  negative_prompt: string | null;
  client_id: string;
  is_default: boolean;
}

export async function POST(req: NextRequest) {
  try { requireGatewayOrigin(req); } catch (error) { return gatewayFailure(error); }
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return gatewayFailure(new GatewayError(401, '请重新登录后生成', 'AUTH_SESSION_EXPIRED'));
  try { await getGatewayAccessToken(); } catch (error) { return gatewayFailure(error); }

  let body: z.infer<typeof bodySchema>;
  try { body = bodySchema.parse(await req.json()); }
  catch {
    return gatewayFailure(new GatewayError(400, '请选择有效素材，服装最多 6 套，提示词最多 500 字', 'INVALID_BODY'));
  }

  const currentClientId = config.gatewayClientId;

  // 0. 校验模型可用性：必须在落 pending / 调网关之前
  const { data: modelData } = await supabase
    .from('ai_models')
    .select('model_id, client_id, default_size')
    .eq('model_id', body.model)
    .eq('usage', 'outfit')
    .eq('is_active', true)
    .in('client_id', [currentClientId, '']);

  const modelRows = (modelData ?? []) as AiModelRow[];
  const modelRow = modelRows.find((m) => m.client_id === currentClientId) ?? modelRows[0];
  if (!modelRow) {
    return gatewayFailure(new GatewayError(400, '所选模型不可用', 'MODEL_NOT_AVAILABLE'));
  }

  const effectiveSize = bodySchema.shape.size.unwrap().safeParse(body.size ?? modelRow.default_size ?? '1024x1024');
  if (!effectiveSize.success) return gatewayFailure(new GatewayError(400, '模型图片尺寸配置不可用，请联系管理员', 'INVALID_IMAGE_SIZE'));

  // 1. 读取素材（service_role 能绕过 RLS；但这里 RLS 已限制 client_id 匹配）
  const { data: character } = await supabase
    .from('characters').select('id, name, image_path, bucket')
    .eq('id', body.character_id).eq('user_id', user.id).eq('client_id', currentClientId).maybeSingle();

  const { data: outfits } = await supabase
    .from('outfits').select('id, name_zh, name_en, description_zh, category, bucket, thumb_path, preview_path')
    .in('id', body.outfit_ids).eq('client_id', currentClientId).eq('is_active', true);

  const { data: pose } = await supabase
    .from('poses').select('id, name_zh, system, category, angle, bucket, thumb_path, preview_path')
    .eq('id', body.pose_id).eq('client_id', currentClientId).eq('is_active', true).maybeSingle();

  if (!character) return gatewayFailure(new GatewayError(404, '人物不存在或无权限', 'CHARACTER_NOT_FOUND'));
  if (!outfits || outfits.length !== body.outfit_ids.length) return gatewayFailure(new GatewayError(400, '所选服装已失效，请重新选择', 'OUTFIT_NOT_AVAILABLE'));
  if (!pose) return gatewayFailure(new GatewayError(404, '姿势不存在', 'POSE_NOT_FOUND'));
  outfits.sort((a, b) => body.outfit_ids.indexOf(a.id) - body.outfit_ids.indexOf(b.id));

  // 2. 签参考图 URL（bucket 取自各行：人物 media-user，服装/姿势 media-official）
  const [characterImage, poseImage] = await Promise.all([
    getSignedUrl(character.bucket, character.image_path, SIGNED_URL_EXPIRES_IN),
    getSignedUrl(pose.bucket, pose.preview_path?.trim() || pose.thumb_path, SIGNED_URL_EXPIRES_IN),
  ]);

  const resolvedOutfits: { name_zh: string; category: string; url: string | null }[] = await Promise.all(
    outfits.map(async (o) => ({
      name_zh: o.name_zh,
      category: o.category,
      url: await getSignedUrl(o.bucket, o.preview_path?.trim() || o.thumb_path, SIGNED_URL_EXPIRES_IN),
    })),
  );
  const usableOutfits: { name_zh: string; category: string; url: string }[] = [];
  for (const o of resolvedOutfits) {
    if (o.url) usableOutfits.push({ name_zh: o.name_zh, category: o.category, url: o.url });
  }

  if (!characterImage || !poseImage || usableOutfits.length !== outfits.length) {
    return gatewayFailure(new GatewayError(400, '所选素材缺少参考图，请更换素材', 'MATERIAL_IMAGE_MISSING'));
  }

  // 3. 读取提示词模板并渲染
  const { data: templateData } = await supabase
    .from('prompt_templates')
    .select('template, negative_prompt, client_id, is_default, sort_order')
    .eq('usage', 'outfit')
    .eq('is_active', true)
    .in('client_id', [currentClientId, ''])
    .order('sort_order', { ascending: true });

  const templateRows = (templateData ?? []) as PromptTemplateRow[];
  const defaultTemplates = templateRows.filter((t) => t.is_default);
  const templatePool = defaultTemplates.length > 0 ? defaultTemplates : templateRows;
  const templateRow = templatePool.find((t) => t.client_id === currentClientId) ?? templatePool[0];

  const prompt = renderPrompt(
    templateRow?.template?.trim() ? templateRow.template : DEFAULT_OUTFIT_TEMPLATE,
    {
      character: character.name ?? '',
      outfits: usableOutfits.map((o) => `${o.name_zh}（${o.category}）`).join(' + '),
      pose: `${pose.name_zh}（${pose.system}·${pose.category}·${pose.angle}）`,
      extra: body.prompt_extra ?? '',
    },
  );
  const negativePrompt = templateRow?.negative_prompt?.trim() ? templateRow.negative_prompt : DEFAULT_NEGATIVE_PROMPT;

  // 4. 先落一条 pending 记录（client_id 是 NOT NULL 且 RLS 依赖它，必须显式写入）
  const admin = createAdminSupabase();
  const { data: pendingRow, error: insertErr } = await admin
    .from('outfit_generations').insert({
      client_id: currentClientId,
      user_id: user.id,
      character_id: character.id,
      outfit_ids: body.outfit_ids,
      pose_id: pose.id,
      prompt,
      model: body.model,
      status: 'pending',
    }).select('id').single();

  if (insertErr || !pendingRow) {
    return gatewayFailure(new GatewayError(500, '无法创建生成记录，请稍后重试', 'UPSTREAM_DATABASE_ERROR'));
  }
  const recordId = pendingRow.id;

  try {
    // Reference order binds the first image to identity and the last to pose.
    const referenceImages = [characterImage, ...usableOutfits.map((o) => o.url), poseImage];
    const result = await generateImage({
      model: modelRow.model_id,
      prompt,
      size: effectiveSize.data,
      n: 1,
      negative_prompt: negativePrompt,
      reference_images: referenceImages,
    });
    const image = result.images[0];
    if (!image) throw new Error('网关返回空 images 数组');

    const bytes = await readGeneratedImage(image);
    const resultBucket = 'media-user';
    const resultPath = `${currentClientId}/${user.id}/${recordId}.png`;
    const { error: uploadError } = await createAdminSupabase().storage.from(resultBucket)
      .upload(resultPath, bytes, { contentType: 'image/png', upsert: false });
    if (uploadError) throw new GatewayError(503, '图片保存失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');

    // 7. 更新记录
    const { error: upStatErr } = await admin
      .from('outfit_generations')
      .update({
        status: 'succeeded',
        result_bucket: resultBucket,
        result_path: resultPath,
        gateway_request_id: result.request_id,
        gateway_usage: result.usage ?? null,
      } satisfies Partial<OutfitGeneration>)
      .eq('id', recordId).eq('user_id', user.id).eq('client_id', currentClientId);

    if (upStatErr) throw new Error(`更新记录失败：${upStatErr.message}`);

    return gatewaySuccess({ id: recordId, result_path: resultPath, result_bucket: resultBucket });
  } catch (error) {
    const safe = error instanceof GatewayError ? error : new GatewayError(502, '生成或图片保存失败，请稍后重试', 'UPSTREAM_GENERATE_FAILED');
    await admin.from('outfit_generations').update({ status: 'failed', error: safe.message }).eq('id', recordId).eq('user_id', user.id).eq('client_id', currentClientId);
    return gatewayFailure(safe);
  }
}
