import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { z } from 'zod';

const text = z.string().trim().min(1).max(200);
const optionalText = z.string().max(10000).nullable().optional();
const flags = { is_active: z.boolean().optional(), sort_order: z.number().int().min(0).max(2147483647).optional() };
const modelUsage = z.enum(['image', 'vision']);
const promptUsage = z.enum(['outfit', 'portrait', 'general']);
const modelSchema = z.object({
  model_id: text, display_name: text, provider: z.string().max(200).nullable().optional(),
  usage: modelUsage.optional(), description_zh: optionalText,
  default_size: z.string().max(100).nullable().optional(), default_quality: z.string().max(100).nullable().optional(),
  is_default: z.boolean().optional(), ...flags,
}).strict();
const promptSchema = z.object({
  name: text, template: z.string().trim().min(1).max(50000), negative_prompt: optionalText,
  usage: promptUsage.optional(), is_default: z.boolean().optional(), ...flags,
}).strict();
const outfitSchema = z.object({
  name_zh: text, name_en: z.string().max(200).nullable().optional(), category: text,
  description_zh: optionalText, description_en: optionalText,
  tags: z.array(text).max(100).optional(), keywords: z.array(text).max(100).optional(), ...flags,
}).strict();
const poseSchema = z.object({
  name_zh: text, name_en: z.string().max(200).nullable().optional(), category: text,
  system: text, angle: text, pose_set: text, description_zh: optionalText, description_en: optionalText, ...flags,
}).strict();
const schemas = { outfits: outfitSchema, poses: poseSchema, ai_models: modelSchema, prompt_templates: promptSchema };
const resources: Record<string, { select: string; keywords: string[]; filters: string[]; sorts: string[] }> = {
  outfits: { select: 'id,client_id,provider_id,name_zh,name_en,category,description_zh,description_en,tags,keywords,bucket,thumb_path,preview_path,sort_order,is_active,created_at', keywords: ['name_zh', 'name_en', 'category', 'provider_id'], filters: ['category', 'is_active'], sorts: ['created_at', 'sort_order', 'name_zh'] },
  poses: { select: 'id,client_id,provider_id,name_zh,name_en,pose_set,system,category,angle,description_zh,description_en,bucket,thumb_path,preview_path,sort_order,is_active,created_at', keywords: ['name_zh', 'name_en', 'provider_id'], filters: ['category', 'system', 'angle', 'is_active'], sorts: ['created_at', 'sort_order', 'name_zh'] },
  characters: { select: 'id,client_id,user_id,name,description,is_public,created_at', keywords: ['name'], filters: ['is_public'], sorts: ['created_at', 'name'] },
  outfit_generations: { select: 'id,client_id,user_id,character_id,outfit_ids,pose_id,prompt,model,status,result_bucket,result_path,error,created_at', keywords: ['model'], filters: ['status', 'model'], sorts: ['created_at'] },
  ai_models: { select: '*', keywords: ['model_id', 'display_name'], filters: ['usage', 'is_active'], sorts: ['created_at', 'sort_order', 'model_id'] },
  prompt_templates: { select: '*', keywords: ['name'], filters: ['usage', 'is_active'], sorts: ['created_at', 'updated_at', 'sort_order', 'name'] },
};
const configurationTables = new Set(['ai_models', 'prompt_templates']);
type Context = { params: Promise<{ table: string }> };
const json = (body: unknown, status = 200) => NextResponse.json(body, { status, headers: { 'Cache-Control': 'no-store' } });

export async function GET(req: NextRequest, ctx: Context) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;
  const { table } = await ctx.params;
  const resource = Object.hasOwn(resources, table) ? resources[table] : undefined;
  if (!resource) return json({ error: 'INVALID_TABLE' }, 400);
  const sp = req.nextUrl.searchParams;
  if (sp.has('client_id') && sp.get('client_id') !== check.clientId) return json({ error: 'INVALID_SCOPE' }, 400);
  const sort = sp.get('sort') ?? 'created_at';
  const order = sp.get('order') ?? 'desc';
  if (!resource.sorts.includes(sort) || !['asc', 'desc'].includes(order)) return json({ error: 'INVALID_SORT' }, 400);
  const pagination = z.object({ page: z.coerce.number().int().min(1).max(100000).default(1), limit: z.coerce.number().int().min(1).max(100).default(20) }).safeParse({ page: sp.get('page') ?? undefined, limit: sp.get('limit') ?? undefined });
  if (!pagination.success) return json({ error: 'INVALID_PAGINATION' }, 400);
  const admin = createAdminSupabase();
  let query = admin.from(table).select(resource.select, { count: 'exact' });
  query = configurationTables.has(table) ? query.in('client_id', [check.clientId, '']) : query.eq('client_id', check.clientId);
  const eq = sp.get('eq');
  if (eq) {
    const colon = eq.indexOf(':');
    const field = eq.slice(0, colon);
    const value = eq.slice(colon + 1);
    if (colon < 1 || !resource.filters.includes(field) || !/^[\p{L}\p{N}_ .-]{1,200}$/u.test(value)) return json({ error: 'INVALID_FILTER' }, 400);
    query = query.eq(field, value);
  }
  const keyword = sp.get('keyword')?.trim();
  if (keyword) {
    // PostgREST's filter grammar is not a SQL parameter. Reject its delimiters.
    if (!/^[\p{L}\p{N}_ -]{1,100}$/u.test(keyword)) return json({ error: 'INVALID_KEYWORD' }, 400);
    query = query.or(resource.keywords.map((column) => `${column}.ilike.%${keyword}%`).join(','));
  }
  const { page, limit } = pagination.data;
  const from = (page - 1) * limit;
  const { data, count, error } = await query.order(sort, { ascending: order === 'asc' }).range(from, from + limit - 1);
  if (error) return json({ error: 'LOAD_FAILED' }, 500);
  return json({ data, total: count ?? 0 });
}

async function mutate(req: NextRequest, ctx: Context) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;
  const { table } = await ctx.params;
  if (!Object.hasOwn(resources, table)) return json({ error: 'INVALID_TABLE' }, 400);
  if (table === 'characters' || table === 'outfit_generations') return json({ error: 'READ_ONLY' }, 405);
  if (req.nextUrl.searchParams.has('client_id') && req.nextUrl.searchParams.get('client_id') !== check.clientId) return json({ error: 'INVALID_SCOPE' }, 400);
  const id = req.nextUrl.searchParams.get('id');
  if (req.method !== 'POST' && !z.uuid().safeParse(id).success) return json({ error: 'INVALID_ID' }, 400);
  let body: Record<string, unknown> = {};
  if (req.method !== 'DELETE') {
    const schema = schemas[table as keyof typeof schemas];
    let parsed: unknown;
    try { parsed = await req.json(); } catch { return json({ error: 'INVALID_BODY' }, 400); }
    let validation;
    if (req.method === 'PATCH') {
      validation = schema.partial().safeParse(parsed);
    } else if (configurationTables.has(table)) {
      validation = schema.safeParse(parsed);
    } else {
      const path = z.string().min(1).max(1024).refine((value) => value.startsWith(`${check.clientId}/`) && !value.includes('\\') && !value.split('/').some((part) => !part || part === '.' || part === '..'));
      validation = schema.extend({ provider_id: text, bucket: z.literal('media-official'), thumb_path: path, preview_path: path }).safeParse(parsed);
    }
    if (!validation.success || !Object.keys(validation.data).length) return json({ error: 'INVALID_BODY' }, 400);
    body = validation.data;
  }
  const admin = createAdminSupabase();
  if (configurationTables.has(table)) {
    const { data, error } = await admin.rpc('admin_mutate_configuration', {
      p_table: table, p_client_id: check.clientId, p_actor_id: check.userId,
      p_operation: req.method, p_id: req.method === 'POST' ? null : id, p_fields: body,
    });
    if (error) {
      if (error.code === 'P0002') return json({ error: 'NOT_FOUND' }, 404);
      if (error.code === '23505') return json({ error: 'CONFLICT' }, 409);
      if (error.code === '22023') return json({ error: 'INVALID_BODY' }, 400);
      if (error.code === '42501') return json({ error: 'FORBIDDEN' }, 403);
      return json({ error: 'SAVE_FAILED' }, 500);
    }
    return json({ data, success: true });
  }
  if (req.method === 'POST') {
    const { data, error } = await admin.from(table).insert({ ...body, client_id: check.clientId }).select('id').single();
    if (error) return json({ error: error.code === '23505' ? 'CONFLICT' : 'SAVE_FAILED' }, error.code === '23505' ? 409 : 500);
    return json({ data });
  }
  const scoped = admin.from(table);
  const query = req.method === 'DELETE' ? scoped.delete() : scoped.update(body);
  const { data, error } = await query.eq('client_id', check.clientId).eq('id', id).select('id').maybeSingle();
  if (error) return json({ error: 'SAVE_FAILED' }, 500);
  if (!data) return json({ error: 'NOT_FOUND' }, 404);
  return json({ data, success: true });
}

export const POST = mutate;
export const PATCH = mutate;
export const DELETE = mutate;
