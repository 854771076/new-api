-- supabase/functions/user-sync/index.ts
--
-- Supabase Edge Function: 在首次 OIDC 登录时，把网关 id_token.sub（网关侧
-- User.Id）写入 auth.users.app_metadata.gateway_user_id 并在 public.user_profiles
-- 里建一行。
--
-- 两种触发方式：
--   1. Supabase Auth → Hooks → Access Token Hook（每个会话签发时自动调用）
--   2. App 端首次登录后手动 POST 到此函数
--
-- 环境变量：
--   SUPABASE_URL / SUPABASE_SERVICE_ROLE_KEY  —— Edge Function 自身运行时提供
--   GATEWAY_BASE_URL                          —— 网关地址，用于可选地在同步后
--                                               查询 /oauth/userinfo 补全字段
--   GATEWAY_CLIENT_ID / GATEWAY_CLIENT_SECRET —— 可选；如果要让 user-sync 代 App
--                                               调用 userinfo，需要这些
import { createClient } from 'https://esm.sh/@supabase/supabase-js@2';

interface AuthUser {
  id: string;
  app_metadata?: Record<string, unknown>;
}

Deno.serve(async (req) => {
  try {
    if (req.method !== 'POST') {
      return new Response('method not allowed', { status: 405 });
    }

    const supabaseUrl = Deno.env.get('SUPABASE_URL')!;
    const serviceKey  = Deno.env.get('SUPABASE_SERVICE_ROLE_KEY')!;
    const gatewayUrl  = Deno.env.get('GATEWAY_BASE_URL') || '';

    const supabase = createClient(supabaseUrl, serviceKey, {
      auth: { persistSession: false, autoRefreshToken: false },
    });

    const { user } = await req.json() as { user: AuthUser };
    if (!user?.id) {
      return new Response('missing user.id', { status: 400 });
    }

    // 从 OIDC 登录时 Supabase 自动写入的 app_metadata 里读 gateway_user_id
    const existing = (user.app_metadata?.gateway_user_id as number | undefined);
    if (!existing) {
      // 不是走 OIDC 来的；静默跳过。如果运营在 Supabase 上开了 email 登录
      // 然后又想绑定网关，可以让 App 端在后续 OIDC 时再触发一次。
      return new Response(JSON.stringify({ ok: true, skipped: 'no gateway_user_id' }), {
        headers: { 'content-type': 'application/json' },
      });
    }

    const clientId = await getOrInitAppClientId(supabase);

    // 写 user_profiles（upsert 保证幂等）
    const { data, error } = await supabase
      .from('user_profiles')
      .upsert({
        id: user.id,
        gateway_user_id: existing,
        client_id: clientId,
        updated_at: new Date().toISOString(),
      }, { onConflict: 'id' });

    if (error) {
      return new Response(JSON.stringify({ ok: false, error: error.message }), {
        status: 500,
        headers: { 'content-type': 'application/json' },
      });
    }

    // 可选：从网关补全 display_name（需要 userinfo scope 已授权）
    if (gatewayUrl && user.app_metadata?.gateway_access_token) {
      try {
        const displayName = await fetchDisplayName(
          gatewayUrl,
          String(user.app_metadata.gateway_access_token),
        );
        if (displayName) {
          await supabase.from('user_profiles').update({ display_name: displayName }).eq('id', user.id);
        }
      } catch (_) {
        // 补全失败不影响主流程
      }
    }

    return new Response(JSON.stringify({ ok: true, data }), {
      headers: { 'content-type': 'application/json' },
    });
  } catch (err) {
    return new Response(JSON.stringify({ ok: false, error: String(err) }), {
      status: 500,
      headers: { 'content-type': 'application/json' },
    });
  }
});

async function getOrInitAppClientId(
  supabase: ReturnType<typeof createClient>,
): Promise<string> {
  const { data: cfg } = await supabase.from('app_config').select('client_id').limit(1);
  if (cfg && cfg.length > 0 && cfg[0].client_id) return cfg[0].client_id;

  const fromEnv = Deno.env.get('GATEWAY_CLIENT_ID');
  if (fromEnv) {
    await supabase.from('app_config').insert({ client_id: fromEnv });
    return fromEnv;
  }
  // 兜底：空字符串会在 RLS 里被 current_app_client_id() 返回；所有策略会
  // 拒绝掉业务表的读写。开发阶段脚手架会在 dev 初始化时写入正确值。
  return '';
}

async function fetchDisplayName(gatewayUrl: string, accessToken: string): Promise<string | undefined> {
  const res = await fetch(`${gatewayUrl}/oauth/userinfo`, {
    headers: { Authorization: `Bearer ${accessToken}` },
  });
  if (!res.ok) return undefined;
  const json = await res.json() as { name?: string; username?: string };
  return json.name || json.username;
}
