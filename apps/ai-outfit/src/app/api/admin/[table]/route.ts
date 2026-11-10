// GET/POST/PATCH/DELETE /api/admin/[table] — 管理后台 CRUD 代理。
// 只允许已授权 admin；所有操作走 service_role Supabase client。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { z } from 'zod';

const ALLOWED_TABLES = new Set([
  'outfits',
  'poses',
  'characters',
  'outfit_generations',
  'price_plans',
  'ai_models',
  'prompt_templates',
]);
// 这些表用 client_id 区分租户行与全局默认行（client_id = ''）。
const CLIENT_SCOPED_TABLES = new Set(['ai_models', 'prompt_templates']);
// 这些表的 client_id 是 NOT NULL，新建时必须显式写入当前 app 的 client_id。
const CLIENT_ID_REQUIRED_TABLES = new Set([
  'outfits',
  'poses',
  'price_plans',
  'ai_models',
  'prompt_templates',
]);
// 每张表参与 keyword 模糊搜索的列；未列出的表沿用默认的三列。
const KEYWORD_COLUMNS: Record<string, string[]> = {
  ai_models: ['model_id', 'display_name'],
  prompt_templates: ['name'],
  default: ['name_zh', 'name_en', 'provider_id'],
};
const bodySchema = z.record(z.string(), z.unknown());

type AdminQuery = ReturnType<ReturnType<typeof createAdminSupabase>['from']>;
type FilteredQuery = ReturnType<AdminQuery['select']>;

function applyFilters(query: FilteredQuery, searchParams: URLSearchParams, table: string): FilteredQuery {
  const eq = searchParams.get('eq');
  if (eq) {
    const [col, val] = eq.split(':');
    if (col && val) query = query.eq(col, val);
  }
  const keyword = searchParams.get('keyword');
  if (keyword) {
    const kw = `%${keyword}%`;
    const columns = KEYWORD_COLUMNS[table] ?? KEYWORD_COLUMNS.default;
    query = query.or(columns.map((column) => `${column}.ilike.${kw}`).join(','));
  }
  const sort = searchParams.get('sort') ?? 'created_at';
  const order = searchParams.get('order') ?? 'desc';
  query = query.order(sort, { ascending: order !== 'desc' });
  const limit = Math.min(100, Math.max(1, parseInt(searchParams.get('limit') ?? '20', 10) || 20));
  const page = Math.max(1, parseInt(searchParams.get('page') ?? '1', 10) || 1);
  const from = (page - 1) * limit;
  query = query.range(from, from + limit - 1);
  return query;
}

export async function GET(req: NextRequest, ctx: { params: Promise<{ table: string }> }) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;

  const { table } = await ctx.params;
  if (!ALLOWED_TABLES.has(table)) return NextResponse.json({ error: 'INVALID_TABLE' }, { status: 400 });

  const admin = createAdminSupabase();
  const sp = new URL(req.url).searchParams;
  const query = applyFilters(admin.from(table).select('*', { count: 'exact' }), sp, table);
  const { data, count, error } = (await query) as unknown as {
    data: unknown[];
    count: number | null;
    error: { message: string } | null;
  };
  if (error) return NextResponse.json({ error: error.message }, { status: 500 });
  return NextResponse.json({ data, total: count ?? 0 });
}

export async function POST(req: NextRequest, ctx: { params: Promise<{ table: string }> }) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;

  const { table } = await ctx.params;
  if (!ALLOWED_TABLES.has(table)) return NextResponse.json({ error: 'INVALID_TABLE' }, { status: 400 });

  let body: Record<string, unknown>;
  try { body = bodySchema.parse(await req.json()); }
  catch (e) { return NextResponse.json({ error: 'INVALID_BODY', detail: String(e) }, { status: 400 }); }

  // client_id 必填的表：新建时未指定则归入当前 app 的租户行。
  if (CLIENT_ID_REQUIRED_TABLES.has(table) && (body.client_id === undefined || body.client_id === null)) {
    body.client_id = process.env.NEXT_PUBLIC_GATEWAY_CLIENT_ID ?? '';
  }

  const admin = createAdminSupabase();

  // 租户表新建默认行时，先清掉同作用域的旧默认，避免命中唯一索引。
  if (CLIENT_SCOPED_TABLES.has(table) && body.is_default === true) {
    let reset = admin
      .from(table)
      .update({ is_default: false } as never)
      .eq('client_id', body.client_id as string)
      .eq('is_default', true);
    if (table === 'prompt_templates' && typeof body.usage === 'string') {
      reset = reset.eq('usage', body.usage);
    }
    const resetResult = (await reset) as unknown as { error: { message: string } | null };
    if (resetResult.error) return NextResponse.json({ error: resetResult.error.message }, { status: 500 });
  }

  const result = await admin.from(table).insert(body as never).select().single();
  const { data, error } = result as unknown as { data: unknown; error: { message: string } | null };
  if (error) return NextResponse.json({ error: error.message }, { status: 500 });
  return NextResponse.json({ data });
}

export async function PATCH(req: NextRequest, ctx: { params: Promise<{ table: string }> }) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;

  const { table } = await ctx.params;
  const sp = new URL(req.url).searchParams;
  const id = sp.get('id');
  if (!ALLOWED_TABLES.has(table) || !id) return NextResponse.json({ error: 'INVALID' }, { status: 400 });

  let body: Record<string, unknown>;
  try { body = bodySchema.parse(await req.json()); }
  catch (e) { return NextResponse.json({ error: 'INVALID_BODY', detail: String(e) }, { status: 400 }); }

  const admin = createAdminSupabase();

  // 设为默认：先按作用域清掉其他默认行，再执行本次更新，保证唯一默认。
  if (CLIENT_SCOPED_TABLES.has(table) && body.is_default === true) {
    const scopeResult = (await admin.from(table).select('client_id, usage').eq('id', id).maybeSingle()) as unknown as {
      data: { client_id: string; usage: string } | null;
      error: { message: string } | null;
    };
    if (scopeResult.error) return NextResponse.json({ error: scopeResult.error.message }, { status: 500 });
    if (!scopeResult.data) return NextResponse.json({ error: 'NOT_FOUND' }, { status: 404 });

    let reset = admin
      .from(table)
      .update({ is_default: false } as never)
      .eq('client_id', scopeResult.data.client_id)
      .eq('is_default', true)
      .neq('id', id);
    if (table === 'prompt_templates') reset = reset.eq('usage', scopeResult.data.usage);
    const resetResult = (await reset) as unknown as { error: { message: string } | null };
    if (resetResult.error) return NextResponse.json({ error: resetResult.error.message }, { status: 500 });
  }

  const result = await admin.from(table).update(body as never).eq('id', id).select().single();
  const { data, error } = result as unknown as { data: unknown; error: { message: string } | null };
  if (error) return NextResponse.json({ error: error.message }, { status: 500 });
  return NextResponse.json({ data });
}

export async function DELETE(req: NextRequest, ctx: { params: Promise<{ table: string }> }) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;

  const { table } = await ctx.params;
  const sp = new URL(req.url).searchParams;
  const id = sp.get('id');
  if (!ALLOWED_TABLES.has(table) || !id) return NextResponse.json({ error: 'INVALID' }, { status: 400 });

  const admin = createAdminSupabase();
  const { error } = (await admin.from(table).delete().eq('id', id)) as unknown as { error: { message: string } | null };
  if (error) return NextResponse.json({ error: error.message }, { status: 500 });
  return NextResponse.json({ success: true });
}
