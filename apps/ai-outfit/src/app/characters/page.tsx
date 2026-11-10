import { redirect } from 'next/navigation';
import Link from 'next/link';
import { createServerSupabase } from '@/lib/supabase/server';
import { getSignedUrls } from '@/lib/media-urls';
import { CharactersClient } from './_components/CharactersClient';

export const dynamic = 'force-dynamic';

export default async function CharactersPage() {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();
  if (!user) redirect('/login');

  const { data: chars, error } = await supabase
    .from('characters')
    .select('*')
    .eq('user_id', user.id)
    .order('created_at', { ascending: false });

  if (error) {
    return <div className="alert alert-error">读取人物列表失败：{error.message}</div>;
  }

  const paths = (chars ?? []).map((c) => c.image_path);
  const signedMap = await getSignedUrls('media-user', paths);

  const items = (chars ?? []).map((c) => ({
    id: c.id,
    name: c.name,
    description: c.description,
    is_public: c.is_public,
    created_at: c.created_at,
    signed_url: signedMap[c.image_path],
  }));

  return (
    <CharactersClient
      initialItems={items}
      uploaderSlot={
        <div className="card bg-base-100 shadow">
          <div className="card-body">
            <h2 className="mb-4 text-lg font-semibold">上传新人物</h2>
            <p className="mb-3 text-sm text-base-content/60">
              人物图会存到 Supabase Storage（media-user bucket），仅你自己可见。
            </p>
            {/* 上传 Modal 由 CharactersClient 触发；这里只放固定入口 */}
            <p className="text-xs text-base-content/50">
              点击右上角「上传人物」按钮或下方按钮打开上传窗口。
            </p>
          </div>
        </div>
      }
    />
  );
}

// 避免 ESLint 抱怨未使用的 Link
void Link;
