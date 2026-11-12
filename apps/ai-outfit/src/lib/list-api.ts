// lib/list-api.ts — 消费端列表分页的共享常量与 URL 工具。
// 该模块同时被服务端 Route Handler 与客户端组件引用，因此不能引入 'server-only'。
export const LIST_PAGE_SIZE = 20;
const LIST_LIMIT_MAX = 60;

export function clampPage(raw: string | null | undefined): number {
  const n = Number(raw ?? '1');
  return Number.isSafeInteger(n) && n > 0 ? n : 1;
}

export function clampLimit(raw: string | null | undefined): number {
  const n = Number(raw ?? String(LIST_PAGE_SIZE));
  if (!Number.isSafeInteger(n) || n < 1) return LIST_PAGE_SIZE;
  return Math.min(n, LIST_LIMIT_MAX);
}

// 媒体代理路径：/api/media 会经用户 RLS 动态签名后 302 跳转，前端直接用 <img src> 即可。
export function mediaUrl(bucket: string | null | undefined, path: string | null | undefined): string | null {
  if (!bucket || !path) return null;
  return `/api/media/${bucket}/${path}`;
}
