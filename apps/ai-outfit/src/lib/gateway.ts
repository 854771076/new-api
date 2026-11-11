import 'server-only';
import { z } from 'zod';
import { config } from '@/lib/config';
import { getGatewayAccessToken } from '@/lib/gateway-session';
import { readGeneratedImage } from '@/lib/generated-image';
import { GatewayError, readGatewayError } from '@/lib/gateway-errors';
import { gatewayCheckoutSchema, gatewayCheckoutResultSchema, gatewayTopupSchema,
  gatewayPlanSchema, gatewayProductSchema,
  type GatewayCheckoutInput, type GatewayCheckoutResult, type GatewayPaymentMethod,
  type GatewayPlan, type GatewayProduct } from '@/lib/gateway-contract';

export { GatewayError } from '@/lib/gateway-errors';
export type { GatewayCheckoutResult, GatewayPaymentMethod, GatewayPlan, GatewayProduct } from '@/lib/gateway-contract';

function normalizeError(err: unknown): GatewayError {
  if (err instanceof GatewayError) return err;
  return new GatewayError(503, 'Gateway service is temporarily unavailable.', 'UPSTREAM_GATEWAY_ERROR');
}

async function requestWithAuth(
  method: string,
  path: string,
  opts: { body?: string | FormData; query?: Record<string, string | number | undefined>; accessToken?: string; signal?: AbortSignal } = {},
): Promise<Response> {
  const url = new URL(`${config.gatewayBaseUrl}${path}`);
  for (const [key, value] of Object.entries(opts.query ?? {})) {
    if (value !== undefined) url.searchParams.set(key, String(value));
  }
  // Supplied server tokens are hints only. The verified session's vault is always
  // authoritative, never a stale provider_token left in cookies.
  const firstToken = await getGatewayAccessToken({ expectedToken: opts.accessToken });
  // FormData retains immutable file blobs: fetch serializes them anew on the
  // single 401 retry. Let fetch supply the multipart boundary on each send.
  const send = (token: string) => fetch(url, {
    method,
    headers: { Authorization: `Bearer ${token}`, ...(typeof opts.body === 'string' ? { 'Content-Type': 'application/json' } : {}) },
    body: opts.body, cache: 'no-store', redirect: 'error',
    signal: opts.signal
      ? AbortSignal.any([opts.signal, AbortSignal.timeout(path.startsWith('/v1/') ? 120_000 : 15_000)])
      : AbortSignal.timeout(path.startsWith('/v1/') ? 120_000 : 15_000),
  });
  try {
    const response = await send(firstToken);
    if (response.status !== 401) return response;
    await response.body?.cancel().catch(() => undefined);
    const refreshed = await getGatewayAccessToken({ force: true, expectedToken: firstToken });
    return await send(refreshed); // Exactly once, including explicit-token callers.
  } catch (error) {
    if (error instanceof GatewayError) throw error;
    throw new GatewayError(503, 'Gateway service is temporarily unavailable.', 'UPSTREAM_GATEWAY_TIMEOUT');
  }
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
  app_quota?: number;
  app_used_quota?: number;
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
  app_id?: number;
  [key: string]: unknown;
}

export interface GatewayLogsResult {
  data: GatewayLogEntry[];
  total: number;
  page: number;
  page_size: number;
}

export interface FetchGatewayOptions {
  /** Server-only hint; the current vault is authoritative. */
  accessToken?: string;
}

/**
 * GET /oauth/userinfo — 读取当前用户网关账户信息。
 */
export async function fetchGatewayUserinfo(opts?: FetchGatewayOptions): Promise<GatewayUserinfo> {
  const res = await requestWithAuth('GET', '/oauth/userinfo', { accessToken: opts?.accessToken });
  if (!res.ok) throw await readGatewayError(res);
  const json = await readJson(res);
  if (!json || typeof json !== 'object') throw new GatewayError(502, UNSUPPORTED_MESSAGE, 'UNSUPPORTED');
  const raw = json as Record<string, unknown>;
  // new-api 的 userinfo 遵循 OIDC 命名（sub / name），前端统一收敛成 id / display_name。
  return {
    id: typeof raw.id === 'string' || typeof raw.id === 'number'
      ? String(raw.id)
      : typeof raw.sub === 'string' ? raw.sub : '',
    username: typeof raw.username === 'string' ? raw.username : undefined,
    email: typeof raw.email === 'string' ? raw.email : undefined,
    quota: typeof raw.quota === 'number' ? raw.quota : undefined,
    used_quota: typeof raw.used_quota === 'number' ? raw.used_quota : undefined,
    app_quota: typeof raw.app_quota === 'number' ? raw.app_quota : undefined,
    app_used_quota: typeof raw.app_used_quota === 'number' ? raw.app_used_quota : undefined,
    request_count: typeof raw.request_count === 'number' ? raw.request_count : undefined,
    group: typeof raw.group === 'string' ? raw.group : undefined,
    display_name:
      typeof raw.display_name === 'string'
        ? raw.display_name
        : typeof raw.name === 'string'
          ? raw.name
          : undefined,
  };
}

async function checkout(path: string, body: Record<string, unknown>, opts?: FetchGatewayOptions): Promise<GatewayCheckoutResult> {
  const res = await requestWithAuth('POST', path, { body: JSON.stringify(body), accessToken: opts?.accessToken });
  if (!res.ok) throw await readGatewayError(res);
  const parsed = gatewayCheckoutResultSchema.safeParse(await readJson(res));
  if (!parsed.success || (parsed.data.payment_method && parsed.data.payment_method !== body.payment_method) ||
      (body.payment_method === 'balance' && parsed.data.status !== 'success') ||
      (body.payment_method === 'waffo_pancake' && parsed.data.status !== 'pending')) {
    throw new GatewayError(502, 'Invalid gateway checkout response.', 'UPSTREAM_INVALID_RESPONSE');
  }
  return { ...parsed.data, payment_method: body.payment_method as GatewayPaymentMethod };
}

export async function fetchGatewayTopup(
  input: { amount: number; payment_method: GatewayPaymentMethod }, opts?: FetchGatewayOptions,
): Promise<GatewayCheckoutResult> {
  const parsed = gatewayTopupSchema.safeParse(input);
  if (!parsed.success) throw new GatewayError(400, 'Invalid top-up request.', 'BAD_REQUEST_INVALID_BODY');
  return checkout('/oauth/topup/checkout', parsed.data, opts);
}

export async function fetchGatewayCatalog(opts?: FetchGatewayOptions): Promise<{ plans: GatewayPlan[]; products: GatewayProduct[] }> {
  const [planRes, productRes] = await Promise.all([
    requestWithAuth('GET', '/oauth/subscription/plans', opts),
    requestWithAuth('GET', '/oauth/products', opts),
  ]);
  if (!planRes.ok) throw await readGatewayError(planRes);
  if (!productRes.ok) throw await readGatewayError(productRes);
  const plans = z.object({ plans: z.array(z.object({ plan: gatewayPlanSchema })) }).safeParse(await readJson(planRes));
  const products = z.object({ products: z.array(gatewayProductSchema) }).safeParse(await readJson(productRes));
  if (!plans.success || !products.success) throw new GatewayError(502, 'Invalid gateway catalog response.', 'UPSTREAM_INVALID_RESPONSE');
  return { plans: plans.data.plans.map(value => value.plan), products: products.data.products };
}

export async function fetchGatewayCheckout(input: GatewayCheckoutInput, opts?: FetchGatewayOptions): Promise<GatewayCheckoutResult> {
  const parsed = gatewayCheckoutSchema.safeParse(input);
  if (!parsed.success) throw new GatewayError(400, 'Invalid checkout request.', 'BAD_REQUEST_INVALID_BODY');
  const path = parsed.data.kind === 'plan' ? '/oauth/subscription/checkout' : '/oauth/products/checkout';
  const idField = parsed.data.kind === 'plan' ? 'plan_id' : 'product_id';
  return checkout(path, { [idField]: parsed.data.id, payment_method: parsed.data.payment_method }, opts);
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
  if (!res.ok) throw await readGatewayError(res);
  const json = await readJson(res);

  // new-api 返回 { total, page, page_size, items }；早期版本可能是裸数组或 { data }。
  const toEntry = (item: unknown): GatewayLogEntry => {
    const raw = (item && typeof item === 'object' ? item : {}) as Record<string, unknown>;
    const createdAt = raw.created_at;
    const id = raw.id ?? raw.request_id;
    const model = raw.model ?? raw.model_name;
    return {
      id: typeof id === 'number' || typeof id === 'string' ? id : undefined,
      quota: typeof raw.quota === 'number' ? raw.quota : undefined,
      prompt_tokens: typeof raw.prompt_tokens === 'number' ? raw.prompt_tokens : undefined,
      completion_tokens: typeof raw.completion_tokens === 'number' ? raw.completion_tokens : undefined,
      use_time: typeof raw.use_time === 'number' ? raw.use_time : undefined,
      is_stream: typeof raw.is_stream === 'boolean' ? raw.is_stream : undefined,
      request_type: typeof raw.type === 'number' ? String(raw.type) : undefined,
      group_name: typeof raw.group === 'string' ? raw.group : undefined,
      request_id: typeof raw.request_id === 'string' ? raw.request_id : undefined,
      app_id: typeof raw.app_id === 'number' ? raw.app_id : undefined,
      // 网关字段名是 model_name，前端统一用 model
      model: typeof model === 'string' ? model : undefined,
      created_at:
        typeof createdAt === 'number' && Number.isFinite(createdAt) && Math.abs(createdAt) <= 8_640_000_000_000
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

// ===== OAuth 定价接口（GET /oauth/pricing） =====

export interface GatewayPricingEntry {
  model_name: string;
  /** 倍率（每 1M token），倍率计费模式使用。 */
  model_ratio: number;
  /** 单次价格（单位 = quota_per_unit），按次价格模式使用。 */
  model_price: number;
  /** 0 = 倍率计费，1 = 按次价格计费。 */
  quota_type: number;
  completion_ratio: number;
  image_ratio?: number | null;
  enable_groups?: string[];
  [key: string]: unknown;
}

export interface GatewayPricing {
  data: GatewayPricingEntry[];
  group_ratio: Record<string, number>;
  usable_group: Record<string, string>;
  quota_per_unit: number;
}

const gatewayPricingEntrySchema = z.object({
  model_name: z.string().min(1),
  model_ratio: z.number().nonnegative().optional().default(0),
  model_price: z.number().nonnegative().optional().default(0),
  quota_type: z.number().int().min(0).max(1).optional().default(0),
  completion_ratio: z.number().nonnegative().optional().default(0),
  image_ratio: z.number().nonnegative().nullable().optional(),
  enable_groups: z.array(z.string()).optional(),
}).passthrough();

/**
 * GET /oauth/pricing — 读取网关模型定价（倍率/单价 + 分组倍率 + 每单位额度）。
 * 供 ai-outfit 提交前报价使用；实际扣费仍由网关 relay 按真实用量结算。
 */
export async function fetchGatewayPricing(opts?: FetchGatewayOptions): Promise<GatewayPricing> {
  const res = await requestWithAuth('GET', '/oauth/pricing', { accessToken: opts?.accessToken });
  if (!res.ok) throw await readGatewayError(res);
  const parsed = z.object({
    data: z.array(gatewayPricingEntrySchema),
    group_ratio: z.record(z.string(), z.number()).optional().default({}),
    usable_group: z.record(z.string(), z.string()).optional().default({}),
    quota_per_unit: z.number().positive(),
  }).safeParse(await readJson(res));
  if (!parsed.success) throw new GatewayError(502, 'Invalid gateway pricing response.', 'UPSTREAM_INVALID_RESPONSE');
  return {
    data: parsed.data.data,
    group_ratio: parsed.data.group_ratio,
    usable_group: parsed.data.usable_group,
    quota_per_unit: parsed.data.quota_per_unit,
  };
}

// ===== AI Relay（保留原有函数 + 重构复用 requestWithAuth） =====

export interface GatewayModel {
  id: string;
  owned_by?: string;
}

/**
 * GET /v1/models — 网关当前可用的模型目录（OAuth api:read 即可访问）。
 */
export async function listGatewayModels(opts?: FetchGatewayOptions): Promise<GatewayModel[]> {
  const res = await requestWithAuth('GET', '/v1/models', { accessToken: opts?.accessToken });
  if (!res.ok) throw await readGatewayError(res);
  const parsed = z.object({
    data: z.array(z.object({
      id: z.string().trim().min(1).max(200),
      owned_by: z.string().max(200).optional(),
    }).passthrough()),
  }).safeParse(await readJson(res));
  if (!parsed.success) throw new GatewayError(502, 'Invalid gateway models response.', 'UPSTREAM_INVALID_RESPONSE');
  return parsed.data.data.map((model) => ({ id: model.id, owned_by: model.owned_by }));
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
    res = await requestWithAuth('POST', '/v1/chat/completions', { body, signal });
  } catch (e) {
    callbacks.onError?.(normalizeError(e));
    return;
  }
  if (!res.ok || !res.body) {
    callbacks.onError?.(await readGatewayError(res));
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
  const res = await requestWithAuth('POST', '/v1/chat/completions', {
    body: JSON.stringify({ model, stream: false, messages }),
  });
  if (!res.ok) throw await readGatewayError(res);
  const json = (await res.json()) as { choices: { message: { content: string } }[] };
  return json.choices[0]?.message?.content ?? '';
}

export type VisionContentPart =
  | { type: 'text'; text: string }
  | { type: 'image_url'; image_url: { url: string } };

export interface VisionMessage {
  role: 'system' | 'user' | 'assistant';
  content: string | VisionContentPart[];
}

/**
 * 多模态视觉对话（非流式）。图片以 HTTPS signed URL 传入，遵循 OpenAI vision 消息结构，
 * 由网关 /v1/chat/completions 透传给支持视觉的模型。
 */
export async function visionChatCompletion(model: string, messages: VisionMessage[]): Promise<string> {
  const res = await requestWithAuth('POST', '/v1/chat/completions', {
    body: JSON.stringify({ model, stream: false, messages }),
  });
  if (!res.ok) throw await readGatewayError(res);
  const json = (await res.json()) as { choices: { message: { content: string } }[] };
  return json.choices[0]?.message?.content ?? '';
}

// ===== 3. 图像生成（换装核心） =====
export interface ImageGenerateInput {
  model: string;
  prompt: string;
  /** HTTPS signed URLs ordered as character, 1–6 clothing references, pose. */
  reference_images: string[];
  size?: string;
  n?: number;
  negative_prompt?: string;
  /** Optional server-only hint; current encrypted vault is authoritative. */
  accessToken?: string;
}

export interface GeneratedImage {
  url?: string;
  b64_json?: string;
  revised_prompt?: string;
}

// Keep the complete normalized upload bounded, rather than retaining eight
// 20 MiB PNGs. At most two downloads/decoders run concurrently below.
const MAX_REFERENCE_UPLOAD_BYTES = 40 * 1024 * 1024;
const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
const MAX_IMAGE_PROMPT_LENGTH = 32_000;
const imageUrlSchema = z.string().max(8192).url().refine(value => {
  // Refinements also run after a failed URL-format check; never expose a raw
  // URL constructor exception (which can include a signed reference URL).
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password && (!url.port || url.port === '443');
  } catch {
    return false;
  }
});
const imageGenerateInputSchema = z.object({
  model: z.string().trim().min(1).max(200),
  prompt: z.string().trim().min(1).max(MAX_IMAGE_PROMPT_LENGTH),
  reference_images: z.array(imageUrlSchema).min(3).max(8),
  size: z.enum(['auto', '1024x1024', '1024x1536', '1536x1024']).default('auto'),
  n: z.literal(1).default(1),
  negative_prompt: z.string().trim().max(4000).optional(),
  accessToken: z.string().optional(),
}).strict();
const imageTokenCountSchema = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
const imageUsageSchema = z.object({
  prompt_tokens: imageTokenCountSchema.optional(),
  completion_tokens: imageTokenCountSchema.optional(),
  input_tokens: imageTokenCountSchema.optional(),
  output_tokens: imageTokenCountSchema.optional(),
  total_tokens: imageTokenCountSchema.optional(),
  input_tokens_details: z.object({
    text_tokens: imageTokenCountSchema.optional(),
    image_tokens: imageTokenCountSchema.optional(),
  }).optional(),
}).refine(value => [value.prompt_tokens, value.completion_tokens, value.input_tokens, value.output_tokens, value.total_tokens]
  .some(count => count !== undefined));
const imageGenerateResponseSchema = z.object({
  data: z.array(z.object({
    url: imageUrlSchema.optional(),
    b64_json: z.string().min(4).max(Math.ceil(MAX_IMAGE_BYTES / 3) * 4)
      // Avoid repeated capture/group quantifiers: V8 can overflow its regex
      // stack on ordinary multi-megabyte GPT image payloads.
      .regex(/^[A-Za-z0-9+/]+={0,2}$/).refine(value => value.length % 4 === 0).optional(),
    revised_prompt: z.string().max(MAX_IMAGE_PROMPT_LENGTH).optional(),
  }).refine(value => Boolean(value.url || value.b64_json))).min(1),
  usage: imageUsageSchema.optional(),
  request_id: z.string().min(1).max(200).regex(/^[A-Za-z0-9._:-]+$/).optional(),
});

export interface ImageGenerateResult {
  images: GeneratedImage[];
  usage?: z.infer<typeof imageUsageSchema>;
  request_id?: string;
}

export async function generateImage(input: ImageGenerateInput): Promise<ImageGenerateResult> {
  const parsedInput = imageGenerateInputSchema.safeParse(input);
  if (!parsedInput.success) throw new GatewayError(400, 'Invalid image edit request.', 'BAD_REQUEST_INVALID_BODY');
  const values = parsedInput.data;
  const roles = "Reference image roles (in upload order): Use the first image for the person's identity, face, and appearance. " +
    'Use all middle images for clothing details. Use the last image only for the body pose, not identity or clothing.';
  const prompt = `${roles}\n\n${values.prompt}` + (values.negative_prompt
    ? `\n\nNegative constraints: Do not include any of the following: ${values.negative_prompt}` : '');
  if (prompt.length > MAX_IMAGE_PROMPT_LENGTH) {
    throw new GatewayError(400, 'Image edit prompt is too long.', 'BAD_REQUEST_INVALID_BODY');
  }

  const form = new FormData();
  form.set('model', values.model);
  form.set('prompt', prompt);
  form.set('size', values.size);
  form.set('n', String(values.n));
  let uploadBytes = 0;
  for (let index = 0; index < values.reference_images.length; index += 2) {
    // Wait for both decoders even on failure; no orphaned heavy work or next
    // batch is started after a rejected download. Promise ordering is stable.
    const batch = await Promise.allSettled(values.reference_images.slice(index, index + 2)
      .map(url => readGeneratedImage({ url })));
    for (const [offset, result] of batch.entries()) {
      if (result.status === 'rejected') {
        throw new GatewayError(502, 'Reference images could not be prepared.', 'UPSTREAM_REFERENCE_IMAGE_ERROR');
      }
      const png = result.value;
      uploadBytes += png.byteLength;
      if (!png.byteLength || png.byteLength > MAX_IMAGE_BYTES || uploadBytes > MAX_REFERENCE_UPLOAD_BYTES) {
        throw new GatewayError(413, 'Reference images exceed the upload limit.', 'REFERENCE_IMAGES_TOO_LARGE');
      }
      form.append('image[]', new Blob([new Uint8Array(png)], { type: 'image/png' }), `reference-${index + offset + 1}.png`);
    }
  }

  const res = await requestWithAuth('POST', '/v1/images/edits', { body: form, accessToken: values.accessToken });
  if (!res.ok) throw await readGatewayError(res);
  const parsed = imageGenerateResponseSchema.safeParse(await readJson(res));
  if (!parsed.success) throw new GatewayError(502, 'Invalid gateway image response.', 'UPSTREAM_INVALID_RESPONSE');
  return { images: parsed.data.data, usage: parsed.data.usage, request_id: parsed.data.request_id };
}

export interface ReferenceImageInput {
  model: string;
  /** Frozen pipeline prompt (v1 product / v2 persona / v4 try-on); sent verbatim, no role prefix. */
  prompt: string;
  /** One or two HTTPS signed reference URLs in the exact order the prompt describes. */
  reference_images: string[];
  size?: string;
  accessToken?: string;
}

/**
 * 六阶段管线专用：把冻结英文提示词原样发送，参考图固定 1–2 张（人物标准化 1 张、
 * 商品图 2 张、最终换装 2 张），不注入旧版多图角色前缀，避免覆盖 v1/v2/v4 约束。
 */
export async function generateReferenceImage(input: ReferenceImageInput): Promise<ImageGenerateResult> {
  const parsedInput = z.object({
    model: z.string().trim().min(1).max(200),
    prompt: z.string().trim().min(1).max(MAX_IMAGE_PROMPT_LENGTH),
    reference_images: z.array(imageUrlSchema).min(1).max(2),
    size: z.enum(['auto', '1024x1024', '1024x1536', '1536x1024']).default('auto'),
    accessToken: z.string().optional(),
  }).strict().safeParse(input);
  if (!parsedInput.success) throw new GatewayError(400, 'Invalid image edit request.', 'BAD_REQUEST_INVALID_BODY');
  const values = parsedInput.data;

  const form = new FormData();
  form.set('model', values.model);
  form.set('prompt', values.prompt);
  form.set('size', values.size);
  form.set('n', '1');
  let uploadBytes = 0;
  for (const url of values.reference_images) {
    const png = await readGeneratedImage({ url }).catch(() => {
      throw new GatewayError(502, 'Reference images could not be prepared.', 'UPSTREAM_REFERENCE_IMAGE_ERROR');
    });
    uploadBytes += png.byteLength;
    if (!png.byteLength || png.byteLength > MAX_IMAGE_BYTES || uploadBytes > MAX_REFERENCE_UPLOAD_BYTES) {
      throw new GatewayError(413, 'Reference images exceed the upload limit.', 'REFERENCE_IMAGES_TOO_LARGE');
    }
    form.append('image[]', new Blob([new Uint8Array(png)], { type: 'image/png' }), 'reference.png');
  }

  const res = await requestWithAuth('POST', '/v1/images/edits', { body: form, accessToken: values.accessToken });
  if (!res.ok) throw await readGatewayError(res);
  const parsed = imageGenerateResponseSchema.safeParse(await readJson(res));
  if (!parsed.success) throw new GatewayError(502, 'Invalid gateway image response.', 'UPSTREAM_INVALID_RESPONSE');
  return { images: parsed.data.data, usage: parsed.data.usage, request_id: parsed.data.request_id };
}

// ===== 4. new-api dashboard /api/user/self（需要 PAT 或 session，OAuth token 不生效） =====
// 注意：OAuth access token 不能调 /api/*；这里保留接口，实际调用用 PAT 或让用户跳转仪表板。
