/**
 * @new-api/oauth-client
 *
 * Zero-dependency TypeScript SDK for integrating external systems with the
 * new-api OAuth 2.0 / OIDC provider.
 *
 * Usage (server-side / BFF):
 *
 *   import { createOAuthClient } from '@new-api/oauth-client';
 *
 *   const client = createOAuthClient({
 *     baseUrl: 'https://ai.example.com',
 *     clientId: 'nac_xxx',
 *     clientSecret: 'xxx',
 *     redirectUri: 'https://api.example.com/oauth/callback',
 *   });
 *
 *   // 1. Build the authorize URL and redirect the user there.
 *   const url = client.buildAuthorizeUrl({ scope: 'openid profile email api:write' });
 *   res.redirect(url);
 *
 *   // 2. On callback, exchange the code for tokens.
 *   const tokens = await client.exchangeCode(req.query.code, req.query.code_verifier);
 *
 *   // 3. Get user info.
 *   const user = await client.userinfo(tokens.access_token);
 *
 *   // 4. Call AI APIs on behalf of the user (proxy through your BFF).
 *   const chat = await fetch('https://ai.example.com/v1/chat/completions', {
 *     method: 'POST',
 *     headers: { Authorization: `Bearer ${tokens.access_token}` },
 *     body: JSON.stringify({ model: 'gpt-4o', messages: [...] }),
 *   });
 */

export interface OAuthClientConfig {
  /** Base URL of the new-api instance, e.g. https://ai.example.com */
  baseUrl: string;
  /** OAuth client ID (nac_ prefix) */
  clientId: string;
  /** OAuth client secret (server-side only, never expose to browser) */
  clientSecret: string;
  /** Registered redirect URI */
  redirectUri: string;
  /** Optional fetch implementation (defaults to global fetch) */
  fetch?: typeof fetch;
}

export interface AuthorizeUrlOptions {
  /** Space-separated list of scopes, e.g. "openid profile email api:write" */
  scope: string;
  /** Opaque value to prevent CSRF; returned in the callback */
  state?: string;
  /** OIDC nonce, echoed into the ID token when `openid` is requested */
  nonce?: string;
  /** PKCE code challenge (S256) */
  codeChallenge: string;
  /** PKCE method, must be "S256" */
  codeChallengeMethod?: "S256";
}

export interface TokenResponse {
  access_token: string;
  token_type: "Bearer";
  expires_in: number;
  refresh_token: string;
  /** Only returned when the granted scope includes `openid`. */
  id_token?: string;
  scope: string;
}

export interface UserInfo {
  sub: string;
  username?: string;
  name?: string;
  email?: string;
}

export interface IntrospectResponse {
  active: boolean;
  scope?: string;
  client_id?: string;
  username?: string;
  sub?: string;
  token_type?: string;
  exp?: number;
  iat?: number;
}

export interface DiscoveryDocument {
  issuer: string;
  authorization_endpoint: string;
  token_endpoint: string;
  userinfo_endpoint: string;
  jwks_uri: string;
  end_session_endpoint: string;
  introspection_endpoint: string;
  revocation_endpoint: string;
  scopes_supported: string[];
  response_types_supported: string[];
  response_modes_supported: string[];
  grant_types_supported: string[];
  subject_types_supported: string[];
  id_token_signing_alg_values_supported: string[];
  token_endpoint_auth_methods_supported: string[];
  code_challenge_methods_supported: string[];
  claims_supported: string[];
}

/**
 * Creates a new new-api OAuth client.
 */
export function createOAuthClient(config: OAuthClientConfig) {
  const baseUrl = config.baseUrl.replace(/\/$/, "");
  const httpFetch = config.fetch ?? fetch;

  /**
   * Builds the authorization URL to redirect the user to.
   */
  function buildAuthorizeUrl(options: AuthorizeUrlOptions): string {
    const params = new URLSearchParams({
      response_type: "code",
      client_id: config.clientId,
      redirect_uri: config.redirectUri,
      scope: options.scope,
      code_challenge: options.codeChallenge,
      code_challenge_method: options.codeChallengeMethod ?? "S256",
    });
    if (options.state) {
      params.set("state", options.state);
    }
    if (options.nonce) {
      params.set("nonce", options.nonce);
    }
    return `${baseUrl}/oauth/authorize?${params.toString()}`;
  }

  /**
   * Exchanges an authorization code for tokens.
   */
  async function exchangeCode(
    code: string,
    codeVerifier: string,
  ): Promise<TokenResponse> {
    const body = new URLSearchParams({
      grant_type: "authorization_code",
      code,
      redirect_uri: config.redirectUri,
      code_verifier: codeVerifier,
    });
    return tokenRequest(body);
  }

  /**
   * Exchanges a refresh token for a new set of tokens (token rotation).
   */
  async function refreshToken(refreshToken: string): Promise<TokenResponse> {
    const body = new URLSearchParams({
      grant_type: "refresh_token",
      refresh_token: refreshToken,
    });
    return tokenRequest(body);
  }

  /**
   * Returns user info for the given access token.
   */
  async function userinfo(accessToken: string): Promise<UserInfo> {
    const res = await httpFetch(`${baseUrl}/oauth/userinfo`, {
      headers: { Authorization: `Bearer ${accessToken}` },
    });
    if (!res.ok) {
      throw new Error(`userinfo failed: ${res.status}`);
    }
    return res.json();
  }

  /**
   * Introspects a token (access or refresh) to check its validity.
   */
  async function introspect(token: string): Promise<IntrospectResponse> {
    const credentials = btoa(`${config.clientId}:${config.clientSecret}`);
    const body = new URLSearchParams({ token });
    const res = await httpFetch(`${baseUrl}/oauth/introspect`, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        Authorization: `Basic ${credentials}`,
      },
      body: body.toString(),
    });
    if (!res.ok) {
      throw new Error(`introspect failed: ${res.status}`);
    }
    return res.json();
  }

  /**
   * Revokes a refresh token.
   */
  async function revoke(token: string): Promise<void> {
    const credentials = btoa(`${config.clientId}:${config.clientSecret}`);
    const body = new URLSearchParams({ token });
    const res = await httpFetch(`${baseUrl}/oauth/revoke`, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        Authorization: `Basic ${credentials}`,
      },
      body: body.toString(),
    });
    if (!res.ok) {
      throw new Error(`revoke failed: ${res.status}`);
    }
  }

  /**
   * Builds the end-session (logout) URL.
   */
  function buildLogoutUrl(options: {
    idTokenHint?: string;
    postLogoutRedirectUri?: string;
    state?: string;
  }): string {
    const params = new URLSearchParams();
    if (options.idTokenHint) {
      params.set("id_token_hint", options.idTokenHint);
    }
    if (options.postLogoutRedirectUri) {
      params.set("post_logout_redirect_uri", options.postLogoutRedirectUri);
    }
    if (options.state) {
      params.set("state", options.state);
    }
    const qs = params.toString();
    return `${baseUrl}/oauth/logout${qs ? `?${qs}` : ""}`;
  }

  /**
   * Fetches the OIDC discovery document.
   */
  async function discover(): Promise<DiscoveryDocument> {
    const res = await httpFetch(
      `${baseUrl}/.well-known/openid-configuration`,
    );
    if (!res.ok) {
      throw new Error(`discovery failed: ${res.status}`);
    }
    return res.json();
  }

  // --- Internal helpers ---

  async function tokenRequest(body: URLSearchParams): Promise<TokenResponse> {
    const credentials = btoa(`${config.clientId}:${config.clientSecret}`);
    const res = await httpFetch(`${baseUrl}/oauth/token`, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        Authorization: `Basic ${credentials}`,
      },
      body: body.toString(),
    });
    const data = await res.json();
    if (!res.ok) {
      throw new OAuthError(
        data.error ?? "unknown_error",
        data.error_description,
      );
    }
    return data as TokenResponse;
  }

  return {
    buildAuthorizeUrl,
    exchangeCode,
    refreshToken,
    userinfo,
    introspect,
    revoke,
    buildLogoutUrl,
    discover,
  };
}

/**
 * Error thrown when the OAuth token endpoint returns an error.
 */
export class OAuthError extends Error {
  constructor(
    public readonly code: string,
    description?: string,
  ) {
    super(description ? `${code}: ${description}` : code);
    this.name = "OAuthError";
  }
}

/**
 * Generates a PKCE code verifier and its S256 challenge.
 * Call this before building the authorize URL; store the verifier server-side
 * (e.g. in the user's session) and pass the challenge to buildAuthorizeUrl.
 */
export function generatePKCEPair(): {
  codeVerifier: string;
  codeChallenge: string;
} {
  const codeVerifier = base64UrlEncode(cryptoRandomBytes(32));
  const codeChallenge = base64UrlEncode(sha256(codeVerifier));
  return { codeVerifier, codeChallenge };
}

/**
 * Generates a random state string for CSRF protection.
 */
export function generateState(): string {
  return base64UrlEncode(cryptoRandomBytes(16));
}

// --- Crypto helpers (Node.js primary target for BFF usage) ---

function cryptoRandomBytes(length: number): Uint8Array {
  // Node.js crypto is always available in the BFF environment.
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const nodeCrypto = require("crypto") as typeof import("crypto");
  return new Uint8Array(nodeCrypto.randomBytes(length));
}

function sha256(input: string): Uint8Array {
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const nodeCrypto = require("crypto") as typeof import("crypto");
  return new Uint8Array(nodeCrypto.createHash("sha256").update(input).digest());
}

function base64UrlEncode(bytes: Uint8Array): string {
  let str = "";
  for (let i = 0; i < bytes.length; i++) {
    str += String.fromCharCode(bytes[i]);
  }
  return btoa(str).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
