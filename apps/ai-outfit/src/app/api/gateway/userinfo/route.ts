// GET /api/gateway/userinfo — BFF：校验 Supabase session → 读网关 userinfo → 返回。
import 'server-only';
import { NextResponse } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { fetchGatewayUserinfo, GatewayError } from '@/lib/gateway';

export const runtime = 'nodejs';

export async function GET() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED', message: '未登录' }, { status: 401 });

  // 从 Supabase session 读取 provider_token（网关 OAuth access_token）
  const { data: { session } } = await supabase.auth.getSession();
  const accessToken = session?.provider_token;
  if (!accessToken) {
    return NextResponse.json(
      { error: 'GATEWAY_TOKEN_MISSING', message: '网关授权令牌缺失，请重新登录' },
      { status: 401 },
    );
  }

  try {
    const info = await fetchGatewayUserinfo({ accessToken });
    return NextResponse.json({ ok: true, data: info });
  } catch (e) {
    if (e instanceof GatewayError) {
      return NextResponse.json({ error: e.code ?? 'GATEWAY_ERROR', message: e.message }, { status: e.status });
    }
    return NextResponse.json(
      { error: 'INTERNAL', message: e instanceof Error ? e.message : String(e) },
      { status: 500 },
    );
  }
}
