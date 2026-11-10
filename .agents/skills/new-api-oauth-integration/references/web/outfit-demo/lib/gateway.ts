// lib/gateway.ts
// new-api Gateway 客户端封装——AI relay。
// 网关 OAuth access_token 由 gatewayAuth 统一持有与自动刷新（public client，
// 无 client_secret）：网关侧 middleware.oauth_token_auth 校验后把 client_id
// 注入 context，controller.Relay 白名单检查，然后 relay 上游 provider。

import { config } from '@/lib/config';
import { getGatewayAccessToken, refreshGatewayToken } from '@/lib/gatewayAuth';

export class GatewayError extends Error {
  status: number;
  code?: string;
  constructor(status: number, message: string, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

function normalizeError(err: unknown): GatewayError {
  if (err instanceof GatewayError) return err;
  return new GatewayError(401, err instanceof Error ? err.message : String(err));
}

/**
 * 带 Bearer 的网关 POST。401 时强制刷新一次令牌并重试一次，
 * 仍失败则把第二个响应原样交给调用方，绝不循环重试。
 */
async function postJsonWithAuth(
  path: string,
  body: string,
  signal?: AbortSignal,
): Promise<Response> {
  const send = (token: string) =>
    fetch(`${config.gatewayBaseUrl}${path}`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${token}`,
      },
      body,
      signal,
    });

  const res = await send(await getGatewayAccessToken());
  if (res.status !== 401) return res;

  await res.body?.cancel().catch(() => undefined);
  return send(await refreshGatewayToken());
}

// ===== 1. 对话式 AI（流式） =====
export interface ChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string;
}

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
  const body = JSON.stringify({
    model,
    stream: true,
    messages,
  });

  let res: Response;
  try {
    res = await postJsonWithAuth('/v1/chat/completions', body, signal);
  } catch (e) {
    callbacks.onError?.(normalizeError(e));
    return;
  }

  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => '');
    const message = text || (res.status === 401 ? '网关登录已失效，请重新登录' : res.statusText);
    callbacks.onError?.(new GatewayError(res.status, message));
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
        try {
          json = JSON.parse(data);
        } catch {
          continue;
        }
        const delta = json.choices?.[0]?.delta?.content;
        if (delta) {
          fullText += delta;
          callbacks.onDelta?.(delta);
        }
      }
    }
    callbacks.onDone?.(fullText);
  } catch (e) {
    callbacks.onError?.(normalizeError(e));
  }
}

// ===== 2. 非流式（用于简单补全等） =====
export async function chatCompletion(model: string, messages: ChatMessage[]): Promise<string> {
  const res = await postJsonWithAuth(
    '/v1/chat/completions',
    JSON.stringify({ model, stream: false, messages }),
  );

  if (!res.ok) {
    const text = await res.text();
    const message = text || (res.status === 401 ? '网关登录已失效，请重新登录' : res.statusText);
    throw new GatewayError(res.status, message);
  }

  const json = (await res.json()) as { choices: { message: { content: string } }[] };
  return json.choices[0]?.message?.content ?? '';
}
