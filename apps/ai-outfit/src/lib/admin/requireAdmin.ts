import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { config } from '@/lib/config';
import { gatewayFailure, requireGatewayOrigin } from '@/lib/gateway-bff';

export async function requireAdmin(req?: NextRequest): Promise<{
  ok: true;
  userId: string;
  clientId: string;
  role: 'owner' | 'admin';
} | { ok: false; response: NextResponse }> {
  const supabase = await createServerSupabase();
  const { data: { user }, error: authError } = await supabase.auth.getUser();
  if (authError || !user) {
    return { ok: false, response: NextResponse.json({ error: 'UNAUTHENTICATED' }, { status: 401, headers: { 'Cache-Control': 'no-store' } }) };
  }
  const { data: clientId, error: scopeError } = await supabase.rpc('current_app_client_id');
  if (scopeError || !config.gatewayClientId || clientId !== config.gatewayClientId) {
    return { ok: false, response: NextResponse.json({ error: 'FORBIDDEN' }, { status: 403, headers: { 'Cache-Control': 'no-store' } }) };
  }
  const { data: row, error: grantError } = await supabase
    .from('app_admins')
    .select('role')
    .eq('client_id', clientId)
    .eq('user_id', user.id)
    .maybeSingle();
  if (grantError || !row || (row.role !== 'owner' && row.role !== 'admin')) {
    return { ok: false, response: NextResponse.json({ error: 'FORBIDDEN' }, { status: 403, headers: { 'Cache-Control': 'no-store' } }) };
  }
  if (req && !['GET', 'HEAD', 'OPTIONS'].includes(req.method)) {
    try { requireGatewayOrigin(req); }
    catch (error) { return { ok: false, response: gatewayFailure(error) }; }
  }
  return { ok: true, userId: user.id, clientId, role: row.role };
}
