// src/lib/gateway.ts
// new-api Gateway 客户端封装——AI relay + 额度查询。
// 核心设计：网关侧 OAuth custom provider 成功登录后，Supabase 会把网关发的
// access_token 存进 auth.session.access_token（或手动写 app_metadata.gateway_access_token）。
// 我们把这个 token 当作 Bearer 直接打网关 relay，网关侧 middleware.oauth_token_auth
// 校验后把 client_id 注入 context，controller.Relay 白名单检查，然后 relay 上游 provider。

import { config } from './config';
import { getSupabase } from './supabase';

async function getGatewayAccessToken(): Promise<string | null> {
  const { data } = await getSupabase().auth.getSession();
  if (!data?.session) return null;

  // 优先读 AuthCallback 写入的 app_metadata（持久化 + 稳定）
  const meta = data.user?.app_metadata as Record<string, unknown> | undefined;
  const fromMeta = (meta?.gateway_access_token as string | undefined) ?? null;
  if (fromMeta) return fromMeta;

  // Fallback：AuthCallback 还没写（首次登录 + Edge Function 没注册），
  // 直接用当前 session.provider_token（Supabase Custom OIDC 的网关 access_token）
  return data.session.provider_token ?? null;
}

export class GatewayError extends Error {
  status: number;
  code?: string;
  constructor(status: number, message: string, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

// ===== 1. 对话式 AI（流式） =====
export interface ChatMessage { role: 'system' | 'user' | 'assistant'; content: string; }

export interface ChatStreamCallbacks {
  onDelta?: (text: string) => void;
  onDone?: (fullText: string, usage?: { prompt_tokens: number; completion_tokens: number }) => void;
  onError?: (err: GatewayError) => void;
}

export async function streamChatCompletion(
  model: string,
  messages: ChatMessage[],
  callbacks: ChatStreamCallbacks,
  signal?: AbortSignal,
) {
  const token = await getGatewayAccessToken();
  if (!token) {
    callbacks.onError?.(new GatewayError(401, 'gateway OAuth access_token missing — 请先完成 OIDC 登录'));
    return;
  }

  const body = JSON.stringify({
    model,
    stream: true,
    messages,
  });

  let res: Response;
  try {
    res = await fetch(`${config.gatewayBaseUrl}/v1/chat/completions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${token}`,
      },
      body,
      signal,
    });
  } catch (e) {
    callbacks.onError?.(new GatewayError(0, (e as Error).message));
    return;
  }

  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => '');
    callbacks.onError?.(new GatewayError(res.status, text || res.statusText));
    return;
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let fullText = '';

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      // SSE: 每条事件以 "data: " 开头、空行分隔
      const events = buffer.split('\n\n');
      buffer = events.pop() ?? '';

      for (const event of events) {
        const line = event.split('\n').find((l) => l.startsWith('data:'));
        if (!line) continue;
        const data = line.slice(5).trim();
        if (data === '[DONE]') continue;
        let json: { choices?: { delta?: { content?: string } }[]; usage?: unknown };
        try { json = JSON.parse(data); } catch { continue; }
        const delta = json.choices?.[0]?.delta?.content;
        if (delta) {
          fullText += delta;
          callbacks.onDelta?.(delta);
        }
      }
    }
    callbacks.onDone?.(fullText);
  } catch (e) {
    callbacks.onError?.(new GatewayError(0, (e as Error).message));
  }
}

// ===== 2. 非流式（用于简单补全等） =====
export async function chatCompletion(
  model: string,
  messages: ChatMessage[],
): Promise<string> {
  const token = await getGatewayAccessToken();
  if (!token) throw new GatewayError(401, 'gateway OAuth access_token missing');

  const res = await fetch(`${config.gatewayBaseUrl}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ model, stream: false, messages }),
  });
  if (!res.ok) throw new GatewayError(res.status, await res.text());
  const json = await res.json() as { choices: { message: { content: string } }[] };
  return json.choices[0]?.message?.content ?? '';
}

// ===== 3. 查询当前用户在网关的额度（用户直充后刷新显示） =====
export async function getGatewayQuota(): Promise<{ quota: number; used: number; subscription?: number } | null> {
  // gateway 没有公开的 OAuth 专属 quota 查询端点；用 OAuth access_token 直连
  // /api/user/amount（需要登录态或 OAuth 放行）。脚手架用 OAuth token 试试，
  // 失败（401/403）返回 null，UI 降级显示"—"。
  const token = await getGatewayAccessToken();
  if (!token) return null;
  try {
    const res = await fetch(`${config.gatewayBaseUrl}/api/user/amount`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok) return null;
    return (await res.json()).data ?? null;
  } catch { return null; }
}
