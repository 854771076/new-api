---
name: newapi-cli
description: >-
  Operate a running new-api instance from the terminal with the bundled
  `newapi-cli` admin tool. Use when the user asks to log in to a new-api
  server, inspect or manage users, channels, API tokens, logs, or server
  options, create an API key for an agent, script admin API calls, or
  maintain a new-api deployment without the web dashboard. Also use when a
  task mentions `newapi-cli`, `/cli/authorize`, `~/.newapi-cli/config.json`,
  or a `nap_` personal access token.
---

# newapi-cli Admin Workflow

`newapi-cli` is a thin HTTP client for the new-api admin API. It never touches
the database directly and only needs a server URL plus a personal access token
(PAT). Source lives in `cmd/newapi-cli/`; it depends only on the standard
library and `spf13/cobra`.

## Install

- npm: `npm install -g @aswless_854771076/newapi-cli` puts `newapi-cli` on `PATH` and
  downloads the matching binary from the package's GitHub release. Installing
  it as a project dependency also copies the bundled `newapi-cli` and
  `new-api-oauth-integration` skills to `.agents/skills/` in that project.
- From source: `go build -o newapi-cli ./cmd/newapi-cli`
  (`newapi-cli.exe` on Windows).

The server must be the same build that embeds the dashboard, because the login
flow is served by the web UI. Rebuild and restart the server after changing
either the frontend or `controller/cli_auth.go`.

## Authenticate

```bash
newapi-cli auth login                      # uses the packaged server by default
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
| `option get` | `option:read` |
| `option update` | `option:write` |
| `api ...` | the scope of the target endpoint |
| `auth status` | none (local config only) |

The authorize page preselects `profile:read`, `usage:read`, `api_key:read`,
`api_key:write`, `user:read`, `user:write`, `channel:read`, `log:read`,
`model:read`, `model:write`, `deployment:read`, `deployment:write`,
`option:read`, `option:write`.

## Commands

```text
auth login|status|logout
user   list [--p 1] [--page-size 20] | get <id>
channel list [--p 1] [--page-size 20] | get <id>
token  list [--p 1] [--page-size 20] | get <id> | create --name <name> [flags]
log    list [--p 1] [--page-size 20] [--type <n>]
option get <key> | update <key>=<value>
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

Use `api` for endpoints without a dedicated subcommand.

```bash
newapi-cli api GET /api/user/self
newapi-cli api GET '/api/log/?p=1&page_size=20&type=2'
newapi-cli api POST /api/channel/ --data '{"mode":"single","channel":{"name":"upstream","type":1,"key":"sk-..."}}'
newapi-cli api POST /api/token/ --data @token.json
```

- METHOD must be uppercase; path must start with `/`.
- `--data` accepts inline JSON or `@file`; it must be valid JSON.
- Channel creation wraps the object as `{"mode":"single","channel":{...}}`.
- List endpoints take `p` and `page_size` query parameters.

## Safety rules

- Treat the PAT as a credential: never print it, echo it into logs, paste it in
  chat, or write it into a repository file. `~/.newapi-cli/config.json` stays
  outside version control.
- Prefer HTTPS; the token is sent as `Authorization: Bearer <PAT>`.
- Confirm with the user before destructive or production-wide actions:
  `option update`, creating/editing channels, deleting tokens, and any
  `api DELETE`/`api PUT`.
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
