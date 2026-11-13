import type { Metadata } from 'next';
import type { ReactNode } from 'react';
import './globals.css';

export const metadata: Metadata = {
  title: 'AI 换装',
  description: 'new-api OAuth + Supabase Auth 换装应用',
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh-CN">
      <body className="min-h-screen bg-base-200 text-base-content">{children}</body>
    </html>
  );
}
