#Requires -Version 5.1
<#
.SYNOPSIS
  new-api OAuth 集成环境自检（Windows / PowerShell）。

.DESCRIPTION
  检查依赖 CLI、必需环境变量、网关 discovery 的 issuer、Supabase custom
  provider（custom:newapi）以及 Supabase authorize 是否返回 302。
  任一项失败时退出码为 1。

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
#>
[CmdletBinding()]
param(
  [string]$GatewayBaseUrl = $env:GATEWAY_BASE_URL,
  [string]$GatewayPublicUrl = $env:GATEWAY_PUBLIC_URL,
  [string]$SupabaseUrl = $env:SUPABASE_URL,
  [string]$SupabaseSecretKey = $env:SUPABASE_SECRET_KEY
)

$ErrorActionPreference = 'Continue'
$script:failed = 0

function Write-Pass([string]$message) {
  Write-Host "[PASS] $message" -ForegroundColor Green
}

function Write-Fail([string]$message, [string]$hint) {
  $script:failed++
  Write-Host "[FAIL] $message" -ForegroundColor Red
  if ($hint) { Write-Host "       -> $hint" -ForegroundColor Yellow }
}

function Test-Cli([string]$name, [string]$versionArgs, [string]$installHint) {
  $command = Get-Command $name -ErrorAction SilentlyContinue
  if (-not $command) {
    Write-Fail "$name 未安装或不在 PATH" $installHint
    return
  }
  $version = & $name $versionArgs 2>&1 | Select-Object -First 1
  Write-Pass "$name 可用：$version"
}

Write-Host "== 1. 依赖 CLI =="
Test-Cli 'bun' '--version' 'winget install Oven-sh.Bun  或  https://bun.sh'
Test-Cli 'go' 'version' 'winget install GoLang.Go'
Test-Cli 'supabase' '--version' 'winget install Supabase.CLI  或  scoop install supabase'
Test-Cli 'cloudflared' '--version' 'winget install Cloudflare.cloudflared'
Test-Cli 'newapi-cli' '--version' '安装配套 newapi-cli，并确认在 PATH 中'

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
if ($script:failed -gt 0) {
  Write-Host "自检失败：$($script:failed) 项未通过。" -ForegroundColor Red
  exit 1
}
Write-Host "自检通过。" -ForegroundColor Green
exit 0
