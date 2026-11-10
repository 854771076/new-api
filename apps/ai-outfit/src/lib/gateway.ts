// lib/gateway.ts — new-api Gateway 客户端封装（AI relay + OAuth 管理 API）。
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
 * 客户端用：带 Bearer token 发请求，401 时自动 refresh 一次。
 */
async function requestWithAuth(
  method: string,
  path: string,
  opts?: { body?: string; query?: Record<string, string | number | undefined>; accessToken?: string },
): Promise<Response> {
  const qs = opts?.query
    ? '?' +
      Object.entries(opts.query)
        .filter(([, v]) => v !== undefined && v !== null && v !== '')
        .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
        .join('&')
    : '';

  const url = `${config.gatewayBaseUrl}${path}${qs}`;

  const send = async (token: string) =>
    fetch(url, {
      method,
      headers: {
        Authorization: `Bearer ${token}`,
        ...(opts?.body ? { 'Content-Type': 'application/json' } : {}),
      },
      body: opts?.body,
    });

  // 优先用传入的 token（服务器端 Route Handler），否则走 gatewayAuth（客户端）
  const firstToken = opts?.accessToken ?? (await getGatewayAccessToken());
  const res = await send(firstToken);
  if (res.status !== 401) return res;
  await res.body?.cancel().catch(() => undefined);
  // 只 refresh 一次；服务器端如果传了 token 则跳过 refresh
  if (opts?.accessToken) return res;
  const refreshed = await refreshGatewayToken();
  return send(refreshed);
}

async function readJson(res: Response): Promise<unknown> {
  const text = await res.text().catch(() => '');
  if (!text) return null;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return null;
  }
}

// 旧版网关没有该路由时会回落到 SPA 的 index.html（200 + text/html），
// 统一识别为“接口不支持”，避免把 HTML 当成解析失败抛给用户。
const UNSUPPORTED_MESSAGE = '当前网关版本未启用该接口';

async function readErrorText(res: Response): Promise<string> {
  const text = await res.text().catch(() => '');
  if (!text) return res.statusText || `HTTP ${res.status}`;
  try {
    const json = JSON.parse(text) as { error_description?: unknown; message?: unknown };
    if (typeof json.error_description === 'string' && json.error_description) return json.error_description;
    if (typeof json.message === 'string' && json.message) return json.message;
  } catch {
    // 不是 JSON，走下面的兜底
  }
  if (text.trimStart().startsWith('<')) return `${UNSUPPORTED_MESSAGE}（HTTP ${res.status}）`;
  return text.slice(0, 200);
}

// ===== OAuth 管理 API（access_token 可调用 /oauth/*） =====

export interface GatewayUserinfo {
  id: string;
  username?: string;
  display_name?: string;
  email?: string;
  provider?: string;
  role?: number;
  status?: number;
  quota?: number;
  used_quota?: number;
  request_count?: number;
  group?: string;
  [key: string]: unknown;
}

export interface GatewayLogEntry {
  id?: number | string;
  user_id?: string | number;
  model?: string;
  request_type?: string;
  status?: number;
  status_code?: number;
  usage?: number;
  quota?: number;
  group_name?: string;
  request_id?: string;
  created_at?: string;
  [key: string]: unknown;
}

export interface GatewayLogsResult {
  data: GatewayLogEntry[];
  total: number;
  page: number;
  page_size: number;
}

export interface GatewayTopupResult {
  checkout_url?: string;
  payment_url?: string;
  order_id?: string;
  [key: string]: unknown;
}

export interface FetchGatewayOptions {
  /** 服务器端 Route Handler 可直接传入 access token，避免依赖 gatewayAuth */
  accessToken?: string;
}

/**
 * GET /oauth/userinfo — 读取当前用户网关账户信息。
 */
export async function fetchGatewayUserinfo(opts?: FetchGatewayOptions): Promise<GatewayUserinfo> {
  const res = await requestWithAuth('GET', '/oauth/userinfo', { accessToken: opts?.accessToken });
  if (!res.ok) throw new GatewayError(res.status, await readErrorText(res));
  const json = await readJson(res);
  if (!json || typeof json !== 'object') throw new GatewayError(502, UNSUPPORTED_MESSAGE, 'UNSUPPORTED');
  const raw = json as Record<string, unknown>;
  // new-api 的 userinfo 遵循 OIDC 命名（sub / name），前端统一收敛成 id / display_name。
  return {
    ...raw,
    id: typeof raw.id === 'string' ? raw.id : String(raw.sub ?? ''),
    display_name:
      typeof raw.display_name === 'string'
        ? raw.display_name
        : typeof raw.name === 'string'
          ? raw.name
          : undefined,
  };
}

/**
 * POST /oauth/topup/checkout — 创建充值订单，返回 checkout_url。
 */
export async function fetchGatewayTopup(
  input: { amount: number; channel?: string },
  opts?: FetchGatewayOptions,
): Promise<GatewayTopupResult> {
  const body = JSON.stringify({ amount: input.amount, ...(input.channel ? { channel: input.channel } : {}) });
  const res = await requestWithAuth('POST', '/oauth/topup/checkout', { body, accessToken: opts?.accessToken });
  if (!res.ok) throw new GatewayError(res.status, await readErrorText(res));
  const json = await readJson(res);
  if (!json || typeof json !== 'object') {
    throw new GatewayError(502, `${UNSUPPORTED_MESSAGE}（在线充值）`, 'UNSUPPORTED');
  }
  return json as GatewayTopupResult;
}

/**
 * GET /oauth/logs — 读取当前用户的消费日志。
 */
export async function fetchGatewayLogs(
  input: { page?: number; pageSize?: number },
  opts?: FetchGatewayOptions,
): Promise<GatewayLogsResult> {
  const page = Math.max(1, input.page ?? 1);
  const pageSize = Math.min(100, Math.max(1, input.pageSize ?? 20));
  const res = await requestWithAuth('GET', '/oauth/logs', {
    query: { page, page_size: pageSize },
    accessToken: opts?.accessToken,
  });
  if (!res.ok) throw new GatewayError(res.status, await readErrorText(res));
  const json = await readJson(res);

  // new-api 返回 { total, page, page_size, items }；早期版本可能是裸数组或 { data }。
  const toEntry = (item: unknown): GatewayLogEntry => {
    const raw = (item && typeof item === 'object' ? item : {}) as Record<string, unknown>;
    const createdAt = raw.created_at;
    return {
      ...raw,
      id: (raw.id ?? raw.request_id) as GatewayLogEntry['id'],
      // 网关字段名是 model_name，前端统一用 model
      model: (raw.model ?? raw.model_name) as string | undefined,
      created_at:
        typeof createdAt === 'number'
          ? new Date(createdAt * 1000).toISOString()
          : typeof createdAt === 'string'
            ? createdAt
            : undefined,
    };
  };

  if (Array.isArray(json)) {
    return { data: json.map(toEntry), total: json.length, page, page_size: pageSize };
  }
  if (json && typeof json === 'object') {
    const obj = json as Record<string, unknown>;
    const items = Array.isArray(obj.items) ? obj.items : Array.isArray(obj.data) ? obj.data : null;
    if (items) {
      return {
        data: items.map(toEntry),
        total: typeof obj.total === 'number' ? obj.total : items.length,
        page,
        page_size: pageSize,
      };
    }
  }
  throw new GatewayError(502, `${UNSUPPORTED_MESSAGE}（消费日志）`, 'UNSUPPORTED');
}

// ===== AI Relay（保留原有函数 + 重构复用 requestWithAuth） =====

async function postJsonWithAuth(
  path: string,
  body: string,
  signal?: AbortSignal,
): Promise<Response> {
  // requestWithAuth 不支持 AbortSignal；直接内联实现以保持流式生成
  const send = (token: string) =>
    fetch(`${config.gatewayBaseUrl}${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
      body,
      signal,
    });
  const res = await send(await getGatewayAccessToken());
  if (res.status !== 401) return res;
  await res.body?.cancel().catch(() => undefined);
  return send(await refreshGatewayToken());
}

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
  const body = JSON.stringify({ model, stream: true, messages });
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
      const events = buffer.split('\n\n');
      buffer = events.pop() ?? '';
      for (const event of events) {
        const line = event.split('\n').find((l) => l.startsWith('data:'));
        if (!line) continue;
        const data = line.slice(5).trim();
        if (data === '[DONE]') continue;
        let json: { choices?: { delta?: { content?: string } }[] };
        try { json = JSON.parse(data); } catch { continue; }
        const delta = json.choices?.[0]?.delta?.content;
        if (delta) { fullText += delta; callbacks.onDelta?.(delta); }
      }
    }
    callbacks.onDone?.(fullText);
  } catch (e) {
    callbacks.onError?.(normalizeError(e));
  }
}

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

// ===== 3. 图像生成（换装核心） =====
export interface ImageGenerateInput {
  model: string;
  prompt: string;
  reference_images?: string[];     // base64 or URL for reference
  character_image?: string;        // 人物图 base64/URL
  outfit_image?: string;           // 服装图 base64/URL
  pose_image?: string;             // 姿势图 base64/URL
  size?: string;                   // 1024x1024 / 1024x1536 / 1536x1024
  n?: number;
  negative_prompt?: string;
  /** 服务端 Route Handler 传 Supabase session 里的 provider_token；浏览器端可省略 */
  accessToken?: string;
}

export interface GeneratedImage {
  url: string;
  b64_json?: string;
  revised_prompt?: string;
}

export interface ImageGenerateResult {
  images: GeneratedImage[];
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number };
  request_id?: string;
}

export async function generateImage(input: ImageGenerateInput): Promise<ImageGenerateResult> {
  const payload: Record<string, unknown> = {
    model: input.model,
    prompt: input.prompt,
    stream: false,
    n: input.n ?? 1,
  };
  if (input.size) payload.size = input.size;
  if (input.negative_prompt) payload.negative_prompt = input.negative_prompt;

  // reference_images 或 character_image + outfit_image + pose_image 通过 extra_body 传给支持的 provider
  const extra: Record<string, unknown> = {};
  if (input.reference_images?.length) extra.reference_images = input.reference_images;
  if (input.character_image) extra.character_image = input.character_image;
  if (input.outfit_image) extra.outfit_image = input.outfit_image;
  if (input.pose_image) extra.pose_image = input.pose_image;
  if (Object.keys(extra).length) payload.extra_body = extra;

  const res = await requestWithAuth('POST', '/v1/images/generations', {
    body: JSON.stringify(payload),
    accessToken: input.accessToken,
  });
  if (!res.ok) {
    const text = await res.text();
    const message = text || (res.status === 401 ? '网关登录已失效，请重新登录' : res.statusText);
    throw new GatewayError(res.status, message);
  }
  const json = (await res.json()) as unknown as {
    data: GeneratedImage[];
    usage?: ImageGenerateResult['usage'];
    request_id?: string;
  };
  return { images: json.data ?? [], usage: json.usage, request_id: json.request_id };
}

// ===== 4. new-api dashboard /api/user/self（需要 PAT 或 session，OAuth token 不生效） =====
// 注意：OAuth access token 不能调 /api/*；这里保留接口，实际调用用 PAT 或让用户跳转仪表板。
