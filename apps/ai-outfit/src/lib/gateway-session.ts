import 'server-only';
import { createCipheriv, createDecipheriv, randomBytes, randomUUID } from 'node:crypto';
import { setTimeout as wait } from 'node:timers/promises';
import type { Session, User } from '@supabase/supabase-js';
import { z } from 'zod';
import { config } from '@/lib/config';
import { createServerSupabase } from '@/lib/supabase/server';
import { createAdminSupabase } from '@/lib/supabase/admin';
import { GatewayError } from '@/lib/gateway-errors';

const sessionClaimsSchema = z.object({ sub: z.string().uuid(), session_id: z.string().uuid() });
const gatewayClaimsSchema = z.object({
  sub: z.string().min(1), client_id: z.string().min(1), exp: z.number().int().positive(),
});
const pairSchema = z.object({
  accessToken: z.string().min(1), refreshToken: z.string().min(1), expiresAt: z.number().finite(),
});
const vaultSchema = z.object({
  session_id: z.string().uuid(), user_id: z.string().uuid(), client_id: z.string(),
  encrypted_pair: z.string(), version: z.number().int().nonnegative(),
  lease_owner: z.string().uuid().nullable(), lease_until: z.string().nullable(),
});
type Pair = z.infer<typeof pairSchema>;
type Vault = z.infer<typeof vaultSchema>;
interface SessionKey { session_id: string; user_id: string; client_id: string; gatewaySubject: string }

function unavailable(): GatewayError {
  return new GatewayError(503, 'Gateway session service is temporarily unavailable. Please try again.', 'UPSTREAM_SESSION_UNAVAILABLE');
}
function expired(): GatewayError {
  return new GatewayError(401, 'Gateway session expired. Please sign in again.', 'AUTH_SESSION_EXPIRED');
}
function jwtPayload(token: string): unknown {
  try { return JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString('utf8')); }
  catch { throw expired(); }
}
function sessionKey(session: Session, user: User): SessionKey {
  const claims = sessionClaimsSchema.safeParse(jwtPayload(session.access_token));
  const identities = user.identities?.filter(value => value.provider === 'custom:newapi') ?? [];
  if (identities.length !== 1) throw expired();
  const identity = identities[0];
  const audience: unknown = identity.identity_data?.aud;
  const clientId = Array.isArray(audience) ? audience[0] : undefined;
  const subject: unknown = identity?.identity_data?.sub;
  if (!claims.success || claims.data.sub !== user.id || session.user.id !== user.id ||
      !config.gatewayClientId || clientId !== config.gatewayClientId || typeof subject !== 'string' || !subject) {
    throw expired();
  }
  return { session_id: claims.data.session_id, user_id: user.id, client_id: config.gatewayClientId, gatewaySubject: subject };
}
function gatewayPair(accessToken: string, refreshToken: string, key: SessionKey): Pair {
  const claims = gatewayClaimsSchema.safeParse(jwtPayload(accessToken));
  if (!claims.success || claims.data.client_id !== key.client_id || claims.data.sub !== key.gatewaySubject) throw expired();
  return { accessToken, refreshToken, expiresAt: claims.data.exp * 1000 };
}
function encryptionKey(): Buffer {
  const encoded = process.env.GATEWAY_TOKEN_ENCRYPTION_KEY ?? '';
  const key = Buffer.from(encoded, 'base64');
  // Require canonical base64 and an independent 256-bit key; no public-key fallback.
  if (key.length !== 32 || key.toString('base64') !== encoded) throw unavailable();
  return key;
}
function associatedData(key: SessionKey): Buffer {
  return Buffer.from(JSON.stringify([key.session_id, key.user_id, key.client_id]));
}
function encrypt(pair: Pair, key: SessionKey): string {
  const nonce = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', encryptionKey(), nonce);
  cipher.setAAD(associatedData(key));
  const ciphertext = Buffer.concat([cipher.update(JSON.stringify(pair), 'utf8'), cipher.final()]);
  return Buffer.concat([nonce, cipher.getAuthTag(), ciphertext]).toString('base64');
}
function decrypt(vault: Vault, key: SessionKey): Pair {
  try {
    const bytes = Buffer.from(vault.encrypted_pair, 'base64');
    const decipher = createDecipheriv('aes-256-gcm', encryptionKey(), bytes.subarray(0, 12));
    decipher.setAAD(associatedData(key));
    decipher.setAuthTag(bytes.subarray(12, 28));
    const pair = pairSchema.parse(JSON.parse(Buffer.concat([decipher.update(bytes.subarray(28)), decipher.final()]).toString('utf8')));
    return gatewayPair(pair.accessToken, pair.refreshToken, key);
  } catch (error) {
    if (error instanceof GatewayError) throw error;
    throw unavailable();
  }
}

async function authorizedSession() {
  const supabase = await createServerSupabase();
  const verified = await supabase.auth.getUser();
  if (verified.error || !verified.data.user) throw expired();
  const { data, error } = await supabase.auth.getSession();
  if (error || !data.session) throw expired();
  // Validate this exact signed JWT before decoding its session_id; getSession alone
  // is only a cookie read. A modified cookie must never select a privileged row.
  const exact = await supabase.auth.getUser(data.session.access_token);
  if (exact.error || !exact.data.user || exact.data.user.id !== verified.data.user.id) throw expired();
  return { supabase, key: sessionKey(data.session, exact.data.user) };
}

/** Only the single successful PKCE exchange may initialize a vault. No request-time
 * provider_token fallback: a deleted/revoked vault cannot be resurrected from cookies. */
export async function persistGatewaySession(session: Session, user: User): Promise<void> {
  const key = sessionKey(session, user);
  if (!session.provider_token || !session.provider_refresh_token) throw expired();
  const pair = gatewayPair(session.provider_token, session.provider_refresh_token, key);
  const { error } = await createAdminSupabase().from('gateway_sessions').upsert({
    session_id: key.session_id, user_id: key.user_id, client_id: key.client_id,
    encrypted_pair: encrypt(pair, key),
  }, { onConflict: 'session_id,user_id,client_id', ignoreDuplicates: true }).abortSignal(AbortSignal.timeout(5_000));
  if (error) throw unavailable();
}

async function readVault(key: SessionKey): Promise<Vault | null> {
  const { data, error } = await createAdminSupabase().from('gateway_sessions').select('*')
    .eq('session_id', key.session_id).eq('user_id', key.user_id).eq('client_id', key.client_id)
    .abortSignal(AbortSignal.timeout(5_000)).maybeSingle();
  if (error) throw unavailable();
  if (!data) return null;
  const parsed = vaultSchema.safeParse(data);
  if (!parsed.success) throw unavailable();
  return parsed.data;
}
async function removeVault(key: SessionKey, owner?: string): Promise<void> {
  let query = createAdminSupabase().from('gateway_sessions').delete()
    .eq('session_id', key.session_id).eq('user_id', key.user_id).eq('client_id', key.client_id);
  if (owner) query = query.eq('lease_owner', owner);
  const { error } = await query.abortSignal(AbortSignal.timeout(5_000));
  if (error) throw unavailable();
}
async function expireVault(key: SessionKey, owner: string): Promise<boolean> {
  const { data, error } = await createAdminSupabase().rpc('gateway_session_expire', {
    p_session_id: key.session_id, p_user_id: key.user_id, p_client_id: key.client_id, p_owner: owner,
  }).abortSignal(AbortSignal.timeout(5_000));
  if (error || !Array.isArray(data)) throw unavailable();
  return data.length === 1;
}
async function acquireVault(key: SessionKey, vault: Vault, owner: string): Promise<Vault | null> {
  const { data, error } = await createAdminSupabase().rpc('gateway_session_acquire', {
    p_session_id: key.session_id, p_user_id: key.user_id, p_client_id: key.client_id,
    p_version: vault.version, p_owner: owner,
  }).abortSignal(AbortSignal.timeout(5_000));
  if (error) throw unavailable();
  if (!Array.isArray(data) || !data.length) return null;
  const parsed = vaultSchema.safeParse(data[0]);
  if (!parsed.success) throw unavailable();
  return parsed.data;
}

export async function getGatewayAccessToken(options: { force?: boolean; expectedToken?: string } = {}): Promise<string> {
  const context = await authorizedSession();
  const deadline = Date.now() + 12_000;
  const owner = randomUUID();
  while (Date.now() < deadline) {
    const vault = await readVault(context.key);
    if (!vault) {
      await context.supabase.auth.signOut({ scope: 'local' });
      throw expired();
    }
    if (vault.lease_owner) {
      if (await expireVault(context.key, vault.lease_owner)) {
        // Database-clock expiry and owner CAS prevent clock skew or a concurrent
        // completed rotation from invalidating a healthy session.
        await context.supabase.auth.signOut({ scope: 'local' });
        throw expired();
      }
      await wait(100);
      continue;
    }
    const pair = decrypt(vault, context.key);
    const forced = options.force && (!options.expectedToken || pair.accessToken === options.expectedToken);
    if (!forced && pair.expiresAt > Date.now() + 60_000) return pair.accessToken;
    const locked = await acquireVault(context.key, vault, owner);
    if (!locked) continue;
    try {
      const body = new URLSearchParams({
        grant_type: 'refresh_token', client_id: context.key.client_id, refresh_token: pair.refreshToken,
      });
      if (process.env.GATEWAY_CLIENT_SECRET) body.set('client_secret', process.env.GATEWAY_CLIENT_SECRET);
      const response = await fetch(`${config.gatewayBaseUrl}/oauth/token`, {
        method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body, cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(8_000),
      });
      const raw: unknown = await response.json().catch(() => null);
      const payload = raw && typeof raw === 'object' ? raw as Record<string, unknown> : {};
      if (!response.ok) {
        if (payload.error === 'invalid_grant') throw expired();
        throw unavailable();
      }
      // Rotation is mandatory. Keeping the old refresh token risks revoked-token replay.
      if (typeof payload.access_token !== 'string' || !payload.access_token ||
          typeof payload.refresh_token !== 'string' || !payload.refresh_token || payload.refresh_token === pair.refreshToken) throw unavailable();
      const next = gatewayPair(payload.access_token, payload.refresh_token, context.key);
      const saved = await createAdminSupabase().rpc('gateway_session_rotate', {
        p_session_id: context.key.session_id, p_user_id: context.key.user_id, p_client_id: context.key.client_id,
        p_owner: owner, p_encrypted_pair: encrypt(next, context.key),
      }).abortSignal(AbortSignal.timeout(5_000));
      if (saved.error || !Array.isArray(saved.data) || saved.data.length !== 1) throw unavailable();
      return next.accessToken;
    } catch (error) {
      // Invalidate the entire session on refresh/save ambiguity. A rotation may
      // have committed before its response failed, already clearing our lease;
      // deleting by the old owner alone would leave that live vault behind.
      try { await removeVault(context.key); }
      finally { await context.supabase.auth.signOut({ scope: 'local' }); }
      if (error instanceof GatewayError) throw error;
      throw unavailable();
    }
  }
  throw unavailable();
}

async function revokeRefreshToken(refreshToken: string, clientId: string): Promise<boolean> {
  try {
    const body = new URLSearchParams({ token: refreshToken, token_type_hint: 'refresh_token', client_id: clientId });
    if (process.env.GATEWAY_CLIENT_SECRET) body.set('client_secret', process.env.GATEWAY_CLIENT_SECRET);
    const response = await fetch(`${config.gatewayBaseUrl}/oauth/revoke`, {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body, cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(8_000),
    });
    return response.ok;
  } catch { return false; }
}

/** Cleanup a failed callback using its already verified exchange result, including
 * ambiguous vault insertion failures. This is never a request-time token source. */
export async function discardGatewayCallbackSession(session: Session, user: User): Promise<void> {
  const key = sessionKey(session, user);
  try {
    if (session.provider_refresh_token) await revokeRefreshToken(session.provider_refresh_token, key.client_id);
  } finally { await removeVault(key); }
}

export async function logoutGatewaySession(): Promise<void> {
  const context = await authorizedSession();
  const owner = randomUUID();
  const deadline = Date.now() + 12_000;
  while (Date.now() < deadline) {
    const vault = await readVault(context.key);
    if (!vault) {
      const { error } = await context.supabase.auth.signOut({ scope: 'local' });
      if (error) throw unavailable();
      return;
    }
    if (vault.lease_owner) {
      if (await expireVault(context.key, vault.lease_owner)) {
        const { error } = await context.supabase.auth.signOut({ scope: 'local' });
        if (error) throw unavailable();
        return;
      }
      await wait(100);
      continue;
    }
    if (!await acquireVault(context.key, vault, owner)) continue;
    let revokeFailed = false;
    try {
      const pair = decrypt(vault, context.key);
      revokeFailed = !await revokeRefreshToken(pair.refreshToken, context.key.client_id);
    } catch { revokeFailed = true; }
    // Serialize logout with refresh, then delete before signing out. Never retain a
    // usable local vault even if the upstream revocation endpoint is unavailable.
    try { await removeVault(context.key, owner); }
    finally {
      const { error } = await context.supabase.auth.signOut({ scope: 'local' });
      if (error) throw unavailable();
    }
    if (revokeFailed) throw new GatewayError(503, 'Signed out locally, but gateway revocation could not be confirmed.', 'UPSTREAM_REVOCATION_FAILED');
    return;
  }
  throw unavailable();
}
