// lib/config.ts — 唯一配置入口；把 NEXT_PUBLIC_* 环境变量集中校验。
// 新增 App 时 99% 不需要改业务代码，只改这里对应的 NEXT_PUBLIC_ 值即可。
// 注意：Next.js 只会把以 NEXT_PUBLIC_ 开头、且以字面量形式访问的变量注入浏览器包，
// 所以这里必须逐个写出 process.env.XXX，不能用动态键名遍历。

const env = {
  supabaseUrl: process.env.NEXT_PUBLIC_SUPABASE_URL ?? '',
  supabaseAnonKey: process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY ?? '',
  gatewayBaseUrl: process.env.NEXT_PUBLIC_GATEWAY_BASE_URL ?? '',
  gatewayClientId: process.env.NEXT_PUBLIC_GATEWAY_CLIENT_ID ?? '',
};

const missing: string[] = [];
if (!env.supabaseUrl) missing.push('NEXT_PUBLIC_SUPABASE_URL');
if (!env.supabaseAnonKey) missing.push('NEXT_PUBLIC_SUPABASE_ANON_KEY');
if (!env.gatewayBaseUrl) missing.push('NEXT_PUBLIC_GATEWAY_BASE_URL');
if (!env.gatewayClientId) missing.push('NEXT_PUBLIC_GATEWAY_CLIENT_ID');

if (missing.length > 0) {
  // 不抛错让 next dev 能起来，但明确提示需要补
  // eslint-disable-next-line no-console
  console.warn(
    '[config] missing env vars:',
    missing.join(', '),
    '— copy .env.example to .env.local and fill them in',
  );
}

export const config = Object.freeze(env);
