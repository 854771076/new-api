import { redirect } from 'next/navigation';
import { OutfitGenerator } from '@/components/outfit-generator';
import { createServerSupabase } from '@/lib/supabase/server';
import type { OutfitSummary } from '@/lib/types';

// 换装页：服装列表在服务端按 RLS 取好，交互交给 Client Component。
export default async function HomePage() {
  const supabase = await createServerSupabase();
  const {
    data: { user },
  } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const { data } = await supabase
    .from('official_outfits')
    .select('id, name, category, description, price_credits')
    .eq('is_active', true)
    .order('category');

  return <OutfitGenerator outfits={(data ?? []) as OutfitSummary[]} />;
}
