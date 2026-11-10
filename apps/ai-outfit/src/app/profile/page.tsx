import { redirect } from 'next/navigation';
import { createServerSupabase } from '@/lib/supabase/server';
import { config } from '@/lib/config';
import { ProfileClient } from './_components/ProfileClient';

export const dynamic = 'force-dynamic';

export default async function ProfilePage() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  // SSR 只校验登录 + 传基础 user 信息给 Client Component；额度/日志由客户端走 BFF 拿
  return <ProfileClient user={user} consoleUrl={config.gatewayPublicUrl} />;
}
