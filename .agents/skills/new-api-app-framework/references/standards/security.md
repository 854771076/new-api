# Security Standard (Checklist and Token Lifecycle)

This file is the security contract behind **Security Checklist** and **Token Lifecycle** in `../../SKILL.md`. Apply the common controls and the selected mode's controls. Callback, PKCE and provider-token rules apply to OAuth; native Supabase proxy mode does not need an OIDC provider.

## Proxy Mode

- A natural person using multiple Apps has a separate proxy gateway user ID per App. The proxy account has immutable App ownership: never create another App's key for that account or reuse it across Apps. Existing OAuth/platform account behavior is unchanged.
- Controlled provisioning atomically binds external identity, proxy origin and App ownership, with authorized recovery and no reactivation by retry; see `identity.md`.
- Rotation must preserve one logical spending budget across old/new credentials: lifetime/remaining limits, periodic usage and in-flight reservations, settlement and refunds. A new key ID or one-time copy of counters must not reset or duplicate the budget. With cap 100 and usage 90, only 10 remains across both keys, including concurrent requests and late old-key settlement; retries cannot double reserve/refund. If the deployed server cannot preserve this continuity, reject rotation of budget-bound keys explicitly. This adds no wallet and does not claim the independent quota-rule development is shipped.

- Validate the Supabase session before resolving the trusted new-api user/App mapping. Browser-supplied user IDs, user-editable metadata and UI mode selection cannot authorize another user's key or wallet.
- Keep proxy and OAuth users independent; do not share IDs/assets or reuse OAuth profiles for native users. Reject ambiguous Supabase auto-linked identities. Enforce proxy subscription unsupported at reads, checkout, admin grants and funding, even on dual-mode Apps.
- Keep per-user keys, admin PATs and Supabase secret keys in server-only stores. Scope PATs to the operations needed, support expiry/revocation, and exclude credentials from logs, client responses and profile/JWT metadata.
- Verify the deployed server supports the specified-user and App funding operations before using them. Ordinary keys do not gain App wallet isolation because the caller supplies `app_id`; admin PAT authority is not App-restricted by that parameter either.
- Keep payment order ownership fixed, accept only verified provider confirmation for crediting, and query the actual order state. Do not replay generation requests to recover a revoked key or treat OAuth refresh as API-key rotation.
- Normal key rotation saves/verifies a replacement before switching and revoking the old key, with operation-ID recovery after timeouts or storage failure. Emergency revocation remains immediate. Disabled Apps still permit authorized historical order queries and settlement of valid accepted orders; new purchases/calls stay blocked.
- This development branch enforces OAuth disablement at authorize, token, refresh, access-token validation and relay entry points; it hides OAuth-disabled Apps from the existing admin selector. Settings changes and refresh-token revocation commit together. Proxy-account/key enforcement and regular-user wallet selection are also implemented on this branch. Hiding a control does not revoke credentials or remove assets, and disabling a mode must not prevent accepted tasks from settling/refunding.

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
- [ ] OAuth tokens are used only for `/v1/*` and `/oauth/*` (userinfo, app plans/products, checkouts, top-up, logs); every `/api/*` dashboard call uses a session or a scoped PAT.
- [ ] Personal access tokens are stored encrypted and carry only the scopes the integration actually needs.
- [ ] Your system never treats a browser return URL or a `pending` order as proof of payment; quota is credited only after new-api confirms the provider payment.
- [ ] Admin console permissions come from `app_metadata`/`app_admins`, and every admin Route Handler calls the server guard; the browser role is UX only.
- [ ] `SUPABASE_SECRET_KEY` is imported only by a `server-only` module and never exposed through `NEXT_PUBLIC_*`.
- [ ] Admin (Ant Design) and consumer (Tailwind + DaisyUI) UIs never share a layout or styling system.
- [ ] The first admin is granted once out of band; no endpoint or first-signup rule can create an admin. OAuth mode uses the OAuth app developer (`oauth_apps.created_by` → `INITIAL_ADMIN_SUB`); proxy mode uses an explicitly trusted Supabase user.
- [ ] All media assets are stored in Supabase Storage private buckets; only `bucket` + `path` is stored in Postgres, and reads use short-lived signed URLs.
- [ ] Balance, credits, orders and usage come from new-api, with no local ledger. OAuth mode uses its `/oauth/*` contracts; proxy mode uses only verified specified-user contracts and never substitutes the administrator's self wallet.

## Token Lifecycle

The TTLs and provider-refresh procedure below apply to **OAuth credentials**. Proxy mode instead uses its API keys' and PATs' actual expiry, revocation and rotation contracts.

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
`../web/outfit-demo/src/lib/gateway-session.ts` is the reference implementation.

When the user logs out of new-api or changes their password, all their tokens
are immediately invalidated. Handle `401` by re-authenticating; do not keep
retrying with the same token.

## Timed proxy membership rules

Use the specified-user quota/cancel contract in `api-contract.md`. Rule dates enforce an enabled key's new-request admission and do not fund wallets or enable proxy subscriptions. Disabling/unbinding removes restrictions. Each key has independent calendar usage; rule-bound rotation remains unsupported. Cancel pending replacements before binding, and never revive a cancelled operation. Admission snapshots preserve the original budget through rule edits, expiry, deletion and task recovery; legacy snapshots cannot supplement reservations. A task stranded before a confirmed upstream result remains `SUBMITTING` for explicit reconciliation, without automatic repeat work or refunds. The reference admin BFF pins native mapping and App; ordinary consumers cannot write paid entitlement gates.
