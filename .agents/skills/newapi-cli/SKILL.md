---
name: newapi-cli
description: >-
  Operate a running new-api instance from the terminal with the bundled
  `newapi-cli` admin tool. Use when the user asks to log in to a new-api
  server, inspect or manage users, channels, API tokens, logs, server
  options, subscription plans, app products, or OAuth apps, create an API
  key for an agent, script admin API calls, or
  maintain a new-api deployment without the web dashboard. Also use when a
  task mentions `newapi-cli`, `/cli/authorize`, `~/.newapi-cli/config.json`,
  or a `nap_` personal access token.
---

# newapi-cli Admin Workflow

`newapi-cli` is a thin HTTP client for the new-api admin API. It never touches
the database directly and only needs a server URL plus a personal access token
(PAT). Source lives in `cmd/newapi-cli/`; it uses Cobra and the gateway JSON wrapper.

## Install

Use the administrator avatar menu **CLI installation & usage** on your trusted new-api server. Download `/api/cli/install.ps1` (Windows) or `/api/cli/install.sh` (macOS/Linux) from that server and run it with the HTTPS server URL. npm publishing has stopped; do not install/update this CLI from npm or GitHub releases.

The backend bundles six native binaries and `skills.zip` for `newapi-cli` and `new-api-app-framework`, all pinned to `/api/cli/manifest`. Installer downloads time out after three minutes; Windows disables slow progress rendering. Installers verify SHA-256, then install the CLI into `~/.newapi-cli/bin` and both skills into `~/.agents/skills`. Existing skill directories are replaced together; keep personal edits elsewhere. Login profiles in `~/.newapi-cli/config.json` are preserved. Restart the terminal after Windows installation; on macOS/Linux add `~/.newapi-cli/bin` to PATH.

For source/server builds, run `go run ./cmd/newapi-cli-assets <version>` before building the gateway. Docker and release builds do this automatically. A source build without assets returns HTTP 503 at the manifest endpoint instead of advertising an incomplete distribution.

## Authenticate

```bash
newapi-cli auth login                      # uses configured server, or prompts
newapi-cli auth login --server https://api.example.com --name my-agent
newapi-cli auth status
newapi-cli auth logout
```

`auth login` prints a `/cli/authorize` URL. Open it in a browser while signed
in to the dashboard, review the pre-selected permission matrix, confirm, then
paste the one-time code (43 characters, single use) back into the CLI. The CLI
exchanges it at `POST /api/cli/exchange` for a PAT and stores it locally. The
code cannot be reused or widened by the CLI; the grant is exactly what the user
approved.

- Config: `~/.newapi-cli/config.json` (0600, directory 0700), one entry per
  profile. Never commit or print this file.
- Server selection: `--server` > `NEWAPI_CLI_SERVER` > the profile's stored
  server > the build-time default baked into release binaries. Re-run
  `auth login --server <url>` to switch servers; keep each server as its own
  profile with `--profile`.
- Profiles: `--profile <name>` or `NEWAPI_CLI_PROFILE`; default profile is
  `default`. Use one profile per server/environment.
- Non-interactive: `--server` skips the prompt, but the authorization code must
  still come from a browser session. There is no password login for the CLI.

## Required scopes

The PAT carries Casbin scopes. A command whose resource/action is missing
fails with HTTP 403, code `ACCESS_TOKEN_SCOPE_DENIED`, exit code 2 — re-run
`auth login` and grant the missing scope rather than retrying.

| Commands | Scopes |
| --- | --- |
| `user list`, `user get` | `user:read` |
| `channel list`, `channel get` | `channel:read` |
| `token list`, `token get`, `token create` | `api_key:read`, `api_key:write` |
| `log list` | `log:read` |
| `log files`, `log tail`, `log search` | `ops:read` (also requires a root-role token) |
| `option get` | `option:read` |
| `option update` | `option:write` |
| `plan list`, `product list`, `product orders` | `billing:read` |
| `plan create` | `billing:write` |
| `plan update <id>`, `product update <id>` | `billing:read`, `billing:write` |
| `product create`, `product enable`, `product disable`, `product complete-order` | `billing:write` |
| `oauth-app list`, `oauth-app get <id>` | `option:read` |
| `oauth-app create`, `oauth-app update <id>`, `oauth-app delete <id>` | `option:write` |
| `api ...` | the scope of the target endpoint |
| `auth status` | none (local config only) |

The authorize page preselects `profile:read`, `usage:read`, `api_key:read`,
`api_key:write`, `user:read`, `user:write`, `channel:read`, `log:read`,
`model:read`, `model:write`, `deployment:read`, `deployment:write`,
`option:read`, `option:write`. `billing:read` and `billing:write` are not
preselected, so grant them on the authorize page before using `plan` or
`product`.

## Commands

```text
auth login|status|logout
user   list [--p 1] [--page-size 20] [--app-id <id>] | get <id>
channel list [--p 1] [--page-size 20] | get <id>
token  list [--p 1] [--page-size 20] | get <id> | create --name <name> [flags]
log    list [--p 1] [--page-size 20] [--type <n>] [--start-timestamp <s>]
            [--end-timestamp <s>] [--model-name <m>] [--token-name <t>]
            [--group <g>] [--request-id <id>] [--upstream-request-id <id>]
            [--username <u>] [--channel <id>]
        | files
        | tail <file> [--lines N] [--follow]
        | search <keyword> [--since <duration>] [--limit N]
option get <key> | update <key>=<value>
plan   list [--p 1] [--page-size 20] [--app-id <id>]
        | create --title <title> [flags]
        | update <id> [flags]
product list [--app-id <id>] [--p 1] [--page-size 20]
        | create --app-id <id> --name <name> --price <amount> --quota <n> [flags]
        | update <id> [flags]
        | enable <id> | disable <id>
        | orders [--app-id <id>] [--user-id <id>] [--p 1] [--page-size 20]
        | complete-order <trade_no>
oauth-app list | get <id> | create [flags] | update <id> [flags] | delete <id>
version
self-update [--run] [--server URL]
api    <METHOD> </path> [--data '<json>' | --data @file.json]
```

Global flags: `--profile <name>`, `--pretty`.

`token create` flags:

- `--name` (required, max 50 characters).
- `--unlimited-quota` (default `true`): ignores `--quota`.
- `--quota` (default `500000`): only sent when `--unlimited-quota=false`.
- `--expires-in` (Go duration, e.g. `24h`, `720h`): empty means never expires
  (`expired_time = -1`). Invalid durations and negative quota return
  `usage_error` (exit 3).

The create response includes the new token `id` and its **full** `key`. This is
the only time the key is returned; later `token list` / `token get` calls are
masked. Capture it immediately if the caller needs the key.

## App-scoped plans and products

Every plan, product, and app wallet belongs to one app. Omit `--app-id` to see
the platform-wide view; pass it to read that app's data.

```bash
newapi-cli user list --app-id 3                 # adds app_quota / app_used_quota
newapi-cli plan list --app-id 3
newapi-cli plan create --app-id 3 --title "Starter" --price 9.99 --total-amount 100000 \
  --duration-unit month --duration-value 1 --quota-reset-period monthly
newapi-cli plan update 12 --price 12.99 --enabled=false
newapi-cli product list --app-id 3
newapi-cli product create --app-id 3 --name "Starter" --price 9.99 --quota 100000 \
  --description "Starter pack" --currency USD --max-purchase-per-user 1
newapi-cli product update 12 --price 12.99 --enabled=false
newapi-cli product disable 12
newapi-cli product orders --app-id 3 --user-id 42
newapi-cli product complete-order APO123         # write: credits the app wallet

# OAuth apps (外部 Web app)
newapi-cli oauth-app list
newapi-cli oauth-app get 7
newapi-cli oauth-app create --name "My App" \
  --redirect-uris https://app.example.com/callback \
  --post-logout-redirect-uris https://app.example.com/logout \
  --scopes openid,profile,email,api:read,api:write --public-client
newapi-cli oauth-app update 7 --redirect-uris https://app.example.com/callback,https://new.example.com/callback
newapi-cli oauth-app update 7 --status 2
newapi-cli oauth-app delete 7
```

`product create` flags:

- `--app-id`, `--name` (required), `--price` (required, `price_amount`),
  `--quota` (required, `quota_amount`).
- `--description`, `--currency` (default `USD`), `--sort-order`,
  `--max-purchase-per-user` (0 means unlimited), `--enabled` (default `true`),
  `--stripe-price-id`, `--creem-product-id`, `--waffo-pancake-product-id`.

`plan create` flags:

- `--title` (required), `--app-id` (default `0` = platform-wide), `--price`
  (`price_amount`), `--currency` (default `USD`), `--duration-unit`
  (default `month`; `year|month|day|hour|custom`), `--duration-value`
  (default `1`), `--custom-seconds`, `--enabled` (default `true`),
  `--sort-order`, `--allow-balance-pay` (default `true`),
  `--allow-wallet-overflow` (default `true`), `--stripe-price-id`,
  `--creem-product-id`, `--waffo-pancake-product-id`,
  `--max-purchase-per-user` (0 means unlimited), `--upgrade-group`,
  `--downgrade-group`, `--total-amount` (quota units, 0 means unlimited),
  `--quota-reset-period` (default `never`; `never|daily|weekly|monthly|custom`),
  `--quota-reset-custom-seconds`.

`oauth-app create` flags:

- `--name` (required), `--redirect-uris` (required, comma-separated),
  `--scopes` (required, comma-separated).
- `--post-logout-redirect-uris` (comma-separated), `--public-client` (bool,
  no client secret), `--logo-url`, `--supabase-project-ref`,
  `--waffo-pancake-return-url`. `oauth-app create` always yields an enabled
  app; `--status` exists only on `oauth-app update` (`1` enabled, `2`
  disabled).

Notes:

- `product update` reads the current product with `GET /api/app-products/<id>`,
  overlays the flags you pass, then `PUT`s the complete object. Fields you do
  not pass keep their current value, so `product update 12 --price 12.99`
  changes only the price.
- `plan update` and `oauth-app update` follow the same read-modify-write
  pattern. `plan update` reads the current record from the plan list (the
  admin API has no per-plan GET), overlays the passed flags, then `PUT`s the
  complete `{"plan": {...}}` object. `oauth-app update` reads with
  `GET /api/oauth-apps/<id>` then `PUT`s the full object.
- `plan list` returns every plan; the admin plans endpoint accepts the
  pagination flags but does not limit the result.
- `product orders` filters by app and user only; the endpoint has no product
  filter.
- Negative `--app-id`, `--price`, `--quota`, `--max-purchase-per-user`, or
  `--total-amount`, an empty `--title`/`--name`, and a missing required flag
  return `usage_error` (exit 3).

## Version and self-update

```bash
newapi-cli --version                         # offline version
newapi-cli version --server https://gateway.example.com
newapi-cli self-update --server https://gateway.example.com
newapi-cli self-update --run --server https://gateway.example.com
```

`version` checks the configured server on a best-effort basis, never npm. `self-update` downloads and verifies the matching CLI plus both skills before replacing them, and rolls back failed replacements. Updates without `--run` only print the command. Server precedence is `--server`, `NEWAPI_CLI_SERVER`, selected profile, build default. No PAT is sent to distribution endpoints. An explicit trusted HTTPS server is required on first install; HTTP is allowed only for loopback development through the native CLI. The server's bundled version is authoritative, even if it is older than the current local build.

## Output

- Default: the unwrapped `data` field as indented JSON on stdout. Non-enveloped
  responses pass through verbatim. Empty `data` prints `null`.
- `--pretty`: objects print as `key: value` lines, arrays of objects as an
  aligned table. Cells are truncated to 48 runes with `…`; nested values become
  compact JSON. List envelopes with `items` print the table then `total`,
  `page`, `page_size`. Pagination is retained — use the JSON form for scripts.
- Errors go to stderr as `{"error":{"message":"...","code":"..."}}`.
- Both modes are stdout-only; pipe into `jq` when using the default JSON.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | General/API error (business failure, not found, HTTP error) |
| 2 | Authentication/authorization (`not_logged_in`, 401, 403, `invalid_grant`) |
| 3 | Usage error (bad flag, invalid argument, malformed `--data`) |

## Escape hatch: `api`

Use `api` for endpoints without a dedicated subcommand. OAuth apps now have a
dedicated `oauth-app` subcommand (see above), which is preferred over the raw
`api` calls below; the raw form remains for scripting and for fields the
subcommand does not expose (for example `ai_model_whitelist`).

```bash
newapi-cli api GET /api/user/self
newapi-cli api GET '/api/log/?p=1&page_size=20&type=2'
newapi-cli api POST /api/channel/ --data '{"mode":"single","channel":{"name":"upstream","type":1,"key":"sk-..."}}'
newapi-cli api POST /api/token/ --data @token.json

# === OAuth apps (外部 Web app 最关键) ===
newapi-cli api GET /api/oauth-apps                                        # 列出所有
newapi-cli api GET /api/oauth-apps/<id>                                   # 查单条（拿当前 redirect_uris 再追加）
newapi-cli api POST /api/oauth-apps --data '{                              # 创建 public client
  "name": "My App",
  "redirect_uris": ["https://<project>.supabase.co/auth/v1/callback"],
  "post_logout_redirect_uris": ["https://<app>/logout-done"],
  "scopes": ["openid","profile","email","api:read","api:write"],
  "public_client": true,
  "status": 1
}'
newapi-cli api PUT /api/oauth-apps/<id> --data '{                          # 追加 redirect_uri（先 GET 当前数组，再追加，不要直接覆盖）
  "redirect_uris": ["https://old...","https://tunnel-xxx.trycloudflare.com/auth/callback"]
}'
newapi-cli api DELETE /api/oauth-apps/<id>

# auth status 拿到 server URL（自动探测 .env.local 用）
newapi-cli auth status   # → {"server":"https://gateway.example.com","..."}
```

- METHOD must be uppercase; path must start with `/`.
- `--data` accepts inline JSON or `@file`; it must be valid JSON.
- Channel creation wraps the object as `{"mode":"single","channel":{...}}`.
- List endpoints take `p` and `page_size` query parameters.
- `auth status` **不**需要 scope，在任何脚本里都能调，拿 `server` 字段是
  探测 `.env.local` 里 `NEXT_PUBLIC_GATEWAY_BASE_URL` 的最稳妥方式。
- `GET /api/oauth-apps` 返回的对象里带 `supabase_project_ref`（如果该 app
  是 outfit-demo 形态），可以直接用于自动 `supabase link`。
- **追加 redirect_uri 的正确姿势**：先 `GET /api/oauth-apps/<id>` 拿当前
  `redirect_uris` 数组，追加新条目后 `PUT` 回去。直接覆盖会丢掉已有值。
- **`self.id` vs `auth.users.id`**：`newapi-cli api GET /api/user/self`
  返回的 `id`（整数）就是 OAuth `sub` 声明，存放在 Supabase
  `auth.users.raw_user_meta_data->>'sub'`。但 `auth.users.id`（UUID）才是
  业务表（如 `app_admins.user_id`）需要的值。两者不能混用。映射 SQL：
  ```sql
  SELECT id::text FROM auth.users
  WHERE raw_user_meta_data->>'sub' = '<self.id>'
    AND raw_app_meta_data->>'provider' = 'custom:newapi';
  ```

## Safety rules

- Treat the PAT as a credential: never print it, echo it into logs, paste it in
  chat, or write it into a repository file. `~/.newapi-cli/config.json` stays
  outside version control.
- Prefer HTTPS; the token is sent as `Authorization: Bearer <PAT>`.
- Confirm with the user before destructive or production-wide actions:
  `option update`, creating/editing channels, deleting tokens,
  `product complete-order`, and any `api DELETE`/`api PUT`.
- Do not bypass the CLI to edit the database, scrape browser cookies, or reuse
  an authorization code. If a scope is missing, re-authorize.
- Run `auth logout` on shared machines; it removes the active profile only.

## Smoke test

```bash
newapi-cli auth status
newapi-cli api GET /api/user/self
```

If `auth status` reports `not_logged_in`, run `auth login`. If a call returns
403 `ACCESS_TOKEN_SCOPE_DENIED`, the PAT is valid but under-scoped.
