#Requires -Version 5.1
<#
.SYNOPSIS
  new-api OAuth 集成环境自检（Windows / PowerShell）。

.DESCRIPTION
  检查依赖 CLI、必需环境变量、网关 discovery 的 issuer、Supabase custom
  provider（custom:newapi）以及 Supabase authorize 是否返回 302。
  加 -Install 时，缺失的 CLI 会按平台尝试自动安装并复验，最后输出安装汇总。
  任一项检查失败时退出码为 1。

  安全提示：-Install 会调用 winget / brew / npm -g / curl | bash 安装或升级
  全局 CLI，从而修改本机全局环境。请确认来源可信后再执行；不加 -Install 时
  只做只读检查，不安装任何东西。

.PARAMETER Install
  尝试自动安装缺失的 CLI（bun、supabase、cloudflared、vercel）。会修改全局环境。

.PARAMETER GatewayBaseUrl
  网关本机/内网地址，例如 http://localhost:3000。默认取 GATEWAY_BASE_URL。

.PARAMETER GatewayPublicUrl
  网关对外地址（必须与 Supabase provider 的 issuer 完全一致）。
  默认取 GATEWAY_PUBLIC_URL，为空时回退 GATEWAY_BASE_URL。

.PARAMETER SupabaseUrl
  Supabase 项目地址，例如 https://xxxx.supabase.co。默认取 SUPABASE_URL。

.PARAMETER SupabaseSecretKey
  Supabase secret / service_role key，仅用于读取 provider 列表。
  默认取 SUPABASE_SECRET_KEY。

.EXAMPLE
  ./scripts/check-env.ps1 -GatewayBaseUrl http://localhost:3000 `
    -GatewayPublicUrl https://demo.trycloudflare.com `
    -SupabaseUrl https://abc.supabase.co -SupabaseSecretKey $env:SUPABASE_SECRET_KEY

.EXAMPLE
  # 只检查；若发现 CLI 缺失则尝试自动安装（会修改全局环境）
  ./scripts/check-env.ps1 -Install
#>
[CmdletBinding()]
param(
  [string]$GatewayBaseUrl = $env:GATEWAY_BASE_URL,
  [string]$GatewayPublicUrl = $env:GATEWAY_PUBLIC_URL,
  [string]$SupabaseUrl = $env:SUPABASE_URL,
  [string]$SupabaseSecretKey = $env:SUPABASE_SECRET_KEY,
  [switch]$Install
)

$ErrorActionPreference = 'Continue'
$script:failed = 0
$script:installOk = 0
$script:installSkip = 0
$script:installFail = 0

function Write-Pass([string]$message) {
  Write-Host "[PASS] $message" -ForegroundColor Green
}

function Write-Fail([string]$message, [string]$hint) {
  $script:failed++
  Write-Host "[FAIL] $message" -ForegroundColor Red
  if ($hint) { Write-Host "       -> $hint" -ForegroundColor Yellow }
}

function Get-Platform {
  if ($env:OS -eq 'Windows_NT') { return 'windows' }
  if ($PSVersionTable.PSVersion.Major -ge 6) {
    if ($IsMacOS) { return 'macos' }
    if ($IsLinux) { return 'linux' }
  }
  return 'unknown'
}

function Get-CliVersion([string]$name, [string]$versionArgs) {
  if (-not (Get-Command $name -ErrorAction SilentlyContinue)) { return $null }
  $output = & $name $versionArgs 2>&1
  $exitCode = $LASTEXITCODE
  $version = @($output) | Select-Object -First 1
  if ($exitCode -ne 0 -or -not $version) { return $null }
  return "$version"
}

function Check-Cli([string]$name, [string]$versionArgs, [string]$hint, [scriptblock]$installer) {
  $version = Get-CliVersion $name $versionArgs
  if ($version) {
    Write-Pass "$name 可用：$version"
    return
  }

  if (-not $Install) {
    Write-Fail "$name 未安装或版本检查失败" $hint
    return
  }

  Write-Host "       -> $name 缺失，尝试自动安装..." -ForegroundColor Cyan
  if (-not $installer) {
    Write-Host "       -> 无法自动安装；获取方式：$hint" -ForegroundColor Yellow
    $script:installSkip++
    Write-Fail "$name 仍缺失" $hint
    return
  }

  $installed = $false
  try {
    & $installer
    $installed = ($LASTEXITCODE -eq 0)
  } catch {
    Write-Host "       -> 安装命令异常：$($_.Exception.Message)" -ForegroundColor Yellow
  }

  if (-not $installed) {
    Write-Host "       -> 安装失败；获取方式：$hint" -ForegroundColor Yellow
    $script:installFail++
    Write-Fail "$name 仍缺失" $hint
    return
  }

  $version = Get-CliVersion $name $versionArgs
  if ($version) {
    $script:installOk++
    Write-Pass "$name 安装成功：$version"
  } else {
    $script:installFail++
    Write-Fail "$name 安装后仍不可用（可能需要重开终端刷新 PATH）" $hint
  }
}

Write-Host "== 1. 依赖 CLI =="
if ($Install) {
  Write-Host "   已开启 -Install：缺失项将尝试自动安装，会修改本机全局环境。" -ForegroundColor Yellow
}

$platform = Get-Platform

# 通用 npm 安装：supabase 与 vercel 一起装，避免重复执行。
$installFromNpm = {
  if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
    Write-Host '       -> 未找到 npm，请先安装 Node.js：https://nodejs.org/' -ForegroundColor Yellow
    $global:LASTEXITCODE = 1
    return
  }
  & npm install -g --yes supabase vercel
}

$installBun = {
  if ($platform -eq 'windows') {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
      Write-Host '       -> 未找到 winget' -ForegroundColor Yellow
      $global:LASTEXITCODE = 1
      return
    }
    & winget install --id Oven-sh.Bun -e --accept-source-agreements --accept-package-agreements
  } elseif ($platform -eq 'macos') {
    & brew install oven-sh/bun/bun
  } elseif ($platform -eq 'linux') {
    & bash -c 'curl -fsSL https://bun.sh/install | bash'
  } else {
    Write-Host '       -> 当前平台不支持自动安装 bun' -ForegroundColor Yellow
    $global:LASTEXITCODE = 1
  }
}

$installCloudflared = {
  if ($platform -eq 'windows') {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
      Write-Host '       -> 未找到 winget' -ForegroundColor Yellow
      $global:LASTEXITCODE = 1
      return
    }
    & winget install --id Cloudflare.cloudflared -e --accept-source-agreements --accept-package-agreements
  } elseif ($platform -eq 'macos') {
    & brew install cloudflared
  } else {
    Write-Host '       -> 请从 Cloudflare 官方下载：https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/' -ForegroundColor Yellow
    $global:LASTEXITCODE = 1
  }
}

$installSupabase = {
  $done = $false
  if ($platform -eq 'windows' -and (Get-Command winget -ErrorAction SilentlyContinue)) {
    & winget install --id Supabase.CLI -e --accept-source-agreements --accept-package-agreements
    if ($LASTEXITCODE -eq 0) { $done = $true }
  } elseif ($platform -eq 'macos' -and (Get-Command brew -ErrorAction SilentlyContinue)) {
    & brew install supabase/tap/supabase
    if ($LASTEXITCODE -eq 0) { $done = $true }
  }
  if (-not $done) {
    Write-Host '       -> 改用 npm 安装 supabase ...' -ForegroundColor Yellow
    & $installFromNpm
  }
}

Check-Cli 'bun' '--version' 'winget install --id Oven-sh.Bun -e  或  https://bun.sh' $installBun
Check-Cli 'go' 'version' 'winget install GoLang.Go  或  https://go.dev/dl/' $null
Check-Cli 'supabase' '--version' 'winget install Supabase.CLI  或  scoop install supabase' $installSupabase
Check-Cli 'cloudflared' '--version' 'winget install --id Cloudflare.cloudflared -e' $installCloudflared
Check-Cli 'newapi-cli' '--version' '仓库自带 cmd/newapi-cli（go build 生成）或 npm 安装 @aswless_854771076/newapi-cli' $null
Check-Cli 'vercel' '--version' 'npm install -g vercel' $installFromNpm

Write-Host ""
Write-Host "== 2. 环境变量 =="
if (-not $GatewayPublicUrl) { $GatewayPublicUrl = $GatewayBaseUrl }

$missing = @()
if (-not $GatewayBaseUrl) { $missing += 'GATEWAY_BASE_URL' }
if (-not $SupabaseUrl) { $missing += 'SUPABASE_URL' }
if (-not $SupabaseSecretKey) { $missing += 'SUPABASE_SECRET_KEY' }
if ($missing.Count -gt 0) {
  Write-Fail ("缺少环境变量：" + ($missing -join ', ')) '参考 .env.example 或通过参数传入'
} else {
  Write-Pass "GATEWAY_BASE_URL=$GatewayBaseUrl"
  Write-Pass "GATEWAY_PUBLIC_URL=$GatewayPublicUrl"
  Write-Pass "SUPABASE_URL=$SupabaseUrl"
  Write-Pass "SUPABASE_SECRET_KEY=已设置"
}

if ($GatewayBaseUrl) {
  Write-Host ""
  Write-Host "== 3. 网关 discovery issuer =="
  try {
    $discovery = Invoke-RestMethod -Uri "$($GatewayBaseUrl.TrimEnd('/'))/.well-known/openid-configuration" -TimeoutSec 15
    if ($discovery.issuer -eq $GatewayPublicUrl) {
      Write-Pass "discovery.issuer = $($discovery.issuer)"
    } else {
      Write-Fail "discovery.issuer=$($discovery.issuer) 与 GATEWAY_PUBLIC_URL=$GatewayPublicUrl 不一致" `
        '把网关后台「服务器地址」改成对外地址并重启网关'
    }
  } catch {
    Write-Fail "无法读取 discovery：$($_.Exception.Message)" '确认网关已启动且地址可访问'
  }
}

if ($SupabaseUrl -and $SupabaseSecretKey) {
  Write-Host ""
  Write-Host "== 4. Supabase custom provider =="
  $headers = @{
    apikey        = $SupabaseSecretKey
    Authorization = "Bearer $SupabaseSecretKey"
  }
  try {
    $response = Invoke-RestMethod -Uri "$($SupabaseUrl.TrimEnd('/'))/auth/v1/admin/custom-providers" `
      -Headers $headers -TimeoutSec 15
    $providers = if ($response.items) { @($response.items) } else { @($response) }
    $provider = $providers | Where-Object { $_.identifier -eq 'custom:newapi' } | Select-Object -First 1
    if (-not $provider) {
      Write-Fail '未找到 identifier=custom:newapi 的 provider' `
        '标识必须带 custom: 前缀，用 POST /auth/v1/admin/custom-providers 创建'
    } elseif ($provider.issuer -ne $GatewayPublicUrl) {
      Write-Fail "provider.issuer=$($provider.issuer) 与 GATEWAY_PUBLIC_URL=$GatewayPublicUrl 不一致" `
        '更新 provider 的 issuer（PATCH /auth/v1/admin/custom-providers/{id}）'
    } else {
      Write-Pass "provider custom:newapi 存在，issuer=$($provider.issuer)"
    }
  } catch {
    Write-Fail "无法读取 provider 列表：$($_.Exception.Message)" `
      '确认 SUPABASE_SECRET_KEY 是 secret/service_role key'
  }

  Write-Host ""
  Write-Host "== 5. Supabase authorize 响应 =="
  $handler = New-Object System.Net.Http.HttpClientHandler
  $handler.AllowAutoRedirect = $false
  $client = New-Object System.Net.Http.HttpClient($handler)
  $client.Timeout = [TimeSpan]::FromSeconds(15)
  try {
    $authorizeUrl = "$($SupabaseUrl.TrimEnd('/'))/auth/v1/authorize?provider=custom:newapi"
    $result = $client.GetAsync($authorizeUrl).GetAwaiter().GetResult()
    $status = [int]$result.StatusCode
    if ($status -eq 302 -or $status -eq 303 -or $status -eq 307) {
      $location = $result.Headers.Location
      Write-Pass "authorize 返回 $status -> $location"
    } else {
      $body = $result.Content.ReadAsStringAsync().GetAwaiter().GetResult()
      Write-Fail "authorize 返回 $status（期望 302）" $body
    }
  } catch {
    Write-Fail "请求 authorize 失败：$($_.Exception.Message)" '确认 Supabase 项目地址可访问'
  } finally {
    $client.Dispose()
    $handler.Dispose()
  }
}

Write-Host ""
if ($Install) {
  Write-Host "== 安装汇总 =="
  Write-Host "安装成功 $($script:installOk) 项 / 跳过 $($script:installSkip) 项 / 失败 $($script:installFail) 项"
  Write-Host ""
}
if ($script:failed -gt 0) {
  Write-Host "自检失败：$($script:failed) 项未通过。" -ForegroundColor Red
  exit 1
}
Write-Host "自检通过。" -ForegroundColor Green
exit 0
