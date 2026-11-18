import type { NextRequest } from 'next/server';
import { z } from 'zod';
import { config } from '@/lib/config';
import { createServerSupabase } from '@/lib/supabase/server';
import { getProxyAccount } from '@/lib/gateway-proxy';
import { GatewayError } from '@/lib/gateway-errors';
import { gatewayFailure, gatewaySuccess, requireGatewayOrigin } from '@/lib/gateway-bff';

export async function POST(request: NextRequest) {
  try {
    requireGatewayOrigin(request);
    if (!['proxy', 'dual'].includes(config.integrationMode)) throw new GatewayError(403, 'Login method is unavailable.', 'LOGIN_DISABLED');
    const input = z.object({ email: z.string().email().max(320), password: z.string().min(1).max(1024) }).strict()
      .safeParse(await request.json().catch(() => null));
    if (!input.success) throw new GatewayError(400, 'Invalid login details.', 'INVALID_LOGIN');
    const supabase = await createServerSupabase();
    const result = await supabase.auth.signInWithPassword(input.data);
    if (result.error || !result.data.session) throw new GatewayError(401, 'Invalid login details.', 'INVALID_LOGIN');
    try { await getProxyAccount(); }
    catch (error) { await supabase.auth.signOut({ scope: 'local' }); throw error; }
    return gatewaySuccess({ signed_in: true });
  } catch (error) { return gatewayFailure(error); }
}
