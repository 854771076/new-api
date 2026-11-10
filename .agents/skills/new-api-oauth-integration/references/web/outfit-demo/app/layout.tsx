import type { Metadata } from 'next';
import Link from 'next/link';
import type { ReactNode } from 'react';
import './globals.css';
import { createServerSupabase } from '@/lib/supabase/server';

export const metadata: Metadata = {
  title: 'AI App Starter',
  description: 'new-api OAuth + Supabase Auth 参考应用',
};

const NAV_LINKS = [
  { href: '/', label: 'AI 换装' },
  { href: '/wardrobe', label: '服装库' },
  { href: '/history', label: '历史' },
  { href: '/profile', label: '个人中心' },
];

// 布局在服务端读取会话：未登录时不渲染导航，与旧版 SPA 保持一致。
export default async function RootLayout({ children }: { children: ReactNode }) {
  const supabase = await createServerSupabase();
  const {
    data: { user },
  } = await supabase.auth.getUser();

  return (
    <html lang="zh-CN" data-theme="light">
      <body className="min-h-screen bg-base-200 text-base-content">
        {user && (
          <nav className="navbar sticky top-0 z-10 border-b border-base-300 bg-base-100/80 backdrop-blur">
            <div className="mx-auto flex w-full max-w-3xl items-center gap-4 px-6 text-sm">
              {NAV_LINKS.map((link) => (
                <Link key={link.href} href={link.href} className="link link-hover">
                  {link.label}
                </Link>
              ))}
            </div>
          </nav>
        )}
        {children}
      </body>
    </html>
  );
}
