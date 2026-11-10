import { redirect } from 'next/navigation';
import { LogoutButton } from '@/components/logout-button';
import { config } from '@/lib/config';
import { createServerSupabase } from '@/lib/supabase/server';

function Row({ k, v }: { k: string; v?: string }) {
  return (
    <div className="flex justify-between px-4 py-3 text-sm">
      <dt className="opacity-60">{k}</dt>
      <dd className="font-medium">{v || '—'}</dd>
    </div>
  );
}

// 个人中心：用户信息 + 跳转网关查看额度/充值。
export default async function ProfilePage() {
  const supabase = await createServerSupabase();
  const {
    data: { user },
  } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  // 网关 User.Id 来自 custom provider identity 的 sub；找不到时回退到
  // identities[0]（邮箱身份可能排在前面，仅作兜底）。
  const identity =
    user.identities?.find((item) => item.provider === 'custom:newapi') ?? user.identities?.[0];
  const identityData = identity?.identity_data as { sub?: string } | undefined;
  const userMeta = (user.user_metadata ?? {}) as Record<string, unknown>;

  // 显示名以 user-sync 从网关 /oauth/userinfo 同步的值为准；
  // id_token 只带认证事件声明，user_metadata 里没有 name。
  const { data: profile } = await supabase
    .from('user_profiles')
    .select('display_name')
    .eq('id', user.id)
    .maybeSingle();

  const name =
    (profile?.display_name as string | null | undefined) ??
    (userMeta.name as string | undefined) ??
    user.email ??
    undefined;

  return (
    <div className="mx-auto max-w-lg p-6">
      <h1 className="mb-4 text-2xl font-semibold">个人中心</h1>
      <dl className="divide-y divide-base-300 rounded-box border border-base-300 bg-base-100">
        <Row k="显示名" v={name} />
        <Row k="邮箱" v={user.email ?? '—'} />
        <Row k="网关 User.Id" v={identityData?.sub ?? '未同步'} />
      </dl>

      <div className="mt-6 space-y-3">
        <a
          href={`${config.gatewayBaseUrl}/app.html#/topup`}
          target="_blank"
          rel="noreferrer"
          className="btn btn-neutral w-full"
        >
          在网关管理平台余额
        </a>
        <p className="text-xs opacity-70">
          应用额度独立于平台余额：可在应用内调用 OAuth 充值接口把平台余额划转到本应用额度池。
        </p>
        <LogoutButton />
      </div>
    </div>
  );
}
