'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { ReactNode } from 'react';
import { Layout, Menu } from 'antd';
import {
  DashboardOutlined,
  AppstoreOutlined,
  TeamOutlined,
  IdcardOutlined,
  HistoryOutlined,
  CrownOutlined,
  SettingOutlined,
  BlockOutlined,
  FileTextOutlined,
} from '@ant-design/icons';
import { useState } from 'react';

const { Header, Sider, Content } = Layout;

const MENU_ITEMS = [
  { key: '/admin', icon: <DashboardOutlined />, label: '运营概览' },
  { key: '/admin/outfits', icon: <AppstoreOutlined />, label: '服装管理' },
  { key: '/admin/poses', icon: <TeamOutlined />, label: '姿势管理' },
  { key: '/admin/characters', icon: <IdcardOutlined />, label: '人物管理' },
  { key: '/admin/generations', icon: <HistoryOutlined />, label: '生成记录' },
  { key: '/admin/models', icon: <BlockOutlined />, label: '模型设置' },
  { key: '/admin/prompts', icon: <FileTextOutlined />, label: '提示词模板' },
  { key: '/admin/plans', icon: <SettingOutlined />, label: '套餐管理' },
];

export default function AdminLayoutInner({ children, role }: { children: ReactNode; role: string }) {
  const pathname = usePathname();
  const [collapsed, setCollapsed] = useState(false);

  return (
    <Layout className="min-h-screen">
      <Header className="flex items-center justify-between bg-slate-900 !text-white px-6">
        <div className="text-lg font-bold">AI 换装 · 管理后台</div>
        <div className="flex items-center gap-4">
          {role === 'owner' && <CrownOutlined />}
          <span className="text-xs opacity-70">权限：{role}</span>
          <Link href="/" className="text-xs opacity-70 hover:opacity-100">← 返回应用</Link>
        </div>
      </Header>
      <Layout>
        <Sider collapsible collapsed={collapsed} onCollapse={setCollapsed} breakpoint="lg">
          <Menu
            theme="dark"
            mode="inline"
            selectedKeys={[pathname]}
            items={MENU_ITEMS}
          />
        </Sider>
        <Layout className="bg-slate-50">
          <Content className="p-6">{children}</Content>
        </Layout>
      </Layout>
    </Layout>
  );
}
