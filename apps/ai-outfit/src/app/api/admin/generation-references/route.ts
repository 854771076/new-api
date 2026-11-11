// GET /api/admin/generation-references?id=xxx — 回溯一条生成记录用到的参考图签名链接。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { getSignedUrl } from '@/lib/media-urls';
import { z } from 'zod';

const json = (body: unknown, status = 200) => NextResponse.json(body, { status, headers: { 'Cache-Control': 'no-store' } });

interface GenerationRefRow {
  character_id: string | null;
  outfit_ids: string[] | null;
  pose_id: string | null;
}

interface CharacterRefRow {
  name: string;
  bucket: string;
  image_path: string;
}

interface OutfitRefRow {
  id: string;
  name_zh: string;
  bucket: string;
  thumb_path: string;
  preview_path: string | null;
}

interface PoseRefRow {
  name_zh: string;
  bucket: string;
  thumb_path: string;
  preview_path: string | null;
}

export async function GET(req: NextRequest) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;

  const id = new URL(req.url).searchParams.get('id');
  if (!z.uuid().safeParse(id).success) return json({ error: 'INVALID_ID' }, 400);

  const admin = createAdminSupabase();
  const { data, error } = (await admin
    .from('outfit_generations')
    .select('character_id, outfit_ids, pose_id')
    .eq('client_id', check.clientId)
    .eq('id', id)
    .maybeSingle()) as unknown as {
    data: GenerationRefRow | null;
    error: { message: string } | null;
  };
  if (error) return json({ error: 'LOAD_FAILED' }, 500);
  if (!data) return json({ error: 'NOT_FOUND' }, 404);
  const row = data;

  const outfitIds = row.outfit_ids ?? [];
  const characterRes = row.character_id
    ? ((await admin.from('characters').select('name, bucket, image_path').eq('client_id', check.clientId).eq('id', row.character_id).maybeSingle()) as unknown as {
        data: CharacterRefRow | null;
        error: unknown;
      })
    : { data: null, error: null };
  const outfitsRes = outfitIds.length
    ? ((await admin.from('outfits').select('id, name_zh, bucket, thumb_path, preview_path').eq('client_id', check.clientId).in('id', outfitIds)) as unknown as {
        data: OutfitRefRow[];
        error: unknown;
      })
    : { data: [], error: null };
  const poseRes = row.pose_id
    ? ((await admin.from('poses').select('name_zh, bucket, thumb_path, preview_path').eq('client_id', check.clientId).eq('id', row.pose_id).maybeSingle()) as unknown as {
        data: PoseRefRow | null;
        error: unknown;
      })
    : { data: null, error: null };

  if (characterRes.error || outfitsRes.error || poseRes.error) return json({ error: 'LOAD_FAILED' }, 500);
  const character = characterRes.data;
  const pose = poseRes.data;
  const outfits = outfitsRes.data ?? [];

  const [characterUrl, poseUrl, outfitUrls] = await Promise.all([
    character ? getSignedUrl(character.bucket, character.image_path) : Promise.resolve(null),
    pose ? getSignedUrl(pose.bucket, pose.preview_path?.trim() || pose.thumb_path) : Promise.resolve(null),
    Promise.all(
      outfits.map(async (o) => ({
        id: o.id,
        name: o.name_zh,
        url: await getSignedUrl(o.bucket, o.preview_path?.trim() || o.thumb_path),
      })),
    ),
  ]);

  return json({
    data: {
      character: character ? { name: character.name, url: characterUrl } : null,
      outfits: outfitUrls,
      pose: pose ? { name: pose.name_zh, url: poseUrl } : null,
    },
  });
}
