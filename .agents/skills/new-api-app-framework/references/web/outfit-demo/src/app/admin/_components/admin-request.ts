'use client';

import { message } from 'antd';

export async function adminRequest<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, { ...init, credentials: 'same-origin', cache: 'no-store' });
  if (!response.ok) throw new Error('请求失败，请重试');
  return await response.json() as T;
}

export async function adminMutation(url: string, method: 'POST' | 'PATCH' | 'DELETE', values?: unknown): Promise<boolean> {
  try {
    await adminRequest(url, {
      method,
      ...(values === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(values) }),
    });
    return true;
  } catch {
    message.error('保存失败，请重试');
    return false;
  }
}
