import type { Metadata } from 'next';
import type { ReactNode } from 'react';
import { ConfigProvider, App as AntdApp } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { requireAdmin } from '@/lib/admin/requireAdmin';
import AdminLayoutInner from './_components/layout-inner';
import { redirect } from 'next/navigation';

export const metadata: Metadata = { title: 'AI 换装 · 管理后台' };

export const dynamic = 'force-dynamic';

export default async function AdminLayout({ children }: { children: ReactNode }) {
  const check = await requireAdmin();
  if (!check.ok) {
    // 403 或 重定向到登录；这里直接 redirect
    redirect('/login');
  }

  return (
    <ConfigProvider
      locale={zhCN}
      theme={{ token: { borderRadius: 8, colorPrimary: '#6366f1' } }}
    >
      <AntdApp>
        <AdminLayoutInner role={check.role}>{children}</AdminLayoutInner>
      </AntdApp>
    </ConfigProvider>
  );
}
