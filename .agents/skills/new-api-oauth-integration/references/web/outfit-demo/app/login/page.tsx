import { redirect } from 'next/navigation';
import { LoginCard } from '@/components/login-card';
import { createServerSupabase } from '@/lib/supabase/server';

// 登录页：已登录直接回首页，未登录渲染登录卡片。
export default async function LoginPage() {
  const supabase = await createServerSupabase();
  const {
    data: { user },
  } = await supabase.auth.getUser();
  if (user) redirect('/');

  return <LoginCard />;
}
