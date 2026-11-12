#!/usr/bin/env bash
# scripts/bootstrap.sh — auto-configure new-api OAuth + Supabase external app
# POSIX counterpart of bootstrap.ps1. Depends on: newapi-cli (already logged in),
# supabase CLI, bun, curl, openssl, and jq or python3.
# Usage: from project root: bash scripts/bootstrap.sh --project-name "AI Outfit"
# Flow: probe newapi-cli login -> probe existing OAuth app (reuse) ->
#       supabase link -> write .env.local -> db push -> custom:newapi provider
#       check/create -> authorize smoke test -> admin bootstrap (UUID mapping)
set -euo pipefail

step() { printf '\n>> %s\n' "$1"; }
ok() { printf '   OK  %s\n' "$1"; }
warn() { printf '   WARN %s\n' "$1"; }
err() { printf '   ERR  %s\n' "$1"; }

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
for cmd in newapi-cli supabase bun openssl; do
  if ! cmd_path="$(command -v "$cmd")"; then
    err "未找到 $cmd；先运行 bash scripts/check-env.sh --install，或手动安装后重试"
    exit 1
  fi
  ok "$cmd: $cmd_path"
done

# --- 2. newapi-cli auth status ---
step 'newapi-cli auth status'
if ! auth_status_json="$(newapi-cli auth status 2>/dev/null)"; then
  err '未登录：请先运行 newapi-cli auth login'
  exit 1
fi
if [ "$(json_field "$auth_status_json" '.expired // false' '"true" if d.get("expired") else "false"')" = 'true' ]; then
  err 'newapi-cli session expired'; exit 1
fi

# --- 3. Get self user + OAuth apps ---
step '获取 new-api 用户与 OAuth 应用'
if ! self_json="$(newapi-cli api GET /api/user/self 2>/dev/null)"; then
  err '读取 /api/user/self 失败'
  exit 1
fi
if ! apps_json="$(newapi-cli api GET /api/oauth-apps 2>/dev/null)"; then
  err '读取 /api/oauth-apps 失败'
  exit 1
fi

self_username="$(json_field "$self_json" '.username // ""' 'd.get("username") or ""')"
self_id="$(json_field "$self_json" '.id // ""' 'd.get("id", "")')"
self_role="$(json_field "$self_json" '.role // ""' 'd.get("role", "")')"
if [[ ! "$self_id" =~ ^[1-9][0-9]*$ ]]; then
  err '无法解析当前用户 id'
  exit 1
fi
ok "User id=$self_id"

# --- 4. Pick the OAuth app (reuse) ---
export NEWAPI_APP_CLIENT_ID="$client_id_override"
candidates="$(json_field "$apps_json" \
  '[.[] | select(if env.NEWAPI_APP_CLIENT_ID != "" then .client_id == env.NEWAPI_APP_CLIENT_ID else .status == 1 and .public_client == true and (.supabase_project_ref // "") != "" end)]' \
  'json.dumps([a for a in d if (a.get("client_id") == os.environ.get("NEWAPI_APP_CLIENT_ID") if os.environ.get("NEWAPI_APP_CLIENT_ID") else a.get("status") == 1 and a.get("public_client") is True and a.get("supabase_project_ref"))])')"
unset NEWAPI_APP_CLIENT_ID
if [ "$(json_field "$candidates" 'length' 'len(d)')" != '1' ]; then
  err 'Select exactly one current OAuth app using --client-id'; exit 1
fi
app_valid="$(json_field "$candidates" \
  '.[0] | .status == 1 and .public_client == true and (["openid","profile","email","api:read","api:write"] - .scopes | length == 0)' \
  '"true" if d[0].get("status") == 1 and d[0].get("public_client") is True and set(["openid","profile","email","api:read","api:write"]).issubset(d[0].get("scopes") or []) else "false"')"
selected_client_id="$(json_field "$candidates" '.[0].client_id' 'd[0].get("client_id") or ""')"
supabase_project_ref="$(json_field "$candidates" '.[0].supabase_project_ref' 'd[0].get("supabase_project_ref") or ""')"
if [ "$app_valid" != 'true' ] || [[ ! "$selected_client_id" =~ ^[A-Za-z0-9_-]+$ ]] || [[ ! "$supabase_project_ref" =~ ^[a-z0-9]{20}$ ]]; then
  err 'Selected app must be active, public, have required scopes and a valid project ref'; exit 1
fi
server_url="$(json_field "$auth_status_json" '.server // ""' 'd.get("server") or ""')"
if [[ ! "$server_url" =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[-A-Za-z0-9._~/]*)?$ ]]; then
  err 'Gateway must be an HTTPS URL without credentials, query, or fragment'; exit 1
fi

# Read dotenv as data, never source credentials as shell code.
env_value() {
  local name="$1" line value='' count=0 pattern="^[[:space:]]*(export[[:space:]]+)?$1([[:space:]]|=|$)"
  if [ -f .env.local ]; then
    while IFS= read -r line || [ -n "$line" ]; do
      line="${line%$'\r'}"
      if [[ "$line" =~ $pattern ]]; then
        if [[ ! "$line" =~ ^[[:space:]]*(export[[:space:]]+)?${name}[[:space:]]*=(.*)$ ]]; then
          err "Malformed $name declaration (value suppressed)" >&2; return 1
        fi
        value="${BASH_REMATCH[2]}"
        count=$((count + 1))
      fi
    done < .env.local
  fi
  if [ "$count" -gt 1 ]; then err "Duplicate $name declaration (values suppressed)" >&2; return 1; fi
  if [ "$count" -eq 1 ]; then printf '%s' "$value"; else return 3; fi
}
for name in NEXT_PUBLIC_SUPABASE_URL SUPABASE_URL NEXT_PUBLIC_GATEWAY_CLIENT_ID GATEWAY_CLIENT_ID NEXT_PUBLIC_GATEWAY_BASE_URL GATEWAY_BASE_URL GATEWAY_PUBLIC_URL; do
  case "$name" in
    *SUPABASE_URL) expected="https://$supabase_project_ref.supabase.co" ;;
    *CLIENT_ID) expected="$selected_client_id" ;;
    *) expected="$server_url" ;;
  esac
  if previous="$(env_value "$name")"; then
    [ "$previous" = "$expected" ] || { err "Existing $name binding differs; use another working copy"; exit 1; }
  else
    [ "$?" -eq 3 ] || exit 1
  fi
  if previous="$(printenv "$name")"; then
    [ "$previous" = "$expected" ] || { err "Environment $name binding differs; aborting"; exit 1; }
  fi
done
if [ -f supabase/.temp/project-ref ]; then
  prior_ref="$(<supabase/.temp/project-ref)"
  [ "${prior_ref%$'\r'}" = "$supabase_project_ref" ] || { err 'Working copy belongs to another Supabase project'; exit 1; }
fi
key_present=false
vault_key=''
if vault_key="$(env_value GATEWAY_TOKEN_ENCRYPTION_KEY)"; then key_present=true;
else [ "$?" -eq 3 ] || exit 1; fi
if process_key="$(printenv GATEWAY_TOKEN_ENCRYPTION_KEY)"; then
  if [ "$key_present" = true ] && [ "$vault_key" != "$process_key" ]; then
    err 'Encryption key contexts differ (values suppressed)'; exit 1
  fi
  vault_key="$process_key"
  key_present=true
fi
if [ "$key_present" = true ]; then
  key_length="$(printf '%s' "$vault_key" | openssl base64 -d -A 2>/dev/null | wc -c)"
  canonical_key="$(printf '%s' "$vault_key" | openssl base64 -d -A 2>/dev/null | openssl base64 -A)"
  if [ "$key_length" -ne 32 ] || [ "$canonical_key" != "$vault_key" ]; then
    err 'Existing encryption key is malformed (value suppressed)'; exit 1
  fi
  for name in NEXT_PUBLIC_SUPABASE_URL NEXT_PUBLIC_GATEWAY_CLIENT_ID GATEWAY_PUBLIC_URL; do
    if ! env_value "$name" >/dev/null && ! printenv "$name" >/dev/null; then
      err 'Existing encryption key has no complete deployment binding'; exit 1
    fi
  done
else
  vault_key="$(openssl rand -base64 32 2>/dev/null)"
fi

# --- 5. Supabase link ---
step "supabase link --project-ref $supabase_project_ref"
if ! supabase link --project-ref "$supabase_project_ref" >/dev/null 2>&1; then
  err 'supabase link 失败'
  exit 1
fi

# --- 6. Supabase API keys ---
step '获取 Supabase API keys'
if ! keys_text="$(supabase projects api-keys "--remote=$supabase_project_ref" --reveal 2>/dev/null)"; then
  err '获取 Supabase API keys 失败；请先运行 supabase login'
  exit 1
fi

anon_key=''
service_role_key=''
while IFS= read -r line; do
  if [[ "$line" =~ ^[[:space:]]*\|?[[:space:]]*(anon|service_role)[[:space:]]*\|[[:space:]]*(eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)[[:space:]]*\|?[[:space:]]*$ ]]; then
    case "${BASH_REMATCH[1]}" in
      anon) anon_key="${BASH_REMATCH[2]}" ;;
      service_role) service_role_key="${BASH_REMATCH[2]}" ;;
    esac
  fi
done <<< "$keys_text"
if [ -z "$anon_key" ] || [ -z "$service_role_key" ]; then
  err '未能解析 anon / service_role key；请确认已 supabase login 且项目已 link'
  exit 1
fi

ok 'Supabase keys resolved (values suppressed)'

# --- 7. Write .env.local ---
step '写入 .env.local'
umask 077
app_origin='http://localhost:3000'
if previous="$(env_value APP_ORIGIN)"; then app_origin="$previous";
else [ "$?" -eq 3 ] || exit 1; fi
env_tmp="$(mktemp .env.local.XXXXXX)"
trap 'rm -f "$env_tmp"' EXIT
{
  if [ -f .env.local ]; then
    while IFS= read -r line || [ -n "$line" ]; do
      if [[ ! "$line" =~ ^[[:space:]]*(export[[:space:]]+)?(NEXT_PUBLIC_SUPABASE_URL|NEXT_PUBLIC_SUPABASE_ANON_KEY|SUPABASE_SECRET_KEY|GATEWAY_TOKEN_ENCRYPTION_KEY|NEXT_PUBLIC_GATEWAY_BASE_URL|GATEWAY_PUBLIC_URL|NEXT_PUBLIC_GATEWAY_CLIENT_ID|APP_ORIGIN|INITIAL_ADMIN_SUB)[[:space:]]*= ]]; then
        printf '%s\n' "$line"
      fi
    done < .env.local
  else printf '# auto-generated by scripts/bootstrap.sh\n'; fi
  printf 'NEXT_PUBLIC_SUPABASE_URL=https://%s.supabase.co\n' "$supabase_project_ref"
  printf 'NEXT_PUBLIC_SUPABASE_ANON_KEY=%s\n' "$anon_key"
  printf 'SUPABASE_SECRET_KEY=%s\n' "$service_role_key"
  printf 'GATEWAY_TOKEN_ENCRYPTION_KEY=%s\n' "$vault_key"
  printf 'NEXT_PUBLIC_GATEWAY_BASE_URL=%s\n' "$server_url"
  printf 'GATEWAY_PUBLIC_URL=%s\n' "$server_url"
  printf 'NEXT_PUBLIC_GATEWAY_CLIENT_ID=%s\n' "$selected_client_id"
  printf 'APP_ORIGIN=%s\n' "$app_origin"
  printf 'INITIAL_ADMIN_SUB=%s\n' "$self_id"
} > "$env_tmp"
mv "$env_tmp" .env.local
chmod 600 .env.local
ok '.env.local 已写入（权限 600）'

# --- 8. db push ---
step 'supabase db push'
if ! supabase db push "--remote=$supabase_project_ref" --yes >/dev/null 2>&1; then
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
  provider_matches="$(json_field "$providers_json" '[.providers[] | select(.identifier == "custom:newapi")]' 'json.dumps([p for p in d["providers"] if p.get("identifier") == "custom:newapi"])')"
  provider_count="$(json_field "$provider_matches" 'length' 'len(d)')"
  case "$provider_count" in 0|1) ;; *) err 'Invalid or ambiguous provider list'; exit 1 ;; esac
  provider_info=''
  if [ "$provider_count" = 1 ]; then
    provider_info='present'
    provider_issuer="$(json_field "$provider_matches" '.[0].issuer' 'd[0].get("issuer") or ""')"
    provider_client="$(json_field "$provider_matches" '.[0].client_id' 'd[0].get("client_id") or ""')"
    provider_valid="$(json_field "$provider_matches" \
      '.[0] | .pkce_enabled == true and .email_optional == true and (["openid","profile","email","api:read","api:write"] - .scopes | length == 0)' \
      '"true" if d[0].get("pkce_enabled") is True and d[0].get("email_optional") is True and set(["openid","profile","email","api:read","api:write"]).issubset(d[0].get("scopes") or []) else "false"')"
    ok 'custom:newapi exists'
    if [ "$provider_issuer" != "$server_url" ] || [ "$provider_client" != "$selected_client_id" ] || [ "$provider_valid" != true ]; then
      warn 'provider 配置与网关不一致，删除后重建'
      if curl -fsS --max-time 15 -X DELETE \
        -H "apikey: $service_role_key" \
        -H "Authorization: Bearer $service_role_key" \
        "$custom_providers_url/custom:newapi" -o /dev/null 2>/dev/null; then
        provider_info=''
      else
        err 'provider 删除失败'; exit 1
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
      -d "$provider_body" -o /dev/null "$custom_providers_url" 2>/dev/null; then
      ok 'provider 已创建'
    else
      err 'provider 创建失败'; exit 1
    fi
  fi
else
  err 'provider 检查/创建失败：无法读取 provider 列表（确认 service_role key 与项目 ref）'; exit 1
fi

# --- 10. Authorize endpoint smoke test ---
step 'authorize 端点冒烟测试'
authorize_url="https://$supabase_project_ref.supabase.co/auth/v1/authorize?provider=custom:newapi&redirect_to=http://localhost:3000/auth/callback"
authorize_status="$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 \
  -H "apikey: $anon_key" \
  "$authorize_url" 2>/dev/null || true)"
case "$authorize_status" in
  302|303|307|308) ok "authorize -> $authorize_status" ;;
  *) err 'Authorize test failed (details suppressed)'; exit 1 ;;
esac

# --- 11. Admin bootstrap ---
step 'Admin bootstrap: resolve trusted provider identity'
identity_filter="FROM auth.identities AS i
JOIN auth.users AS u ON u.id = i.user_id
WHERE i.provider = 'custom:newapi'
  AND i.identity_data->>'sub' = '$self_id'
  AND pg_catalog.jsonb_typeof(i.identity_data->'aud') = 'array'
  AND pg_catalog.jsonb_typeof(i.identity_data->'aud'->0) = 'string'
  AND i.identity_data->'aud'->>0 = '$selected_client_id'
  AND (SELECT count(*) FROM auth.identities AS linked
       WHERE linked.user_id = u.id AND linked.provider = 'custom:newapi') = 1"
uuid_query="SELECT u.id::text $identity_filter;"
if ! uuid_result="$(supabase db query "--remote=$supabase_project_ref" --read-only "$uuid_query" 2>/dev/null)"; then
  err 'Identity query failed (output suppressed)'; exit 1
fi
uuid_pattern='[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}'
uuid_count=0
admin_uuid=''
while [[ "$uuid_result" =~ $uuid_pattern ]]; do
  admin_uuid="${BASH_REMATCH[0]}"
  uuid_count=$((uuid_count + 1))
  uuid_result="${uuid_result#*"$admin_uuid"}"
done
if [ "$uuid_count" -ne 1 ]; then
  err 'Expected exactly one trusted identity; log in first or resolve ambiguity'; exit 1
fi
admin_insert="DO \$bootstrap\$
DECLARE resolved_user uuid;
BEGIN
  SELECT u.id INTO STRICT resolved_user $identity_filter;
  IF resolved_user <> '$admin_uuid'::uuid THEN
    RAISE EXCEPTION 'Bootstrap identity changed';
  END IF;
  INSERT INTO public.app_admins (client_id, user_id, role, created_at)
  VALUES ('$selected_client_id', resolved_user, 'owner', now())
  ON CONFLICT (client_id, user_id) DO NOTHING;
END;
\$bootstrap\$;"
if ! supabase db query "--remote=$supabase_project_ref" --yes "$admin_insert" >/dev/null 2>&1; then
  err 'Owner grant failed (output suppressed)'; exit 1
fi
ok 'App-scoped owner bootstrap applied'

# --- 12. Asset seed (optional) ---
if [ -n "$pack_root" ]; then
  step "资产导入：bun run scripts/seed-assets.ts --pack-root $pack_root --client-id $selected_client_id"
  if ! bun run scripts/seed-assets.ts --pack-root "$pack_root" --client-id "$selected_client_id" >/dev/null 2>&1; then
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
