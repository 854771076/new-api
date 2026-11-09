// src/lib/config.ts — 唯一配置入口；把 VITE_* 环境变量集中校验。
// 新增 App 时 99% 不需要改业务代码，只改这里对应的 VITE_ 值即可。

const env = {
  supabaseUrl:      import.meta.env.VITE_SUPABASE_URL     ?? '',
  supabaseAnonKey:  import.meta.env.VITE_SUPABASE_ANON_KEY ?? '',
  gatewayBaseUrl:   import.meta.env.VITE_GATEWAY_BASE_URL  ?? '',
  gatewayClientId:  import.meta.env.VITE_GATEWAY_CLIENT_ID ?? '',
};

const missing: string[] = [];
if (!env.supabaseUrl)      missing.push('VITE_SUPABASE_URL');
if (!env.supabaseAnonKey)  missing.push('VITE_SUPABASE_ANON_KEY');
if (!env.gatewayBaseUrl)   missing.push('VITE_GATEWAY_BASE_URL');
if (!env.gatewayClientId)  missing.push('VITE_GATEWAY_CLIENT_ID');

if (missing.length > 0) {
  // 不抛错让 vite dev 能起来，但明确提示需要补
  // eslint-disable-next-line no-console
  console.warn('[config] missing env vars:', missing.join(', '),
    '— copy .env.example to .env.local and fill them in');
}

export const config = Object.freeze(env);
