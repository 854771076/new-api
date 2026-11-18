// lib/config.ts — 唯一配置入口；把 NEXT_PUBLIC_* 环境变量集中校验。
const env = {
  integrationMode: process.env.NEXT_PUBLIC_GATEWAY_INTEGRATION_MODE ?? 'proxy',
  supabaseUrl: process.env.NEXT_PUBLIC_SUPABASE_URL ?? '',
  supabaseAnonKey: process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? '',
  gatewayBaseUrl: process.env.NEXT_PUBLIC_GATEWAY_BASE_URL ?? '',
  gatewayClientId: process.env.NEXT_PUBLIC_GATEWAY_CLIENT_ID ?? `proxy:${process.env.NEXT_PUBLIC_GATEWAY_APP_ID ?? ''}`,
  gatewayAppId: Number(process.env.NEXT_PUBLIC_GATEWAY_APP_ID ?? 0),
  gatewayPublicUrl: process.env.GATEWAY_PUBLIC_URL ?? process.env.NEXT_PUBLIC_GATEWAY_BASE_URL ?? '',
  appOrigin: process.env.APP_ORIGIN ?? '',
  initialAdminSub: process.env.INITIAL_ADMIN_SUB ?? '',
};

const missing: string[] = [];
if (!env.supabaseUrl) missing.push('NEXT_PUBLIC_SUPABASE_URL');
if (!env.supabaseAnonKey) missing.push('NEXT_PUBLIC_SUPABASE_ANON_KEY');
if (!env.gatewayBaseUrl) missing.push('NEXT_PUBLIC_GATEWAY_BASE_URL');
if (!env.gatewayClientId) missing.push('NEXT_PUBLIC_GATEWAY_CLIENT_ID');

if (missing.length > 0) {
  // eslint-disable-next-line no-console
  console.warn(
    '[config] missing env vars:',
    missing.join(', '),
    '— configure .env.local using README.md',
  );
}

export const config = Object.freeze(env);
