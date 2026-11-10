// /api/characters — 角色 CRUD：GET 列表 / POST 上传 / PATCH 更新 / DELETE 删除。
// 策略：service_role 做 Storage 上传 + characters 行 upsert/delete，所有写操作前先校验 owner。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { randomUUID } from 'crypto';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { config } from '@/lib/config';

export const runtime = 'nodejs';

const MAX_SIZE = 8 * 1024 * 1024; // 8MB
const ALLOWED_MIME = new Set(['image/jpeg', 'image/png', 'image/webp']);

// Supabase 类型在项目中未为 admin client 注册，使用轻量本地类型
interface CharacterRow {
  id: string;
  client_id: string;
  user_id: string;
  name: string;
  description: string | null;
  bucket: string;
  image_path: string;
  cover_path: string | null;
  is_public: boolean;
  created_at: string;
  updated_at: string;
}

function extractExt(filename: string, mime: string): string {
  const mimeExt = mime.split('/')[1]; // jpeg / png / webp
  if (mimeExt === 'jpeg') return 'jpg';
  if (mimeExt === 'png') return 'png';
  if (mimeExt === 'webp') return 'webp';
  const fnExt = filename.split('.').pop()?.toLowerCase();
  if (fnExt === 'jpg' || fnExt === 'jpeg') return 'jpg';
  if (fnExt === 'png') return 'png';
  if (fnExt === 'webp') return 'webp';
  return 'png';
}

function charactersTable(admin: ReturnType<typeof createAdminSupabase>) {
  // Supabase 类型中未声明 characters 表，强转后继续用链式调用
  // eslint-disable-next-line @typescript-eslint/no-explicit-any -- 项目尚未启用 Supabase 数据库生成类型
  return (admin as any).from('characters');
}

export async function GET() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });

  const { data, error } = await supabase
    .from('characters')
    .select('*')
    .eq('user_id', user.id)
    .order('created_at', { ascending: false });

  if (error) {
    return NextResponse.json({ error: 'DB_ERROR', message: error.message }, { status: 500 });
  }
  return NextResponse.json({ ok: true, data });
}

export async function POST(req: NextRequest) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });

  let form: FormData;
  try {
    form = await req.formData();
  } catch {
    return NextResponse.json({ error: 'INVALID_BODY', message: '请求必须是 multipart/form-data' }, { status: 400 });
  }

  const file = form.get('file');
  if (!(file instanceof File)) {
    return NextResponse.json({ error: 'MISSING_FILE', message: '缺少 file 字段' }, { status: 400 });
  }

  if (!ALLOWED_MIME.has(file.type)) {
    return NextResponse.json(
      { error: 'INVALID_MIME', message: `仅支持 ${Array.from(ALLOWED_MIME).join(', ')}` },
      { status: 400 },
    );
  }

  if (file.size > MAX_SIZE) {
    return NextResponse.json(
      { error: 'FILE_TOO_LARGE', message: `文件超过 ${MAX_SIZE / 1024 / 1024}MB 上限` },
      { status: 413 },
    );
  }

  const nameRaw = form.get('name');
  const name = typeof nameRaw === 'string' && nameRaw.trim() ? nameRaw.trim() : file.name.replace(/\.[^.]+$/, '');
  const descriptionRaw = form.get('description');
  const description = typeof descriptionRaw === 'string' && descriptionRaw.trim() ? descriptionRaw.trim() : null;

  const uuid = randomUUID();
  const ext = extractExt(file.name, file.type);
  const path = `characters/${uuid}.${ext}`;
  const bucket = 'media-user';
  const buf = Buffer.from(await file.arrayBuffer());

  const admin = createAdminSupabase();

  // 1. 上传 Storage
  const { error: upErr } = await admin.storage.from(bucket).upload(path, buf, {
    contentType: file.type,
    upsert: false,
  });
  if (upErr) {
    return NextResponse.json(
      { error: 'STORAGE_ERROR', message: `上传 Storage 失败：${upErr.message}` },
      { status: 500 },
    );
  }

  // 2. 写 characters 表（service_role 绕过 RLS，但我们会校验 owner）
  const { data: row, error: dbErr } = await charactersTable(admin)
    .insert({
      id: uuid,
      client_id: config.gatewayClientId,
      user_id: user.id,
      name,
      description,
      bucket,
      image_path: path,
      cover_path: null,
      is_public: false,
    } satisfies Partial<CharacterRow>)
    .select('*')
    .single() as { data: CharacterRow | null; error: unknown };

  if (dbErr) {
    // 尝试清理 Storage
    await admin.storage.from(bucket).remove([path]).catch(() => undefined);
    const msg = dbErr instanceof Error ? dbErr.message : String(dbErr);
    return NextResponse.json(
      { error: 'DB_ERROR', message: `写入数据库失败：${msg}` },
      { status: 500 },
    );
  }

  return NextResponse.json({ ok: true, data: row });
}

export async function PATCH(req: NextRequest) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });

  const { searchParams } = new URL(req.url);
  const id = searchParams.get('id');
  if (!id) return NextResponse.json({ error: 'MISSING_ID', message: '缺少 id 参数' }, { status: 400 });

  let body: { name?: unknown; description?: unknown };
  try { body = (await req.json()) as { name?: unknown; description?: unknown }; }
  catch { return NextResponse.json({ error: 'INVALID_BODY', message: '请求必须是合法 JSON' }, { status: 400 }); }

  // 构造 update payload（只允许 name / description）
  const patch: Record<string, unknown> = {};
  if (typeof body.name === 'string') patch.name = body.name.trim();
  if (typeof body.description === 'string') patch.description = body.description.trim() || null;
  if (Object.keys(patch).length === 0) {
    return NextResponse.json({ error: 'EMPTY_PATCH', message: '没有可更新的字段' }, { status: 400 });
  }

  // service_role + 先验证 owner
  const admin = createAdminSupabase();
  const { data: existing, error: qErr } = await charactersTable(admin)
    .select('user_id').eq('id', id).maybeSingle() as { data: { user_id: string } | null; error: unknown };
  if (qErr) {
    const msg = qErr instanceof Error ? qErr.message : String(qErr);
    return NextResponse.json({ error: 'DB_ERROR', message: msg }, { status: 500 });
  }
  if (!existing) return NextResponse.json({ error: 'NOT_FOUND', message: '人物不存在' }, { status: 404 });
  if (existing.user_id !== user.id) {
    return NextResponse.json({ error: 'FORBIDDEN', message: '无权修改该人物' }, { status: 403 });
  }

  const { data: row, error: upErr } = await charactersTable(admin)
    .update(patch).eq('id', id).select('*').single() as { data: CharacterRow | null; error: unknown };
  if (upErr) {
    const msg = upErr instanceof Error ? upErr.message : String(upErr);
    return NextResponse.json({ error: 'DB_ERROR', message: msg }, { status: 500 });
  }

  return NextResponse.json({ ok: true, data: row });
}

export async function DELETE(req: NextRequest) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401 });

  const { searchParams } = new URL(req.url);
  const id = searchParams.get('id');
  if (!id) return NextResponse.json({ error: 'MISSING_ID', message: '缺少 id 参数' }, { status: 400 });

  const admin = createAdminSupabase();

  // 先查 owner + 路径
  const { data: row, error: qErr } = await charactersTable(admin)
    .select('user_id, bucket, image_path').eq('id', id).maybeSingle() as {
      data: { user_id: string; bucket: string; image_path: string } | null;
      error: unknown;
    };
  if (qErr) {
    const msg = qErr instanceof Error ? qErr.message : String(qErr);
    return NextResponse.json({ error: 'DB_ERROR', message: msg }, { status: 500 });
  }
  if (!row) return NextResponse.json({ error: 'NOT_FOUND', message: '人物不存在' }, { status: 404 });
  if (row.user_id !== user.id) {
    return NextResponse.json({ error: 'FORBIDDEN', message: '无权删除该人物' }, { status: 403 });
  }

  // 1. 删数据库行
  const { error: delErr } = await charactersTable(admin).delete().eq('id', id) as { error: unknown };
  if (delErr) {
    const msg = delErr instanceof Error ? delErr.message : String(delErr);
    return NextResponse.json({ error: 'DB_ERROR', message: msg }, { status: 500 });
  }

  // 2. 删 Storage（允许失败，记录即可）
  if (row.image_path && row.bucket) {
    await admin.storage.from(row.bucket).remove([row.image_path]).catch((e) => {
      console.warn('[characters] Storage remove failed:', e);
    });
  }

  return NextResponse.json({ ok: true });
}
