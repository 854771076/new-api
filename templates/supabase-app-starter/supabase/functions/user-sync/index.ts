// supabase/functions/user-sync/index.ts
//
// Supabase Edge Function: 在 OIDC 登录后把网关 User.Id 写入
// public.user_profiles 并补全显示名。
//
// 触发方式：App 端登录回调（AuthCallback.tsx）携带当前 Supabase access_token
// 调用本函数。本函数只信任该 token，不接受请求 body 里的任何用户身份字段。
//
// 鉴权：
//   Authorization: Bearer <Supabase access_token>
//   → supabase.auth.getUser(jwt) 解析真实用户；缺失或校验失败返回 401。
//
// gateway_user_id 来源（按顺序，全部由服务端控制或已验证）：
//   1. auth.users.app_metadata.gateway_user_id（只有 service_role / Admin API 可写）
//   2. 用 app_metadata.gateway_access_token 调网关 /oauth/userinfo 得到的 sub
//   3. 已验证的 OIDC identity_data.sub（GATEWAY_OIDC_PROVIDER 指定的 provider）
//
// 环境变量：
//   SUPABASE_URL / SUPABASE_SERVICE_ROLE_KEY  —— Edge Function 运行时自动提供
//   GATEWAY_BASE_URL                          —— 可选；网关地址，用于补全显示名
//   GATEWAY_CLIENT_ID                         —— app_config 尚未初始化时写入的 client_id
//   GATEWAY_OIDC_PROVIDER                     —— 可选；Supabase Custom OIDC provider
//                                                slug，默认 newapi
//   APP_ORIGIN                                —— 可选；CORS 允许的来源，默认 *
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2';

interface AuthIdentity {
  provider?: string;
  identity_data?: Record<string, unknown>;
}

interface AuthUser {
  id: string;
  app_metadata?: Record<string, unknown>;
  identities?: AuthIdentity[];
}

interface GatewayProfile {
  sub: number;
  displayName?: string;
}

const corsHeaders = {
  'Access-Control-Allow-Origin': Deno.env.get('APP_ORIGIN') || '*',
  'Access-Control-Allow-Headers': 'authorization, apikey, content-type, x-client-info',
  'Access-Control-Allow-Methods': 'POST, OPTIONS',
  'Access-Control-Max-Age': '86400',
};

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { ...corsHeaders, 'content-type': 'application/json' },
  });
}

function positiveInt(value: unknown): number | null {
  const parsed = typeof value === 'number' ? value : Number.parseInt(String(value ?? ''), 10);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
}

Deno.serve(async (req) => {
  try {
    if (req.method === 'OPTIONS') {
      return new Response(null, { status: 204, headers: corsHeaders });
    }
    if (req.method !== 'POST') {
      return json({ ok: false, error: 'method not allowed' }, 405);
    }

    const authorization = req.headers.get('Authorization') || '';
    const jwt = authorization.startsWith('Bearer ')
      ? authorization.slice('Bearer '.length).trim()
      : '';
    if (!jwt) {
      return json({ ok: false, error: 'missing bearer token' }, 401);
    }

    const supabaseUrl = Deno.env.get('SUPABASE_URL');
    const serviceKey = Deno.env.get('SUPABASE_SERVICE_ROLE_KEY');
    if (!supabaseUrl || !serviceKey) {
      console.error('[user-sync] SUPABASE_URL or SUPABASE_SERVICE_ROLE_KEY is missing');
      return json({ ok: false, error: 'server misconfigured' }, 500);
    }

    const supabase = createClient(supabaseUrl, serviceKey, {
      auth: { persistSession: false, autoRefreshToken: false },
    });

    // 从 Supabase JWT 解析真实用户。绝不能信任 req.json() 里的 user 字段：
    // 任何人构造一个 user.id 就能改写别人的 user_profiles。
    const { data: authData, error: authError } = await supabase.auth.getUser(jwt);
    const user = authData?.user as unknown as AuthUser | undefined;
    if (authError || !user?.id) {
      return json({ ok: false, error: 'invalid or expired token' }, 401);
    }

    const gatewayUrl = Deno.env.get('GATEWAY_BASE_URL') || '';
    const gatewayProvider = Deno.env.get('GATEWAY_OIDC_PROVIDER') || 'newapi';
    const gatewayAccessToken =
      typeof user.app_metadata?.gateway_access_token === 'string'
        ? user.app_metadata.gateway_access_token
        : '';

    let gatewayProfile: GatewayProfile | null = null;
    if (gatewayUrl && gatewayAccessToken) {
      gatewayProfile = await fetchGatewayProfile(gatewayUrl, gatewayAccessToken);
    }

    // gateway_user_id 只能来自服务端事实：app_metadata、已验证的 OIDC sub，
    // 或网关 userinfo 的校验结果。
    let gatewayUserId = positiveInt(user.app_metadata?.gateway_user_id);
    if (!gatewayUserId && gatewayProfile) {
      gatewayUserId = gatewayProfile.sub;
    }
    if (!gatewayUserId) {
      const identity = user.identities?.find(
        (item) =>
          item.provider === gatewayProvider ||
          item.provider === `custom:${gatewayProvider}`,
      );
      gatewayUserId = positiveInt(identity?.identity_data?.sub);
    }
    if (!gatewayUserId) {
      return json({ ok: true, skipped: 'no gateway_user_id' });
    }

    const clientId = await getOrInitAppClientId(supabase);
    if (!clientId) {
      return json({ ok: false, error: 'app_config.client_id is not configured' }, 500);
    }

    const { data, error } = await supabase
      .from('user_profiles')
      .upsert({
        id: user.id,
        gateway_user_id: gatewayUserId,
        client_id: clientId,
        updated_at: new Date().toISOString(),
      }, { onConflict: 'id' });

    if (error) {
      console.error('[user-sync] user_profiles upsert failed:', error.message);
      // 23505 = unique_violation（gateway_user_id 已绑定到其他 Supabase 用户）
      const conflict = error.code === '23505';
      return json(
        { ok: false, error: conflict ? 'gateway_user_id already linked' : 'failed to sync profile' },
        conflict ? 409 : 500,
      );
    }

    // 可选：从网关补全 display_name（需要 profile scope）
    if (gatewayProfile?.displayName && gatewayProfile.sub === gatewayUserId) {
      const { error: nameError } = await supabase
        .from('user_profiles')
        .update({ display_name: gatewayProfile.displayName, updated_at: new Date().toISOString() })
        .eq('id', user.id);
      if (nameError) {
        console.error('[user-sync] display_name update failed:', nameError.message);
      }
    }

    return json({ ok: true, data });
  } catch (err) {
    console.error('[user-sync] unexpected error:', err);
    return json({ ok: false, error: 'internal error' }, 500);
  }
});

async function getOrInitAppClientId(
  supabase: ReturnType<typeof createClient>,
): Promise<string> {
  const { data: cfg, error } = await supabase
    .from('app_config')
    .select('client_id')
    .eq('id', 1)
    .maybeSingle();
  if (error) {
    console.error('[user-sync] app_config read failed:', error.message);
  }
  if (cfg?.client_id) return cfg.client_id;

  const fromEnv = Deno.env.get('GATEWAY_CLIENT_ID');
  if (!fromEnv) return '';

  const { error: writeError } = await supabase
    .from('app_config')
    .upsert({ id: 1, client_id: fromEnv }, { onConflict: 'id' });
  if (writeError) {
    console.error('[user-sync] app_config write failed:', writeError.message);
    return '';
  }
  return fromEnv;
}

async function fetchGatewayProfile(
  gatewayUrl: string,
  accessToken: string,
): Promise<GatewayProfile | null> {
  try {
    const res = await fetch(`${gatewayUrl.replace(/\/$/, '')}/oauth/userinfo`, {
      headers: { Authorization: `Bearer ${accessToken}` },
    });
    if (!res.ok) return null;
    const payload = await res.json() as { sub?: string | number; name?: string; username?: string };
    const sub = positiveInt(payload.sub);
    if (!sub) return null;
    return { sub, displayName: payload.name || payload.username };
  } catch (err) {
    console.error('[user-sync] gateway userinfo failed:', err);
    return null;
  }
}
