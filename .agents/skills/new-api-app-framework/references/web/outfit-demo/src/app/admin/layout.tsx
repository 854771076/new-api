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
    if (check.response.status === 401) redirect('/login');
    return <div role="alert">无权访问管理后台</div>;
  }

  return (
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          borderRadius: 8,
          colorPrimary: '#6366f1',
          colorBgLayout: '#f5f6fa',
          colorBgContainer: '#ffffff',
          colorBorderSecondary: '#e8eaf0',
        },
        components: {
          Layout: {
            headerBg: '#ffffff',
            headerPadding: 24,
            siderBg: '#ffffff',
            bodyBg: '#f5f6fa',
          },
          Menu: {
            itemBorderRadius: 8,
            itemMarginInline: 8,
          },
          Table: {
            headerBg: '#f8f9fc',
            cellPaddingBlock: 12,
          },
        },
      }}
    >
      <AntdApp>
        <AdminLayoutInner role={check.role}>{children}</AdminLayoutInner>
      </AntdApp>
    </ConfigProvider>
  );
}
