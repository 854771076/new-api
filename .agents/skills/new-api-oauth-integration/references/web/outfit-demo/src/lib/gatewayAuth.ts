// src/lib/gatewayAuth.ts
// 网关 OAuth access_token 的本地持有与自动刷新。
//
// Supabase 只维护自己的 session，不会刷新 custom provider 的 token；网关
// access_token 有效期 1 小时，refresh token 每次使用都会轮换。new-api 支持
// public client（无 client_secret），因此浏览器可以直接用 client_id +
// refresh_token 调 /oauth/token 换取新令牌，不需要后端参与。
import type { Session } from '@supabase/supabase-js';
import { config } from './config';
import { getSupabase } from './supabase';

const REFRESH_SKEW_MS = 60_000;
const STORAGE_PREFIX = 'gateway_tokens:';

export type GatewayAuthErrorCode =
  | 'missing_session'
  | 'missing_refresh_token'
  | 'invalid_grant'
  | 'invalid_response'
  | 'refresh_failed'
  | 'signed_out';

export class GatewayAuthError extends Error {
  code: GatewayAuthErrorCode;

  constructor(code: GatewayAuthErrorCode, message: string) {
    super(message);
    this.name = 'GatewayAuthError';
    this.code = code;
  }
}

interface GatewayTokens {
  accessToken: string;
  refreshToken: string | null;
  /** 网关 access_token 的过期时间（epoch ms）；无法解析 JWT exp 时为 null。 */
  expiresAt: number | null;
}

interface GatewayTokenState {
  userId: string;
  tokens: GatewayTokens;
}

let state: GatewayTokenState | null = null;
let inflightRefresh: Promise<string> | null = null;
/** 清除/切换用户时递增，避免迟到的刷新结果覆盖新登录态。 */
let stateEpoch = 0;

function storageKey(userId: string): string {
  return `${STORAGE_PREFIX}${userId}`;
}

/** 只做 base64url 解码读取 exp，不校验签名。 */
function decodeJwtExpiry(token: string): number | null {
  const parts = token.split('.');
  if (parts.length < 2) return null;

  const base64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
  const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4);

  try {
    const binary = atob(padded);
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
    const payload = JSON.parse(new TextDecoder().decode(bytes)) as { exp?: unknown };
    return typeof payload.exp === 'number' && Number.isFinite(payload.exp)
      ? payload.exp * 1000
      : null;
  } catch {
    return null;
  }
}

function tokensFromSession(session: Session): GatewayTokens | null {
  const accessToken = session.provider_token;
  if (!accessToken) return null;
  return {
    accessToken,
    refreshToken: session.provider_refresh_token ?? null,
    expiresAt: decodeJwtExpiry(accessToken),
  };
}

function persist(userId: string, tokens: GatewayTokens): void {
  try {
    sessionStorage.setItem(storageKey(userId), JSON.stringify(tokens));
  } catch {
    // sessionStorage 不可用时只保留内存态。
  }
}

function loadStored(userId: string): GatewayTokens | null {
  try {
    const raw = sessionStorage.getItem(storageKey(userId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<GatewayTokens>;
    if (typeof parsed.accessToken !== 'string' || !parsed.accessToken) return null;
    return {
      accessToken: parsed.accessToken,
      refreshToken: typeof parsed.refreshToken === 'string' ? parsed.refreshToken : null,
      expiresAt: typeof parsed.expiresAt === 'number' ? parsed.expiresAt : null,
    };
  } catch {
    return null;
  }
}

function clearGatewayTokens(): void {
  if (state) {
    try {
      sessionStorage.removeItem(storageKey(state.userId));
    } catch {
      // ignore
    }
  }
  state = null;
  stateEpoch += 1;
}

/**
 * 从 Supabase Session 重新播种网关令牌。
 * 登录回调、SIGNED_IN、TOKEN_REFRESHED 时调用。
 */
export function seedGatewayTokensFromSession(session: Session | null): void {
  if (!session?.user) {
    clearGatewayTokens();
    return;
  }

  const userId = session.user.id;
  if (state && state.userId !== userId) clearGatewayTokens();

  const tokens = tokensFromSession(session);
  if (!tokens) {
    // Supabase 刷新自己的 session 时不会带上新的 provider token；
    // 同一用户的既有网关令牌继续有效，不能清空。
    return;
  }

  // Supabase 不轮换 provider token，本地刷新过的令牌可能比 session 里的更新，
  // 这种情况下保留本地令牌，避免覆盖成已过期/已轮换的旧值。
  const existing = state?.userId === userId ? state : null;
  if (
    existing &&
    existing.tokens.expiresAt !== null &&
    tokens.expiresAt !== null &&
    existing.tokens.expiresAt > tokens.expiresAt
  ) {
    return;
  }

  state = {
    userId,
    tokens: {
      ...tokens,
      refreshToken: tokens.refreshToken ?? existing?.tokens.refreshToken ?? null,
    },
  };
  persist(userId, state.tokens);
}

async function loadTokens(): Promise<GatewayTokenState | null> {
  if (state) return state;

  const { data } = await getSupabase().auth.getSession();
  const session = data.session;
  if (!session?.user) return null;

  const userId = session.user.id;
  const stored = loadStored(userId);
  if (stored) {
    state = { userId, tokens: stored };
    return state;
  }

  const fromSession = tokensFromSession(session);
  if (!fromSession) return null;
  state = { userId, tokens: fromSession };
  persist(userId, fromSession);
  return state;
}

async function readJson(res: Response): Promise<Record<string, unknown> | null> {
  try {
    const data = (await res.json()) as unknown;
    return data !== null && typeof data === 'object' ? (data as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

async function performRefresh(epoch: number): Promise<string> {
  const current = await loadTokens();
  if (!current) {
    throw new GatewayAuthError('missing_session', '网关登录态缺失，请重新登录');
  }
  if (!current.tokens.refreshToken) {
    throw new GatewayAuthError('missing_refresh_token', '网关 refresh token 缺失，请重新登录');
  }

  const body = new URLSearchParams({
    grant_type: 'refresh_token',
    client_id: config.gatewayClientId,
    refresh_token: current.tokens.refreshToken,
  });

  let res: Response;
  try {
    res = await fetch(`${config.gatewayBaseUrl}/oauth/token`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      // 公共客户端：只带 client_id，不带 client_secret。
      body: body.toString(),
    });
  } catch (e) {
    throw new GatewayAuthError(
      'refresh_failed',
      `刷新网关令牌失败：${e instanceof Error ? e.message : String(e)}`,
    );
  }

  const payload = await readJson(res);
  if (!res.ok) {
    const errorCode =
      typeof payload?.error === 'string'
        ? payload.error
        : typeof payload?.code === 'string'
          ? payload.code
          : '';

    if (errorCode === 'invalid_grant') {
      clearGatewayTokens();
      try {
        await getSupabase().auth.signOut();
      } catch {
        // 本地令牌已清除，登出请求失败不改变结论。
      }
      throw new GatewayAuthError('invalid_grant', '网关登录已失效，请重新登录');
    }

    const description =
      typeof payload?.error_description === 'string' ? payload.error_description : res.statusText;
    throw new GatewayAuthError('refresh_failed', `刷新网关令牌失败：${description}`);
  }

  const accessToken = payload?.access_token;
  if (typeof accessToken !== 'string' || !accessToken) {
    throw new GatewayAuthError('invalid_response', '网关令牌响应缺少 access_token');
  }

  const refreshToken =
    typeof payload?.refresh_token === 'string' && payload.refresh_token
      ? payload.refresh_token
      : current.tokens.refreshToken;
  const expiresIn =
    typeof payload?.expires_in === 'number' && Number.isFinite(payload.expires_in)
      ? payload.expires_in
      : null;

  if (epoch !== stateEpoch) {
    throw new GatewayAuthError('signed_out', '登录状态已变化，请重新登录');
  }

  const next: GatewayTokens = {
    accessToken,
    refreshToken,
    expiresAt:
      expiresIn !== null ? Date.now() + expiresIn * 1000 : decodeJwtExpiry(accessToken),
  };
  state = { userId: current.userId, tokens: next };
  persist(current.userId, next);
  return accessToken;
}

/** 强制用 refresh_token 换新令牌；并发调用共享同一个 in-flight Promise。 */
export function refreshGatewayToken(): Promise<string> {
  if (inflightRefresh) return inflightRefresh;

  const epoch = stateEpoch;
  inflightRefresh = performRefresh(epoch).finally(() => {
    inflightRefresh = null;
  });
  return inflightRefresh;
}

/** 获取可用的网关 access_token；剩余有效期不足 60 秒时先刷新。 */
export async function getGatewayAccessToken(): Promise<string> {
  const current = await loadTokens();
  if (!current) {
    throw new GatewayAuthError('missing_session', '网关登录态缺失，请重新登录');
  }

  const { tokens } = current;
  if (tokens.expiresAt !== null && tokens.expiresAt - Date.now() < REFRESH_SKEW_MS) {
    return refreshGatewayToken();
  }
  return tokens.accessToken;
}

/**
 * 读取当前持有的网关 refresh token。
 * 本地刷新会轮换 refresh token，因此 Supabase session 里的
 * provider_refresh_token 可能已经失效，登出吊销必须使用这里的值。
 */
export async function getGatewayRefreshToken(): Promise<string | null> {
  const current = await loadTokens();
  return current?.tokens.refreshToken ?? null;
}

getSupabase().auth.onAuthStateChange((event, session) => {
  if (event === 'SIGNED_OUT') {
    clearGatewayTokens();
    return;
  }
  if (event === 'SIGNED_IN' || event === 'TOKEN_REFRESHED') {
    seedGatewayTokensFromSession(session);
  }
});
