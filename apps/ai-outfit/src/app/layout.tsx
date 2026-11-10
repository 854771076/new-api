import type { Metadata } from 'next';
import Link from 'next/link';
import type { ReactNode } from 'react';
import './globals.css';
import { createServerSupabase } from '@/lib/supabase/server';
import { LogoutButton } from '@/components/logout-button';
import { GatewayAuthBootstrap } from '@/components/gateway-auth-bootstrap';
import { config } from '@/lib/config';
import {
  Shirt,
  PersonStanding,
  UserRound,
  History,
  LayoutTemplate,
  UserCog,
  Menu,
} from 'lucide-react';

export const metadata: Metadata = {
  title: 'AI 换装',
  description: 'new-api OAuth + Supabase Auth 换装应用',
};

const NAV_ITEMS = [
  { href: '/studio', label: '换装工作室', icon: LayoutTemplate },
  { href: '/wardrobe', label: '服装库', icon: Shirt },
  { href: '/poses', label: '姿势库', icon: PersonStanding },
  { href: '/characters', label: '我的人物', icon: UserRound },
  { href: '/history', label: '作品历史', icon: History },
];

export default async function RootLayout({ children }: { children: ReactNode }) {
  const supabase = await createServerSupabase();
  const { data: { user } } = await supabase.auth.getUser();

  return (
    <html lang="zh-CN" data-theme="light">
      <body className="min-h-screen bg-base-200 text-base-content">
        {user ? (
          <>
            <GatewayAuthBootstrap />
            {/* DaisyUI Navbar */}
            <div className="navbar sticky top-0 z-30 border-b border-base-300 bg-base-100/80 backdrop-blur">
              <div className="mx-auto w-full max-w-6xl">
                {/* Mobile drawer toggle */}
                <div className="navbar-start">
                  <label htmlFor="app-drawer" className="btn btn-ghost drawer-button lg:hidden" aria-label="菜单">
                    <Menu size={20} />
                  </label>
                  <Link href="/studio" className="btn btn-ghost text-lg font-bold">
                    <span className="hidden sm:inline">AI 换装</span>
                    <span className="sm:hidden">AI 换装</span>
                  </Link>
                </div>

                {/* Desktop menu */}
                <div className="navbar-center hidden lg:flex">
                  <ul className="menu menu-horizontal gap-1 px-1">
                    {NAV_ITEMS.map(({ href, label, icon: Icon }) => (
                      <li key={href}>
                        <Link href={href} className="gap-2">
                          <Icon size={16} />
                          <span>{label}</span>
                        </Link>
                      </li>
                    ))}
                  </ul>
                </div>

                {/* Profile dropdown */}
                <div className="navbar-end">
                  <div className="dropdown dropdown-end">
                    <div tabIndex={0} role="button" className="btn btn-ghost btn-circle avatar placeholder">
                      <UserCog size={20} />
                    </div>
                    <ul tabIndex={0} className="menu dropdown-content bg-base-100 rounded-box z-[1] mt-3 w-52 shadow border border-base-300">
                      <li className="menu-title"><span>{user.email ?? user.id}</span></li>
                      <li><Link href="/profile" className="gap-2"><UserRound size={16} />个人中心</Link></li>
                      <li><Link href={`${config.gatewayPublicUrl}/app.html#/topup`} target="_blank" rel="noreferrer" className="gap-2">充值额度 ↗</Link></li>
                      <li><Link href={`${config.gatewayPublicUrl}/app.html#/log`} target="_blank" rel="noreferrer" className="gap-2">消费日志 ↗</Link></li>
                      <li className="menu-title"><span /></li>
                      <li><LogoutButton /></li>
                    </ul>
                  </div>
                </div>
              </div>
            </div>

            {/* Mobile drawer */}
            <div className="drawer lg:hidden">
              <input id="app-drawer" type="checkbox" className="drawer-toggle" />
              <div className="drawer-side">
                <label htmlFor="app-drawer" aria-label="关闭菜单" className="drawer-overlay" />
                <ul className="menu bg-base-100 text-base-content min-h-full w-64 gap-1 p-4">
                  <li className="menu-title"><span className="text-lg font-bold">AI 换装</span></li>
                  {NAV_ITEMS.map(({ href, label, icon: Icon }) => (
                    <li key={href}>
                      <Link href={href} className="gap-2">
                        <Icon size={16} />
                        <span>{label}</span>
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            </div>

            <main className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6">
              {children}
            </main>
          </>
        ) : (
          <main className="mx-auto flex min-h-screen w-full max-w-md items-center justify-center p-4">
            {children}
          </main>
        )}
      </body>
    </html>
  );
}
