#!/usr/bin/env bash
# new-api OAuth 集成环境自检（macOS / Linux）。
#
# 检查依赖 CLI、必需环境变量、网关 discovery 的 issuer、Supabase
# custom provider（custom:newapi）以及 Supabase authorize 是否返回 302。
# 加 --install 时，缺失的 CLI 会按平台尝试自动安装并复验，最后输出安装汇总。
# 任一项检查失败时退出码为 1。
#
# 安全提示：--install 会调用 brew / npm -g / curl | bash 安装或升级全局 CLI，
# 从而修改本机全局环境。请确认来源可信后再执行；不带 --install 时只做只读检查。
#
# 用法：
#   GATEWAY_BASE_URL=http://localhost:3000 \
#   GATEWAY_PUBLIC_URL=https://demo.trycloudflare.com \
#   SUPABASE_URL=https://abc.supabase.co \
#   SUPABASE_SECRET_KEY=... \
#   ./scripts/check-env.sh
#
#   # 只检查；若发现 CLI 缺失则尝试自动安装（会修改全局环境）
#   ./scripts/check-env.sh --install
set -uo pipefail

INSTALL=0
if [ "$#" -gt 0 ]; then
  for arg in "$@"; do
    case "$arg" in
      --install) INSTALL=1 ;;
      -h|--help)
        printf '用法：%s [--install]\n' "$0"
        printf '  --install  尝试自动安装缺失的 CLI（会修改全局环境）\n'
        exit 0
        ;;
      *)
        printf '未知参数：%s（可用：--install）\n' "$arg" >&2
        exit 2
        ;;
    esac
  done
fi

GATEWAY_BASE_URL="${GATEWAY_BASE_URL:-}"
GATEWAY_PUBLIC_URL="${GATEWAY_PUBLIC_URL:-$GATEWAY_BASE_URL}"
SUPABASE_URL="${SUPABASE_URL:-}"
SUPABASE_SECRET_KEY="${SUPABASE_SECRET_KEY:-}"

failed=0
install_ok=0
install_skip=0
install_fail=0

pass() { printf '[PASS] %s\n' "$1"; }
fail() {
  failed=$((failed + 1))
  printf '[FAIL] %s\n' "$1"
  [ -n "${2:-}" ] && printf '       -> %s\n' "$2"
}

platform="unknown"
case "$(uname -s 2>/dev/null || echo unknown)" in
  Darwin) platform=macos ;;
  Linux) platform=linux ;;
esac

get_cli_version() {
  local name="$1" version_args="$2" output status version
  command -v "$name" >/dev/null 2>&1 || return 1
  output="$("$name" $version_args 2>&1)"
  status=$?
  version="$(printf '%s\n' "$output" | head -n 1)"
  [ "$status" -eq 0 ] || return 1
  [ -n "$version" ] || return 1
  printf '%s' "$version"
}

# supabase 与 vercel 共用的 npm 安装，避免重复执行。
install_npm_cli() {
  if ! command -v npm >/dev/null 2>&1; then
    printf '       -> 未找到 npm，请先安装 Node.js：https://nodejs.org/\n'
    return 1
  fi
  npm install -g --yes supabase vercel
}

install_bun() {
  case "$platform" in
    macos)
      command -v brew >/dev/null 2>&1 || { printf '       -> 未找到 brew\n'; return 1; }
      brew install oven-sh/bun/bun
      ;;
    linux)
      command -v curl >/dev/null 2>&1 || { printf '       -> 未找到 curl\n'; return 1; }
      curl -fsSL https://bun.sh/install | bash
      ;;
    *)
      printf '       -> 当前平台不支持自动安装 bun\n'
      return 1
      ;;
  esac
}

install_cloudflared() {
  case "$platform" in
    macos)
      command -v brew >/dev/null 2>&1 || { printf '       -> 未找到 brew\n'; return 1; }
      brew install cloudflared
      ;;
    linux)
      printf '       -> 请从 Cloudflare 官方下载：https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/\n'
      return 1
      ;;
    *)
      printf '       -> 当前平台不支持自动安装 cloudflared\n'
      return 1
      ;;
  esac
}

install_supabase() {
  case "$platform" in
    macos)
      if command -v brew >/dev/null 2>&1 && brew install supabase/tap/supabase; then
        return 0
      fi
      ;;
  esac
  printf '       -> 改用 npm 安装 supabase ...\n'
  install_npm_cli
}

check_cli() {
  local name="$1" version_args="$2" hint="$3" installer="${4:-}"
  local version
  if version="$(get_cli_version "$name" "$version_args")" && [ -n "$version" ]; then
    pass "$name 可用：$version"
    return
  fi

  if [ "$INSTALL" -ne 1 ]; then
    fail "$name 未安装或版本检查失败" "$hint"
    return
  fi

  printf '       -> %s 缺失，尝试自动安装...\n' "$name"
  if [ -z "$installer" ]; then
    printf '       -> 无法自动安装；获取方式：%s\n' "$hint"
    install_skip=$((install_skip + 1))
    fail "$name 仍缺失" "$hint"
    return
  fi

  if "$installer"; then
    if version="$(get_cli_version "$name" "$version_args")" && [ -n "$version" ]; then
      install_ok=$((install_ok + 1))
      pass "$name 安装成功：$version"
    else
      install_fail=$((install_fail + 1))
      fail "$name 安装后仍不可用（可能需要重开终端刷新 PATH）" "$hint"
    fi
  else
    printf '       -> 安装 %s 失败；获取方式：%s\n' "$name" "$hint"
    install_fail=$((install_fail + 1))
    fail "$name 仍缺失" "$hint"
  fi
}

echo "== 1. 依赖 CLI =="
if [ "$INSTALL" -eq 1 ]; then
  echo '   已开启 --install：缺失项将尝试自动安装，会修改本机全局环境。'
fi
check_cli bun --version 'curl -fsSL https://bun.sh/install | bash' install_bun
check_cli go version 'https://go.dev/dl/'
check_cli supabase --version 'brew install supabase/tap/supabase' install_supabase
check_cli cloudflared --version 'brew install cloudflared' install_cloudflared
check_cli newapi-cli --version '仓库自带 cmd/newapi-cli（go build 生成）或 npm 安装 @aswless_854771076/newapi-cli'
check_cli vercel --version 'npm install -g vercel' install_npm_cli

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
if [ "$INSTALL" -eq 1 ]; then
  echo '== 安装汇总 =='
  printf '安装成功 %d 项 / 跳过 %d 项 / 失败 %d 项\n' "$install_ok" "$install_skip" "$install_fail"
  echo
fi
if [ "$failed" -gt 0 ]; then
  printf '自检失败：%d 项未通过。\n' "$failed"
  exit 1
fi
echo '自检通过。'
exit 0
