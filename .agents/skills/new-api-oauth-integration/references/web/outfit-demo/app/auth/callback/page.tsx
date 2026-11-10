import { Suspense } from 'react';
import { AuthCallback } from '@/components/auth-callback';

// useSearchParams 需要 Suspense 边界，否则构建时无法确定静态外壳。
export default function AuthCallbackPage() {
  return (
    <Suspense fallback={<div className="p-6 text-center">登录完成，正在跳转…</div>}>
      <AuthCallback />
    </Suspense>
  );
}
