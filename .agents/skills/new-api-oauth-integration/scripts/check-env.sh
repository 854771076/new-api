#!/usr/bin/env bash
# new-api OAuth 集成环境自检（macOS / Linux）。
#
# 检查依赖 CLI、必需环境变量、网关 discovery 的 issuer、Supabase
# custom provider（custom:newapi）以及 Supabase authorize 是否返回 302。
# 任一项失败时退出码为 1。
#
# 用法：
#   GATEWAY_BASE_URL=http://localhost:3000 \
#   GATEWAY_PUBLIC_URL=https://demo.trycloudflare.com \
#   SUPABASE_URL=https://abc.supabase.co \
#   SUPABASE_SECRET_KEY=... \
#   ./scripts/check-env.sh
set -uo pipefail

GATEWAY_BASE_URL="${GATEWAY_BASE_URL:-}"
GATEWAY_PUBLIC_URL="${GATEWAY_PUBLIC_URL:-$GATEWAY_BASE_URL}"
SUPABASE_URL="${SUPABASE_URL:-}"
SUPABASE_SECRET_KEY="${SUPABASE_SECRET_KEY:-}"

failed=0
pass() { printf '[PASS] %s\n' "$1"; }
fail() {
  failed=$((failed + 1))
  printf '[FAIL] %s\n' "$1"
  [ -n "${2:-}" ] && printf '       -> %s\n' "$2"
}

check_cli() {
  local name="$1" version_args="$2" hint="$3"
  if ! command -v "$name" >/dev/null 2>&1; then
    fail "$name 未安装或不在 PATH" "$hint"
    return
  fi
  local version
  version="$($name $version_args 2>&1 | head -n 1)"
  pass "$name 可用：$version"
}

echo "== 1. 依赖 CLI =="
check_cli bun --version 'curl -fsSL https://bun.sh/install | bash'
check_cli go version 'https://go.dev/dl/'
check_cli supabase --version 'brew install supabase/tap/supabase'
check_cli cloudflared --version 'brew install cloudflared'
check_cli newapi-cli --version '安装配套 newapi-cli，并确认在 PATH 中'

echo
echo "== 2. 环境变量 =="
missing=()
[ -z "$GATEWAY_BASE_URL" ] && missing+=('GATEWAY_BASE_URL')
[ -z "$SUPABASE_URL" ] && missing+=('SUPABASE_URL')
[ -z "$SUPABASE_SECRET_KEY" ] && missing+=('SUPABASE_SECRET_KEY')
if [ ${#missing[@]} -gt 0 ]; then
  fail "缺少环境变量：${missing[*]}" '参考 .env.example 导出后重试'
else
  pass "GATEWAY_BASE_URL=$GATEWAY_BASE_URL"
  pass "GATEWAY_PUBLIC_URL=$GATEWAY_PUBLIC_URL"
  pass "SUPABASE_URL=$SUPABASE_URL"
  pass "SUPABASE_SECRET_KEY=已设置"
fi

if [ -n "$GATEWAY_BASE_URL" ]; then
  echo
  echo "== 3. 网关 discovery issuer =="
  discovery="$GATEWAY_BASE_URL/.well-known/openid-configuration"
  issuer="$(curl -fsS --max-time 15 "${discovery%/}" 2>/dev/null \
    | sed -n 's/.*"issuer"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  if [ -z "$issuer" ]; then
    fail '无法读取 discovery' '确认网关已启动且地址可访问'
  elif [ "$issuer" != "$GATEWAY_PUBLIC_URL" ]; then
    fail "discovery.issuer=$issuer 与 GATEWAY_PUBLIC_URL=$GATEWAY_PUBLIC_URL 不一致" \
      '把网关后台「服务器地址」改成对外地址并重启网关'
  else
    pass "discovery.issuer = $issuer"
  fi
fi

if [ -n "$SUPABASE_URL" ] && [ -n "$SUPABASE_SECRET_KEY" ]; then
  echo
  echo "== 4. Supabase custom provider =="
  providers="$(curl -fsS --max-time 15 \
    -H "apikey: $SUPABASE_SECRET_KEY" \
    -H "Authorization: Bearer $SUPABASE_SECRET_KEY" \
    "$SUPABASE_URL/auth/v1/admin/custom-providers" 2>/dev/null)"
  if [ -z "$providers" ]; then
    fail '无法读取 provider 列表' '确认 SUPABASE_SECRET_KEY 是 secret/service_role key'
  else
    # 以 identifier 为锚点截取对象片段，避免依赖 jq。
    block="$(printf '%s' "$providers" | tr -d '\n' | grep -o '{[^{}]*"identifier"[^{}]*}' | grep '"custom:newapi"' | head -n 1)"
    if [ -z "$block" ]; then
      fail '未找到 identifier=custom:newapi 的 provider' \
        '标识必须带 custom: 前缀，用 POST /auth/v1/admin/custom-providers 创建'
    else
      provider_issuer="$(printf '%s' "$block" | sed -n 's/.*"issuer"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
      if [ "$provider_issuer" != "$GATEWAY_PUBLIC_URL" ]; then
        fail "provider.issuer=$provider_issuer 与 GATEWAY_PUBLIC_URL=$GATEWAY_PUBLIC_URL 不一致" \
          '更新 provider 的 issuer（PATCH /auth/v1/admin/custom-providers/{id}）'
      else
        pass "provider custom:newapi 存在，issuer=$provider_issuer"
      fi
    fi
  fi

  echo
  echo "== 5. Supabase authorize 响应 =="
  status="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 \
    "$SUPABASE_URL/auth/v1/authorize?provider=custom:newapi" 2>/dev/null)"
  case "$status" in
    302|303|307) pass "authorize 返回 $status" ;;
    *) fail "authorize 返回 ${status:-无响应}（期望 302）" '检查 provider 是否已创建且 issuer 可达' ;;
  esac
fi

echo
if [ "$failed" -gt 0 ]; then
  printf '自检失败：%d 项未通过。\n' "$failed"
  exit 1
fi
echo '自检通过。'
exit 0
