# Local Acceptance Standard (Cloudflare Tunnel + Supabase URLs)

Applies when you run the full OAuth / OIDC flow locally against a **cloud**
Supabase project. This file is the detail behind the compact local-acceptance
block in **Step 2** of `../../SKILL.md`; the scripts referenced here are
`../../scripts/check-env.ps1`, `../../scripts/check-env.sh`,
`../../scripts/bootstrap.ps1`, and `../../scripts/bootstrap.sh`.

## 1. Local verification notes

| Symptom | Cause and fix |
|---------|---------------|
| Login bounces back to `/sign-in` after a restart | `SESSION_SECRET` is unset, so new-api generates a random secret per process and invalidates refresh cookies. Set it in `.env` once. |
| `/oauth/authorize` requires authentication | It authenticates a browser top-level navigation from the refresh cookie (`Path=/`, HttpOnly) because the request carries no `Authorization` header. Keep the gateway and frontend on the same host so that cookie is sent. |
| Supabase cloud project cannot finish the flow against `http://localhost` | Its server exchanges the code at the issuer URL and rejects a non-public issuer. Expose the gateway over HTTPS (tunnel or deployed host), put that origin in new-api's server address setting, and only then register the provider. Locally you can verify up to the consent redirect (`302 /oauth/consent`). |

## 2. Supabase site_url and additional_redirect_urls

**Supabase site_url / additional_redirect_urls**：`http://localhost:*` 被 Auth 默认允许，authorize 端点直接返回 302，无需额外配置。非 localhost 域名（Cloudflare tunnel、生产域名）需要在 **Supabase Dashboard → Auth → URL Configuration** 手动加入，或通过 **Supabase Management API**（需要 `sb_mgmt_xxx` token，不是 service_role）。`PUT /auth/v1/admin/config`（旧 Auth Admin API 端点）在新版 Supabase 返回 404，已经废弃。

For the Management API form, the path is
`/v1/projects/<ref>/auth/config` with an `sb_mgmt_xxx` token.

## 3. Local acceptance with Cloudflare Tunnel

Supabase's Auth service fetches the issuer's discovery document when you create
or use the custom provider. It cannot reach `http://localhost`, so to run the
full login flow against a cloud Supabase project you must expose the gateway
over HTTPS. Do **not** tunnel the Next.js frontend — Supabase reaches the
**gateway** (the OIDC issuer), not the app.

```bash
# 1. 启动 tunnel 暴露网关端口（new-api 默认 http://localhost:3000）
cloudflared tunnel --url http://localhost:3000
# → https://<random>.trycloudflare.com （每次重启 hostname 都变）
```

拿到 tunnel origin 后，两步闭环。

**Step 2a — 追加到 new-api OAuth app 的 redirect_uris（先 GET 当前值再追加，不要覆盖）：**

```powershell
# Windows / PowerShell
$appId = "3"   # 你的 OAuth app id
$tunnelOrigin = "https://xxxxxxxx.trycloudflare.com"
$current = newapi-cli api GET "/api/oauth-apps/$appId" | ConvertFrom-Json
$uris = @($current.redirect_uris)
if ($uris -notcontains "$tunnelOrigin/auth/v1/callback") {
  $uris += "$tunnelOrigin/auth/v1/callback"
}
newapi-cli api PUT "/api/oauth-apps/$appId" --data (@{ redirect_uris = $uris } | ConvertTo-Json)
```

```bash
# macOS / Linux (jq)
app_id=3
tunnel="https://xxxxxxxx.trycloudflare.com"
callback="$tunnel/auth/v1/callback"
uris=$(newapi-cli api GET "/api/oauth-apps/$app_id" \
  | jq -c --arg cb "$callback" '(.redirect_uris // []) + [$cb] | unique')
newapi-cli api PUT "/api/oauth-apps/$app_id" --data "{\"redirect_uris\":$uris}"
```

`unique` appends the callback only when it is missing and keeps the stored array
valid JSON; do not PUT a hand-built string.

**Step 2b — Supabase Dashboard → Auth → URL Configuration**：在 "Additional Redirect URLs" 里加 `$tunnelOrigin` 和 `$tunnelOrigin/auth/v1/callback`（`PUT /auth/v1/admin/config` 旧端点在新版返回 404，用 Dashboard 最稳）。

Tunnel origins are anonymous and temporary — **re-run 2a every time you
restart cloudflared** (2b 在 Dashboard 里一次配置多个 tunnel origin 即可，
或者用 Supabase Management API `/v1/projects/<ref>/auth/config` 搭配
`sb_mgmt_xxx` token 自动化)。Do not commit a tunnel hostname, and remove it
after testing. `bootstrap.ps1` / `bootstrap.sh` handle the localhost case
automatically; tunnel origin handling is left manual because the hostname
changes each session.
