// POST /api/generate/[id]/advance — 单步推进一次换装任务。
// 浏览器带 session 逐次调用（网关访问令牌依赖请求 cookie），每步 < Hobby 60s 超时；
// 阶段产物落库，中断后重进即可从持久化状态续跑。
import 'server-only';
import { type NextRequest } from 'next/server';
import { createServerSupabase } from '@/lib/supabase/server';
import { GatewayError } from '@/lib/gateway-errors';
import { getGatewayAccessToken } from '@/lib/gateway-session';
import { gatewaySuccess, gatewayFailure, requireGatewayOrigin } from '@/lib/gateway-bff';
import { advanceTryon } from '@/lib/tryon/advance';

export const maxDuration = 60;

export async function POST(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  try { requireGatewayOrigin(req); } catch (error) { return gatewayFailure(error); }
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) return gatewayFailure(new GatewayError(401, '请重新登录后生成', 'AUTH_SESSION_EXPIRED'));
  try { await getGatewayAccessToken(); } catch (error) { return gatewayFailure(error); }

  const { id } = await ctx.params;
  try {
    return gatewaySuccess(await advanceTryon(id, user.id));
  } catch (error) {
    return gatewayFailure(error);
  }
}
