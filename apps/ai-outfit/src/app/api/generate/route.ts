// POST /api/generate — 换装生成核心。
// 校验 → 校验模型 → 读取人物/服装/姿势 → 签参考图 URL → 渲染 prompt → 落 pending → 调网关 → 下载 → 上传 Storage → 写记录 → 返回结果。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { getSignedUrl } from '@/lib/media-urls';
import { config } from '@/lib/config';
import { generateImage, GatewayError } from '@/lib/gateway';
import { DEFAULT_NEGATIVE_PROMPT, DEFAULT_OUTFIT_TEMPLATE, renderPrompt } from '@/lib/prompt';
import type { OutfitGeneration } from '@/lib/types';
import { z } from 'zod';

const bodySchema = z.object({
  character_id: z.string().uuid(),
  outfit_ids: z.array(z.string().uuid()).min(1),
  pose_id: z.string().uuid(),
  model: z.string().min(1).default('gpt-image-1'),
  size: z.string().optional(),
  prompt_extra: z.string().max(500).optional(),
});

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
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ success: false, error: { message: '未登录', code: 'UNAUTHENTICATED' } }, { status: 401 });

  // 网关 OAuth access_token 存在 Supabase session 的 provider_token 里；
  // 服务端没有浏览器 sessionStorage，必须显式取出后透传给网关。
  const { data: { session } } = await supabase.auth.getSession();
  const gatewayToken = session?.provider_token;
  if (!gatewayToken) {
    return NextResponse.json(
      { success: false, error: { message: '网关授权令牌缺失，请重新登录', code: 'GATEWAY_TOKEN_MISSING' } },
      { status: 401 },
    );
  }

  let body: z.infer<typeof bodySchema>;
  try { body = bodySchema.parse(await req.json()); }
  catch (e) {
    return NextResponse.json(
      { success: false, error: { message: e instanceof Error ? e.message : '请求格式错误', code: 'INVALID_BODY' } },
      { status: 400 },
    );
  }

  const currentClientId = config.gatewayClientId;

  // 0. 校验模型可用性：必须在落 pending / 调网关之前
  const { data: modelData } = await supabase
    .from('ai_models')
    .select('model_id, client_id, default_size')
    .eq('model_id', body.model)
    .eq('is_active', true)
    .in('client_id', [currentClientId, '']);

  const modelRows = (modelData ?? []) as AiModelRow[];
  const modelRow = modelRows.find((m) => m.client_id === currentClientId) ?? modelRows[0];
  if (!modelRow) {
    return NextResponse.json(
      { success: false, error: { message: '所选模型不可用', code: 'MODEL_NOT_AVAILABLE' } },
      { status: 400 },
    );
  }

  // 1. 读取素材（service_role 能绕过 RLS；但这里 RLS 已限制 client_id 匹配）
  const { data: character } = await supabase
    .from('characters').select('id, name, image_path, bucket')
    .eq('id', body.character_id).eq('user_id', user.id).maybeSingle();

  const { data: outfits } = await supabase
    .from('outfits').select('id, name_zh, name_en, description_zh, category, bucket, thumb_path, preview_path')
    .in('id', body.outfit_ids).eq('is_active', true);

  const { data: pose } = await supabase
    .from('poses').select('id, name_zh, system, category, angle, bucket, thumb_path, preview_path')
    .eq('id', body.pose_id).eq('is_active', true).maybeSingle();

  if (!character) return NextResponse.json({ success: false, error: { message: '人物不存在或无权限', code: 'CHARACTER_NOT_FOUND' } }, { status: 404 });
  if (!outfits || outfits.length === 0) return NextResponse.json({ success: false, error: { message: '请选择至少一套服装', code: 'OUTFIT_EMPTY' } }, { status: 400 });
  if (!pose) return NextResponse.json({ success: false, error: { message: '姿势不存在', code: 'POSE_NOT_FOUND' } }, { status: 404 });

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

  if (!characterImage || !poseImage || usableOutfits.length === 0) {
    return NextResponse.json(
      { success: false, error: { message: '人物、服装或姿势缺少可用的参考图', code: 'MATERIAL_IMAGE_MISSING' } },
      { status: 400 },
    );
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
  const { data: pendingRow, error: insertErr } = await supabase
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
    return NextResponse.json({ success: false, error: { message: `无法创建生成记录：${insertErr?.message ?? 'unknown'}`, code: 'DB_ERROR' } }, { status: 500 });
  }
  const recordId = pendingRow.id;

  try {
    // 5. 调网关：参考图按 人物 → 服装 → 姿势 顺序，同时冗余传单图字段
    const referenceImages = [characterImage, ...usableOutfits.map((o) => o.url), poseImage];
    const result = await generateImage({
      model: modelRow.model_id,
      prompt,
      size: body.size ?? modelRow.default_size ?? undefined,
      n: 1,
      negative_prompt: negativePrompt,
      reference_images: referenceImages,
      character_image: characterImage,
      outfit_image: usableOutfits[0].url,
      pose_image: poseImage,
      accessToken: gatewayToken,
    });
    const image = result.images[0];
    if (!image) throw new Error('网关返回空 images 数组');

    // 6. 下载 → 上传 Storage
    let resultBucket: string | null = null;
    let resultPath: string | null = null;

    const admin = createAdminSupabase();
    const { data: client } = await admin.auth.getUser(user.id);
    const clientId = (client.user?.app_metadata as { client_id?: string } | undefined)?.client_id ?? '';

    if (image.url) {
      const fetched = await fetch(image.url);
      if (!fetched.ok) throw new Error(`下载网关结果失败：${fetched.status}`);
      const buf = Buffer.from(await fetched.arrayBuffer());
      const ext = image.url.match(/\.(png|jpg|jpeg|webp)(\?|$)/i)?.[1] ?? 'png';
      const path = `${clientId}/${user.id}/${recordId}.${ext}`;
      const { error: upErr } = await admin.storage
        .from('media-user')
        .upload(path, buf, { contentType: `image/${ext === 'jpg' ? 'jpeg' : ext}`, upsert: true });
      if (upErr) throw new Error(`上传 Storage 失败：${upErr.message}`);
      resultBucket = 'media-user';
      resultPath = path;
    }

    // 7. 更新记录
    const { error: upStatErr } = await supabase
      .from('outfit_generations')
      .update({
        status: 'succeeded',
        result_bucket: resultBucket,
        result_path: resultPath,
        gateway_request_id: result.request_id,
        gateway_usage: result.usage ?? null,
      } satisfies Partial<OutfitGeneration>)
      .eq('id', recordId);

    if (upStatErr) throw new Error(`更新记录失败：${upStatErr.message}`);

    return NextResponse.json({ success: true, data: { id: recordId, result_path: resultPath, result_bucket: resultBucket } });
  } catch (e) {
    // 8. 失败回写
    const msg = e instanceof GatewayError ? `网关错误 (${e.status})：${e.message}` : e instanceof Error ? e.message : String(e);
    await supabase
      .from('outfit_generations')
      .update({ status: 'failed', error: msg } satisfies Partial<OutfitGeneration>)
      .eq('id', recordId);
    const status = e instanceof GatewayError ? Math.max(400, e.status) : 500;
    return NextResponse.json(
      { success: false, error: { message: msg, code: 'GENERATE_FAILED' } },
      { status },
    );
  }
}
