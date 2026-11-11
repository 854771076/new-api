'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import type { ReactNode } from 'react';
import { useState } from 'react';
import { Layout, Menu, Drawer, Button, Grid, theme } from 'antd';
import {
  DashboardOutlined,
  AppstoreOutlined,
  TeamOutlined,
  IdcardOutlined,
  HistoryOutlined,
  CrownOutlined,
  BlockOutlined,
  FileTextOutlined,
  MenuOutlined,
} from '@ant-design/icons';

const { Header, Sider, Content } = Layout;
const SIDER_WIDTH = 208;

const MENU_ITEMS = [
  { key: '/admin', icon: <DashboardOutlined />, label: '运营概览' },
  { key: '/admin/outfits', icon: <AppstoreOutlined />, label: '服装管理' },
  { key: '/admin/poses', icon: <TeamOutlined />, label: '姿势管理' },
  { key: '/admin/characters', icon: <IdcardOutlined />, label: '人物管理' },
  { key: '/admin/generations', icon: <HistoryOutlined />, label: '生成记录' },
  { key: '/admin/models', icon: <BlockOutlined />, label: '模型设置' },
  { key: '/admin/prompts', icon: <FileTextOutlined />, label: '提示词模板' },
];

function NavMenu({ pathname, onNavigate }: { pathname: string; onNavigate?: () => void }) {
  return (
    <Menu
      theme="light"
      mode="inline"
      selectedKeys={[pathname]}
      items={MENU_ITEMS.map((item) => ({
        ...item,
        label: <Link href={item.key} onClick={onNavigate}>{item.label}</Link>,
      }))}
    />
  );
}

export default function AdminLayoutInner({ children, role }: { children: ReactNode; role: string }) {
  const pathname = usePathname();
  const screens = Grid.useBreakpoint();
  const isDesktop = Boolean(screens.lg);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const {
    token: { colorBgContainer, colorBorderSecondary, colorTextSecondary, colorTextHeading },
  } = theme.useToken();

  return (
    <Layout className="min-h-screen">
      <Header
        className="flex items-center justify-between gap-3 px-4 md:px-6"
        style={{ background: colorBgContainer, borderBottom: `1px solid ${colorBorderSecondary}` }}
      >
        <div className="flex min-w-0 items-center gap-3">
          {!isDesktop && (
            <Button
              type="text"
              aria-label="打开导航菜单"
              icon={<MenuOutlined />}
              onClick={() => setDrawerOpen(true)}
            />
          )}
          <div className="truncate text-base font-bold md:text-lg" style={{ color: colorTextHeading }}>
            AI 换装 · 管理后台
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-3 md:gap-4" style={{ color: colorTextSecondary }}>
          {role === 'owner' && <CrownOutlined />}
          <span className="hidden text-xs sm:inline">权限：{role}</span>
          <Link href="/studio" className="whitespace-nowrap text-xs hover:opacity-100" style={{ color: colorTextSecondary }}>
            ← 返回应用
          </Link>
        </div>
      </Header>
      <Layout>
        {isDesktop ? (
          <Sider
            theme="light"
            width={SIDER_WIDTH}
            style={{ background: colorBgContainer, borderRight: `1px solid ${colorBorderSecondary}` }}
          >
            <NavMenu pathname={pathname} />
          </Sider>
        ) : (
          <Drawer
            placement="left"
            open={drawerOpen}
            onClose={() => setDrawerOpen(false)}
            size={Math.min(SIDER_WIDTH, 280)}
            styles={{ body: { padding: 0, background: colorBgContainer } }}
            title="管理后台"
          >
            <NavMenu pathname={pathname} onNavigate={() => setDrawerOpen(false)} />
          </Drawer>
        )}
        <Layout style={{ background: 'transparent' }}>
          <Content className="p-4 md:p-6">
            <div className="mx-auto w-full max-w-screen-2xl">{children}</div>
          </Content>
        </Layout>
      </Layout>
    </Layout>
  );
}
