# Security Standard (Checklist and Token Lifecycle)

This file is the full security contract behind **Security Checklist** and
**Token Lifecycle** in `../../SKILL.md`. Nothing here is optional: an
integration that skips one item is not built to this standard.

## Security Checklist

- [ ] Confidential clients keep `client_secret` only in backend environment variables; public clients have no secret and rely on PKCE.
- [ ] Public-client mode is enabled only for apps that cannot keep a secret (SPA, mini-program); server-side apps stay confidential.
- [ ] `redirect_uri` exactly matches a registered URI (BFF callback or `https://<project>.supabase.co/auth/v1/callback`).
- [ ] PKCE `code_verifier` is matched on callback; a public client without it is rejected.
- [ ] `state` parameter is validated to prevent CSRF.
- [ ] OIDC `nonce` is generated per authorization request and validated when an `id_token` is consumed.
- [ ] State, verifier, and session cookies use `HttpOnly`, `Secure` in production, and `SameSite=Lax` (or `SameSite=None; Secure` only for a required cross-site callback).
- [ ] Gateway tokens are kept in memory (optionally `sessionStorage`) and never written to `user_metadata` or logged.
- [ ] Refreshed refresh tokens replace the old value; `invalid_grant` ends the session instead of retrying.
- [ ] HTTPS is enabled in production (required for `SameSite=None` cookies).
- [ ] OAuth tokens are used only for `/v1/*` and `/oauth/userinfo`; quota and payment calls use a dashboard session or a scoped PAT.
- [ ] Personal access tokens are stored encrypted and carry only the scopes the integration actually needs.
- [ ] Your system never treats a browser return URL or a `pending` order as proof of payment; quota is credited only after new-api confirms the provider payment.
- [ ] Admin console permissions come from `app_metadata`/`app_admins`, and every admin Route Handler calls the server guard; the browser role is UX only.
- [ ] `SUPABASE_SECRET_KEY` is imported only by a `server-only` module and never exposed through `NEXT_PUBLIC_*`.
- [ ] Admin (Ant Design) and consumer (Tailwind + DaisyUI) UIs never share a layout or styling system.
- [ ] The first admin defaults to the OAuth app developer (`oauth_apps.created_by` → `INITIAL_ADMIN_SUB`) and is granted once out of band; no endpoint or first-signup rule can create an admin.
- [ ] All media assets are stored in Supabase Storage private buckets; only `bucket` + `path` is stored in Postgres, and reads use short-lived signed URLs.
- [ ] Balance, credits, top-up orders, and usage come from new-api (`/api/user/self`, `/api/user/topup/self`, `/api/log/self`); there is no local ledger, and OAuth-only apps redirect to the dashboard instead of reimplementing payment.

## Token Lifecycle

- **Access token**: JWT, 1 hour TTL, used to call AI APIs and userinfo.
- **Refresh token**: Opaque, 30 day TTL, rotated on each use.
- **Authorization code**: Single-use, 60 second TTL.

### The provider token is not refreshed for you

Supabase refreshes its own session, but it does **not** refresh a custom
provider's `provider_token`. After one hour every direct AI call would fail with
`401`. The application must refresh it:

1. on login, read `session.provider_token` and `session.provider_refresh_token`;
2. before each gateway call, check the access token's `exp` and refresh when less than 60 s remain with `POST <gateway>/oauth/token` (`grant_type=refresh_token`, `client_id`, `refresh_token`; public client: no `client_secret`);
3. replace the stored refresh token with the rotated value; on `invalid_grant`, clear local state and send the user through login again.

Refresh is also the recovery path for a relay `401`: retry once with a fresh
token, never loop. In BFF mode, `oauthClient.refreshToken(session.refreshToken)`
returns the rotated pair; store it in place of the old tokens.
`../web/outfit-demo/lib/gatewayAuth.ts` is the reference implementation.

When the user logs out of new-api or changes their password, all their tokens
are immediately invalidated. Handle `401` by re-authenticating; do not keep
retrying with the same token.
