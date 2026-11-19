import 'server-only';
import { type NextRequest, NextResponse } from 'next/server';
import { z } from 'zod';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import { config } from '@/lib/config';
import { gatewayFailure } from '@/lib/gateway-bff';
import { adminRequest } from '@/lib/gateway-proxy';
import { createAdminSupabase } from '@/lib/supabase/admin';

const timestamp = z.number().int().min(1).max(253402300799).nullable().optional();
const rule = z.object({
  name: z.string().trim().min(1).max(128), description: z.string().max(255).optional(), enabled: z.boolean(),
  quota_limit_enabled: z.boolean(), quota_period: z.enum(['daily', 'weekly', 'monthly']), quota_limit: z.number().int().nonnegative().safe(),
  time_window_enabled: z.boolean(), time_window_timezone: z.enum(['UTC', 'Asia/Shanghai']), allowed_weekdays: z.string().max(64),
  valid_from: timestamp, valid_until: timestamp,
}).strict();
type Context = { params: Promise<{ user_id: string; path: string[] }> };

async function manage(req: NextRequest, context: Context) {
  const check = await requireAdmin(req);
  if (!check.ok) return check.response;
  const { user_id, path } = await context.params;
  if (!z.uuid().safeParse(user_id).success || !Number.isSafeInteger(config.gatewayAppId) || config.gatewayAppId <= 0) {
    return NextResponse.json({ error: 'INVALID_TARGET' }, { status: 400 });
  }
  const endpoint = path.join('/');
  const validRoute = endpoint === 'quota-rules' && ['GET', 'POST'].includes(req.method)
    || /^quota-rules\/[1-9]\d*$/.test(endpoint) && ['PUT', 'DELETE'].includes(req.method)
    || /^keys\/[1-9]\d*\/quota-rule$/.test(endpoint) && req.method === 'PUT'
    || /^keys\/[1-9]\d*\/cancel-rotation$/.test(endpoint) && req.method === 'POST';
  if (!validRoute) return NextResponse.json({ error: 'UNSUPPORTED' }, { status: 400 });
  // Resolve the native subject in a protected mapping; never accept a gateway ID/App from the browser.
  const mapping = await createAdminSupabase().from('gateway_proxy_accounts').select('gateway_user_id')
    .eq('user_id', user_id).eq('app_id', config.gatewayAppId).eq('client_id', check.clientId)
    .eq('issuer', `${config.supabaseUrl.replace(/\/$/, '')}/auth/v1`).eq('identity_source', 'proxy').maybeSingle();
  if (mapping.error || !mapping.data || !z.number().int().positive().safe().safeParse(mapping.data.gateway_user_id).success) {
    return NextResponse.json({ error: 'NOT_FOUND' }, { status: 404 });
  }
  let input: unknown;
  if (req.method !== 'GET' && req.method !== 'DELETE') {
    try {
      const schema = endpoint.endsWith('/quota-rule') ? z.object({ rule_id: z.number().int().nonnegative().safe() }).strict()
        : endpoint.endsWith('/cancel-rotation') ? z.object({}).strict() : rule;
      input = schema.parse(await req.json());
    } catch { return NextResponse.json({ error: 'INVALID_BODY' }, { status: 400 }); }
  }
  const query = new URLSearchParams({ app_id: String(config.gatewayAppId) });
  if (req.method === 'GET') {
    const pagination = z.object({ p: z.coerce.number().int().min(1).max(100000), page_size: z.coerce.number().int().min(1).max(100) })
      .safeParse({ p: req.nextUrl.searchParams.get('p') ?? 1, page_size: req.nextUrl.searchParams.get('page_size') ?? 20 });
    if (!pagination.success) return NextResponse.json({ error: 'INVALID_PAGINATION' }, { status: 400 });
    query.set('p', String(pagination.data.p)); query.set('page_size', String(pagination.data.page_size));
  }
  try {
    const response = await adminRequest(`/api/app-proxy/users/${mapping.data.gateway_user_id}/${endpoint}?${query}`, req.method, input);
    return NextResponse.json(await response.json(), { status: response.status, headers: { 'Cache-Control': 'no-store' } });
  } catch (error) { return gatewayFailure(error); }
}

export const GET = manage;
export const POST = manage;
export const PUT = manage;
export const DELETE = manage;
