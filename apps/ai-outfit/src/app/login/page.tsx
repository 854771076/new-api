import { redirect } from 'next/navigation';
import { LoginCard } from '@/components/login-card';
import { createServerSupabase } from '@/lib/supabase/server';

export default async function LoginPage({
  searchParams,
}: {
  searchParams: Promise<{ error?: string }>;
}) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (user) redirect('/studio');

  const { error } = await searchParams;

  return (
    <div className="flex min-h-[calc(100vh-4rem)] items-center justify-center">
      <div className="w-full max-w-md">
        <LoginCard errorCode={error} />
      </div>
    </div>
  );
}
