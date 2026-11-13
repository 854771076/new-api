import { redirect } from 'next/navigation';
import { createServerSupabase } from '@/lib/supabase/server';
import { ProfileClient } from './_components/ProfileClient';

export const dynamic = 'force-dynamic';

export default async function ProfilePage() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  // 仅序列化展示字段，不能把可能携带 provider token 的完整 metadata 传入浏览器。
  const profileUser = {
    id: user.id,
    email: user.email,
    app_metadata: {
      provider: typeof user.app_metadata.provider === 'string' ? user.app_metadata.provider : undefined,
    },
    user_metadata: {
      name: typeof user.user_metadata.name === 'string' ? user.user_metadata.name : undefined,
      full_name: typeof user.user_metadata.full_name === 'string' ? user.user_metadata.full_name : undefined,
    },
  };

  // 额度、目录和日志独立走 BFF；不在 Server Component 渲染期间刷新网关凭证或修改 Cookie。
  return <ProfileClient user={profileUser} />;
}
