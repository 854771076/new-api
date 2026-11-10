// POST /api/gateway/topup — BFF：校验 Supabase session → 创建网关充值订单 → 返回 checkout_url。
import 'server-only';
import { NextResponse, type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { fetchGatewayTopup, GatewayError } from '@/lib/gateway';

export const runtime = 'nodejs';

const MIN_AMOUNT = 1;
const MAX_AMOUNT = 10_000;

export async function POST(req: NextRequest) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return NextResponse.json({ error: 'UNAUTHENTICATED', message: '未登录' }, { status: 401 });

  const { data: { session } } = await supabase.auth.getSession();
  const accessToken = session?.provider_token;
  if (!accessToken) {
    return NextResponse.json(
      { error: 'GATEWAY_TOKEN_MISSING', message: '网关授权令牌缺失，请重新登录' },
      { status: 401 },
    );
  }

  let body: { amount?: unknown; channel?: unknown };
  try {
    body = (await req.json()) as { amount?: unknown; channel?: unknown };
  } catch {
    return NextResponse.json({ error: 'INVALID_BODY', message: '请求必须是合法 JSON' }, { status: 400 });
  }

  const amount = typeof body.amount === 'number' && Number.isFinite(body.amount) ? body.amount : null;
  if (amount === null || amount < MIN_AMOUNT || amount > MAX_AMOUNT) {
    return NextResponse.json(
      { error: 'INVALID_AMOUNT', message: `amount 必须在 ${MIN_AMOUNT}–${MAX_AMOUNT} 之间` },
      { status: 400 },
    );
  }

  const channel = typeof body.channel === 'string' && body.channel ? body.channel : undefined;

  try {
    const result = await fetchGatewayTopup({ amount, channel }, { accessToken });
    return NextResponse.json({ ok: true, data: result });
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
