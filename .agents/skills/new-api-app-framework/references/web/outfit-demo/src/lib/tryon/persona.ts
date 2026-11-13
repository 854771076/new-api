// lib/tryon/persona.ts — 人物标准化（v2）：身份保持的正面全身工作室参考图。
// 原图始终保留在 characters.image_path；标准化结果写 cover_path，并推进 persona 状态机。
import 'server-only';
import type { SupabaseClient } from '@supabase/supabase-js';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { getSignedUrl } from '@/lib/media-urls';
import { GatewayError } from '@/lib/gateway-errors';
import { generateReferenceImage } from '@/lib/gateway';
import { readGeneratedImage } from '@/lib/generated-image';
import { PERSONA_V2_PROMPT } from './prompts';
import { resolveTryonModel } from './models';
import type { Character } from '@/lib/types';

export interface PersonaInput {
  character: Pick<Character, 'id' | 'bucket' | 'image_path' | 'cover_path' | 'persona_status' | 'persona_version'>;
  clientId: string;
  userId: string;
  /** 显式指定生图模型；缺省时按 ai_models(usage='image') 解析。 */
  imageModel?: string;
  size?: string;
}

export interface PersonaResult {
  bucket: string;
  path: string;
  personaVersion: number;
}

/**
 * 生成并落盘标准化人物图。
 * 成功才覆盖 cover_path 并把 persona_status 置 ready；任何失败保留原图与既有可用 cover，
 * 仅写入 failed/错误信息，符合「失败继续保留原有可用素材」。
 */
export async function normalizePersona(
  supabase: SupabaseClient,
  input: PersonaInput,
): Promise<PersonaResult> {
  const { character, clientId, userId } = input;
  const admin = createAdminSupabase();

  const model = input.imageModel ?? (await resolveTryonModel(supabase, 'image')).model;
  const size = input.size ?? '1024x1536';

  const sourceUrl = await getSignedUrl(character.bucket, character.image_path);
  if (!sourceUrl) {
    throw new GatewayError(400, '人物原图无法读取，请重新上传', 'AI_TRYON_PERSONA_SOURCE_MISSING');
  }

  await markPersonaStatus(admin, character.id, clientId, userId, 'normalizing', null);

  let bytes: Buffer;
  try {
    const result = await generateReferenceImage({
      model,
      prompt: PERSONA_V2_PROMPT,
      reference_images: [sourceUrl],
      size,
    });
    const image = result.images[0];
    if (!image) throw new Error('empty persona image');
    bytes = await readGeneratedImage(image);
  } catch (error) {
    await markPersonaStatus(
      admin, character.id, clientId, userId, 'failed',
      error instanceof GatewayError ? error.message : '人物标准化失败，请稍后重试',
    );
    throw error instanceof GatewayError
      ? error
      : new GatewayError(502, '人物标准化失败，请稍后重试', 'AI_TRYON_PERSONA_FAILED');
  }

  // 新版本写到独立路径，避免覆盖原图或上一版仍被历史任务引用的标准化图。
  const nextVersion = (character.persona_version ?? 0) + 1;
  const bucket = 'media-user';
  const path = `${clientId}/${userId}/characters/${character.id}/persona-v${nextVersion}.png`;
  const { error: uploadError } = await admin.storage.from(bucket).upload(path, bytes, {
    contentType: 'image/png',
    upsert: false,
  });
  if (uploadError) {
    await markPersonaStatus(admin, character.id, clientId, userId, 'failed', '人物标准化图保存失败');
    throw new GatewayError(503, '人物标准化图保存失败，请稍后重试', 'UPSTREAM_STORAGE_ERROR');
  }

  const previousCover = character.cover_path;
  const { error: updateError } = await admin.from('characters').update({
    cover_path: path,
    persona_status: 'ready',
    persona_version: nextVersion,
    persona_error: null,
  }).eq('id', character.id).eq('client_id', clientId).eq('user_id', userId).select('id').maybeSingle();

  if (updateError) {
    await admin.storage.from(bucket).remove([path]);
    await markPersonaStatus(admin, character.id, clientId, userId, 'failed', '人物标准化状态更新失败');
    throw new GatewayError(503, '人物标准化状态更新失败，请稍后重试', 'UPSTREAM_DATABASE_ERROR');
  }

  // 旧标准化图仅在它是本管线产物（persona 子路径）且不再被引用时清理；最初没有 cover 时为空。
  if (previousCover && previousCover !== path && previousCover.includes('/persona-v')) {
    await admin.storage.from(bucket).remove([previousCover]).catch(() => undefined);
  }

  return { bucket, path, personaVersion: nextVersion };
}

/**
 * 返回本次换装应使用的人物参考图定位；尚未标准化或状态失败但无可用 cover 时现场生成。
 * ready 且存在 cover_path 时直接复用，不重复扣费/重复生成。
 */
export async function ensurePersona(
  supabase: SupabaseClient,
  input: PersonaInput,
): Promise<PersonaResult> {
  const { character } = input;
  if (character.persona_status === 'ready' && character.cover_path) {
    return {
      bucket: character.bucket,
      path: character.cover_path,
      personaVersion: character.persona_version ?? 0,
    };
  }
  return normalizePersona(supabase, input);
}

async function markPersonaStatus(
  admin: SupabaseClient,
  characterId: string,
  clientId: string,
  userId: string,
  status: Character['persona_status'],
  error: string | null,
) {
  await admin.from('characters').update({
    persona_status: status,
    ...(error === null ? { persona_error: null } : { persona_error: error }),
  }).eq('id', characterId).eq('client_id', clientId).eq('user_id', userId);
}
