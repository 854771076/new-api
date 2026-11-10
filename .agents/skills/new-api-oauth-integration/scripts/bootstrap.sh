#!/usr/bin/env bash
# scripts/bootstrap.sh — auto-configure new-api OAuth + Supabase external app
# POSIX counterpart of bootstrap.ps1. Depends on: newapi-cli (already logged in),
# supabase CLI, bun, curl, and jq or python3.
# Usage: from project root: bash scripts/bootstrap.sh --project-name "AI Outfit"
# Flow: probe newapi-cli login -> probe existing OAuth app (reuse) ->
#       supabase link -> write .env.local -> db push -> custom:newapi provider
#       check/create -> authorize smoke test -> admin bootstrap (UUID mapping)
set -euo pipefail

step() { printf '\n>> %s\n' "$1"; }
ok() { printf '   OK  %s\n' "$1"; }
warn() { printf '   WARN %s\n' "$1"; }
err() { printf '   ERR  %s\n' "$1"; }

# indent_output prefixes multi-line CLI output with the same 3 spaces ps1 uses.
indent_output() {
  [ -n "$1" ] || return 0
  while IFS= read -r line; do
    printf '   %s\n' "$line"
  done <<< "$1"
}

# json_field <json> <jq filter> <python expression>
# Prints the queried value (empty when absent). jq is preferred; python3 is the
# fallback so the script works without jq. Exits 1 when neither is installed.
json_field() {
  local json="$1" jq_filter="$2" py_expr="$3" value
  if command -v jq >/dev/null 2>&1; then
    value="$(printf '%s' "$json" | jq -r "$jq_filter" 2>/dev/null || true)"
  elif command -v python3 >/dev/null 2>&1; then
    value="$(printf '%s' "$json" | python3 -c 'import json, os, sys
d = json.load(sys.stdin)
v = eval(sys.argv[1])
if v is None:
    v = ""
print(v)' "$py_expr" 2>/dev/null || true)"
  else
    err '缺少 jq 或 python3；请安装其中之一（install jq or python3）'
    exit 1
  fi
  if [ "$value" = 'null' ] || [ "$value" = 'None' ]; then
    value=''
  fi
  printf '%s' "$value"
}

usage() {
  printf '用法：%s [--project-name <name>] [--client-id <oauth-app-id>] [--pack-root <dir>]\n' "$0"
  printf '  --project-name <name>        项目名（默认：%s）\n' "$project_name"
  printf '  --client-id <oauth-app-id>   指定 new-api OAuth 应用；留空则自动复用已有 public client\n'
  printf '  --pack-root <dir>            资产包根目录；留空则跳过 seed-assets\n'
  printf '  -h, --help                  显示本帮助\n'
}

# Accepted for CLI parity with bootstrap.ps1, which declares but does not use it.
project_name='AI Outfit'
client_id_override=''
pack_root=''

while [ "$#" -gt 0 ]; do
  case "$1" in
    --project-name)
      [ "$#" -ge 2 ] || { printf '参数 --project-name 缺少取值\n' >&2; exit 2; }
      project_name="$2"
      shift 2
      ;;
    --client-id)
      [ "$#" -ge 2 ] || { printf '参数 --client-id 缺少取值\n' >&2; exit 2; }
      client_id_override="$2"
      shift 2
      ;;
    --pack-root)
      [ "$#" -ge 2 ] || { printf '参数 --pack-root 缺少取值\n' >&2; exit 2; }
      pack_root="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf '未知参数：%s（可用：--project-name、--client-id、--pack-root、--help）\n' "$1" >&2
      exit 2
      ;;
  esac
done

# bootstrap.sh lives in <project>/scripts, so run from the project root.
script_dir="$(cd "$(dirname "$0")" && pwd)"
cd "$script_dir/.." || exit 1

# --- 1. Toolchain ---
step '检查工具链'
for cmd in newapi-cli supabase bun; do
  if ! cmd_path="$(command -v "$cmd")"; then
    err "未找到 $cmd；先运行 bash scripts/check-env.sh --install，或手动安装后重试"
    exit 1
  fi
  ok "$cmd: $cmd_path"
done

# --- 2. newapi-cli auth status ---
step 'newapi-cli auth status'
if ! auth_status_json="$(newapi-cli auth status)"; then
  err '未登录：请先运行 newapi-cli auth login'
  exit 1
fi
indent_output "$auth_status_json"

# --- 3. Get self user + OAuth apps ---
step '获取 new-api 用户与 OAuth 应用'
if ! self_json="$(newapi-cli api GET /api/user/self)"; then
  err '读取 /api/user/self 失败'
  exit 1
fi
if ! apps_json="$(newapi-cli api GET /api/oauth-apps)"; then
  err '读取 /api/oauth-apps 失败'
  exit 1
fi

self_username="$(json_field "$self_json" '.username // ""' 'd.get("username") or ""')"
self_id="$(json_field "$self_json" '.id // ""' 'd.get("id", "")')"
self_role="$(json_field "$self_json" '.role // ""' 'd.get("role", "")')"
if [ -z "$self_id" ]; then
  err '无法解析当前用户 id'
  exit 1
fi
ok "用户：$self_username（id=$self_id, role=$self_role）"

# --- 4. Pick the OAuth app (reuse) ---
selected_client_id="$client_id_override"
supabase_project_ref=''
if [ -z "$selected_client_id" ]; then
  app_summary="$(json_field "$apps_json" \
    '[.[]? | select(.public_client == true and ((.supabase_project_ref // "") != ""))] | if length == 0 then empty else (.[0] | "\(.client_id // "")\t\(.supabase_project_ref // "")\t\(.name // "")") end' \
    'next(("\t".join([a.get("client_id") or "", a.get("supabase_project_ref") or "", a.get("name") or ""]) for a in d if a.get("public_client") is True and (a.get("supabase_project_ref") or "")), "")')"
  if [ -z "$app_summary" ]; then
    warn '未找到 public_client=true 且配置了 supabase_project_ref 的 OAuth 应用；请先在 new-api 后台创建'
    exit 1
  fi
  IFS=$'\t' read -r selected_client_id supabase_project_ref app_name <<< "$app_summary"
  ok "复用 OAuth 应用：$app_name -> client_id=$selected_client_id"
  ok "绑定 Supabase 项目：$supabase_project_ref"
else
  export NEWAPI_APP_CLIENT_ID="$selected_client_id"
  supabase_project_ref="$(json_field "$apps_json" \
    '.[]? | select(.client_id == env.NEWAPI_APP_CLIENT_ID) | .supabase_project_ref // ""' \
    'next((a.get("supabase_project_ref") or "" for a in d if a.get("client_id") == os.environ.get("NEWAPI_APP_CLIENT_ID")), "")')"
  unset NEWAPI_APP_CLIENT_ID
  if [ -z "$supabase_project_ref" ]; then
    err "client_id=$selected_client_id 未绑定 supabase_project_ref；请在 new-api 后台补充后重试"
    exit 1
  fi
  ok "使用指定 OAuth 应用：client_id=$selected_client_id"
  ok "绑定 Supabase 项目：$supabase_project_ref"
fi

# --- 5. Supabase link ---
step "supabase link --project-ref $supabase_project_ref"
if ! supabase link --project-ref "$supabase_project_ref"; then
  err 'supabase link 失败'
  exit 1
fi

# --- 6. Supabase API keys ---
step '获取 Supabase API keys'
if ! keys_text="$(supabase projects api-keys 2>&1)"; then
  indent_output "$keys_text"
  err '获取 Supabase API keys 失败；请先运行 supabase login'
  exit 1
fi

# The CLI prints a "NAME | KEY" table; take the second column of each row.
anon_key="$(printf '%s\n' "$keys_text" | awk -F'|' '$1 ~ /^[[:space:]]*anon[[:space:]]*$/ { gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2); print $2; exit }')"
service_role_key="$(printf '%s\n' "$keys_text" | awk -F'|' '$1 ~ /^[[:space:]]*service_role[[:space:]]*$/ { gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2); print $2; exit }')"
if [ -z "$anon_key" ] || [ -z "$service_role_key" ]; then
  err '未能解析 anon / service_role key；请确认已 supabase login 且项目已 link'
  exit 1
fi

server_url="$(json_field "$auth_status_json" '.server // ""' 'd.get("server") or ""')"
if [ -z "$server_url" ]; then
  err '无法确定 new-api 服务地址（newapi-cli auth status 缺少 server）'
  exit 1
fi

ok "网关：$server_url"
ok "Anon key（长度=${#anon_key}）"
ok "Service_role key（长度=${#service_role_key}）"

# --- 7. Write .env.local ---
step '写入 .env.local'
umask 077
{
  printf '# auto-generated by scripts/bootstrap.sh\n'
  printf 'NEXT_PUBLIC_SUPABASE_URL=https://%s.supabase.co\n' "$supabase_project_ref"
  printf 'NEXT_PUBLIC_SUPABASE_ANON_KEY=%s\n' "$anon_key"
  printf 'SUPABASE_SECRET_KEY=%s\n' "$service_role_key"
  printf 'NEXT_PUBLIC_GATEWAY_BASE_URL=%s\n' "$server_url"
  printf 'GATEWAY_PUBLIC_URL=%s\n' "$server_url"
  printf 'NEXT_PUBLIC_GATEWAY_CLIENT_ID=%s\n' "$selected_client_id"
  printf 'APP_ORIGIN=http://localhost:3000\n'
  printf 'INITIAL_ADMIN_SUB=%s\n' "$self_id"
} > .env.local
chmod 600 .env.local
ok '.env.local 已写入（权限 600）'

# --- 8. db push ---
step 'supabase db push --remote --yes'
if ! supabase db push --remote --yes; then
  err 'supabase db push 失败'
  exit 1
fi

# --- 9. Check custom:newapi provider ---
# Endpoint is /auth/v1/admin/custom-providers (plural), not /providers.
# PUT /auth/v1/admin/config is deprecated; localhost redirects are auto-allowed.
step '检查 Supabase custom:newapi provider'
custom_providers_url="https://$supabase_project_ref.supabase.co/auth/v1/admin/custom-providers"
if providers_json="$(curl -fsS --max-time 15 \
  -H "apikey: $service_role_key" \
  -H "Authorization: Bearer $service_role_key" \
  "$custom_providers_url" 2>/dev/null)"; then
  provider_info="$(json_field "$providers_json" \
    '[.providers[]? | select(.identifier == "custom:newapi")] | if length == 0 then empty else (.[0] | "\(.issuer // "")\t\(.pkce_enabled // false)\t\(.email_optional // false)") end' \
    'next(("\t".join([p.get("issuer") or "", "true" if p.get("pkce_enabled") else "false", "true" if p.get("email_optional") else "false"]) for p in (d.get("providers") or []) if p.get("identifier") == "custom:newapi"), "")')"

  if [ -n "$provider_info" ]; then
    IFS=$'\t' read -r provider_issuer provider_pkce provider_email_optional <<< "$provider_info"
    ok "custom:newapi 已存在（issuer=$provider_issuer, pkce=$provider_pkce）"
    if [ "$provider_issuer" != "$server_url" ] || [ "$provider_pkce" != 'true' ] || [ "$provider_email_optional" != 'true' ]; then
      warn 'provider 配置与网关不一致，删除后重建'
      if curl -fsS --max-time 15 -X DELETE \
        -H "apikey: $service_role_key" \
        -H "Authorization: Bearer $service_role_key" \
        "$custom_providers_url/custom:newapi" -o /dev/null; then
        provider_info=''
      else
        err 'provider 删除失败'
      fi
    fi
  fi

  if [ -z "$provider_info" ]; then
    step '创建 custom:newapi provider'
    provider_body="$(printf '{"provider_type":"oidc","identifier":"custom:newapi","name":"new-api","issuer":"%s","client_id":"%s","client_secret":"ignored-by-public-client","scopes":["openid","profile","email","api:read","api:write"],"pkce_enabled":true,"email_optional":true}' "$server_url" "$selected_client_id")"
    if curl -fsS --max-time 15 -X POST \
      -H "apikey: $service_role_key" \
      -H "Authorization: Bearer $service_role_key" \
      -H 'Content-Type: application/json' \
      -d "$provider_body" -o /dev/null "$custom_providers_url"; then
      ok 'provider 已创建'
    else
      err 'provider 创建失败'
    fi
  fi
else
  err 'provider 检查/创建失败：无法读取 provider 列表（确认 service_role key 与项目 ref）'
fi

# --- 10. Authorize endpoint smoke test ---
step 'authorize 端点冒烟测试'
authorize_url="https://$supabase_project_ref.supabase.co/auth/v1/authorize?provider=custom:newapi&redirect_to=http://localhost:3000/auth/callback"
authorize_status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
  -H "apikey: $service_role_key" \
  -H "Authorization: Bearer $service_role_key" \
  "$authorize_url" 2>/dev/null || true)"
case "$authorize_status" in
  302|303|307) ok "authorize -> $authorize_status" ;;
  *) warn "authorize 返回 ${authorize_status:-无响应}（期望 302/303/307）" ;;
esac

# --- 11. Admin bootstrap ---
# new-api OAuth sub is an integer user id (stored in auth.users.raw_user_meta_data.sub).
# app_admins.user_id must be auth.users.id (UUID). Query to map them.
step "管理员初始化：解析 auth.users 中 sub=$self_id 的 UUID"
uuid_query="SELECT id::text FROM auth.users WHERE raw_user_meta_data->>'sub' = '$self_id' AND raw_app_meta_data->>'provider' = 'custom:newapi' LIMIT 1;"
uuid_result="$(supabase db query --remote --yes "$uuid_query" 2>&1 || true)"
indent_output "$uuid_result"

admin_uuid="$(printf '%s\n' "$uuid_result" | grep -oE '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' | head -n 1 || true)"
if [ -n "$admin_uuid" ]; then
  ok "auth.users UUID：$admin_uuid"
  admin_insert="$(supabase db query --remote --yes "INSERT INTO public.app_admins (user_id, role, created_at) VALUES ('$admin_uuid'::uuid, 'owner', now()) ON CONFLICT (user_id) DO NOTHING RETURNING id, role;" 2>&1 || true)"
  indent_output "$admin_insert"

  trap 'rm -f .env.local.tmp' EXIT
  {
    while IFS= read -r line || [ -n "$line" ]; do
      case "$line" in
        INITIAL_ADMIN_SUB=*) printf 'INITIAL_ADMIN_SUB=%s\n' "$admin_uuid" ;;
        *) printf '%s\n' "$line" ;;
      esac
    done < .env.local
  } > .env.local.tmp
  mv .env.local.tmp .env.local
  chmod 600 .env.local
  ok '.env.local 的 INITIAL_ADMIN_SUB 已更新为 UUID'
else
  warn "未找到 sub=$self_id 的 auth.users 记录；管理员需先用 custom:newapi 登录一次"
  warn "然后手动执行：supabase db query --remote \"INSERT INTO public.app_admins (user_id, role, created_at) VALUES ('<uuid>'::uuid, 'owner', now());\""
fi

# --- 12. Asset seed (optional) ---
if [ -n "$pack_root" ]; then
  step "资产导入：bun run scripts/seed-assets.ts --pack-root $pack_root --client-id $selected_client_id"
  if ! bun run scripts/seed-assets.ts --pack-root "$pack_root" --client-id "$selected_client_id"; then
    err '资产导入失败'
    exit 1
  fi
fi

# --- 13. Summary ---
printf '\n*** bootstrap done ***\n'
printf '  1. bun run dev\n'
printf '  2. http://localhost:3000/login -> OAuth 登录流程\n'
printf '  3. http://localhost:3000/admin（需先完成登录与管理员初始化）\n'
printf '  4. 未传 --pack-root 时：bun run scripts/seed-assets.ts --pack-root <path> --client-id %s\n' "$selected_client_id"
