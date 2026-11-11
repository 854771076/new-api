#!/usr/bin/env pwsh
# scripts/bootstrap.ps1 — auto-configure new-api OAuth + Supabase external app
# Depends on: newapi-cli (already logged in), supabase CLI, bun
# Usage: from project root: ./scripts/bootstrap.ps1 -ProjectName "AI Outfit"
# Flow: probe newapi-cli login -> probe existing outfit-demo OAuth app (reuse) ->
#       supabase link -> db push -> custom:newapi provider check/create ->
#       write .env.local -> admin SQL (UUID mapping)

param(
  [string]$ProjectName = "AI Outfit",
  [string]$ClientIdOverride = "",   # manual OAuth app id; empty = reuse outfit-demo
  [string]$PackRoot = ""            # asset pack root; empty = skip seed
)

$ErrorActionPreference = "Stop"
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location (Join-Path $scriptDir "..")

function Write-Step($msg) { Write-Host "`n>> $msg" -ForegroundColor Cyan }
function Write-OK($msg)  { Write-Host "   OK  $msg" -ForegroundColor Green }
function Write-Warn($msg){ Write-Host "   WARN $msg" -ForegroundColor Yellow }
function Write-Err($msg) { Write-Host "   ERR  $msg" -ForegroundColor Red }

function Invoke-BootstrapCli {
  param([string]$Command, [string[]]$Arguments, [switch]$AsJson)
  # Capture both streams: CLI diagnostics can contain credentials. Windows
  # PowerShell represents native stderr as ErrorRecords, even on success.
  $ErrorActionPreference = "Continue"
  try {
    $LASTEXITCODE = $null
    $output = (& $Command @Arguments 2>&1 | Out-String)
    $exitCode = $LASTEXITCODE
  } catch {
    throw "$Command invocation failed (output suppressed)"
  }
  if ($exitCode -ne 0) {
    throw "$Command failed with exit code $exitCode (output suppressed)"
  }
  if ($AsJson) {
    try { return ($output | ConvertFrom-Json -ErrorAction Stop) }
    catch { throw "$Command returned invalid JSON (output suppressed)" }
  }
  return $output
}

# --- 1. Toolchain ---
Write-Step "Check toolchain"
foreach ($cmd in @("newapi-cli", "supabase", "bun")) {
  $found = Get-Command $cmd -ErrorAction SilentlyContinue
  if ($found) { Write-OK "$cmd : $($found.Source)" } else { Write-Err "$cmd not found"; exit 1 }
}

# --- 2. newapi-cli auth status ---
Write-Step "newapi-cli auth status"
$authStatus = Invoke-BootstrapCli -Command "newapi-cli" -Arguments @("auth", "status") -AsJson
if ($authStatus.expired) { Write-Err "Run newapi-cli auth login first"; exit 1 }
$serverUrl = $authStatus.server
$serverUri = $null
if (-not [Uri]::TryCreate($serverUrl, [UriKind]::Absolute, [ref]$serverUri) -or
    $serverUri.Scheme -ne "https" -or $serverUri.UserInfo -or $serverUri.Query -or $serverUri.Fragment) {
  Write-Err "new-api server must be a public HTTPS URL without credentials, query, or fragment"
  exit 1
}

# --- 3. Get self user + OAuth apps ---
Write-Step "Get new-api user + OAuth apps"
$self = Invoke-BootstrapCli -Command "newapi-cli" -Arguments @("api", "GET", "/api/user/self") -AsJson
$apps = @(Invoke-BootstrapCli -Command "newapi-cli" -Arguments @("api", "GET", "/api/oauth-apps") -AsJson)
if ([string]$self.id -cnotmatch '^[1-9][0-9]*$') { Write-Err "Invalid new-api user id"; exit 1 }
Write-OK "User id=$($self.id)"

# Overrides select an app from the current list; they never supply a project ref.
if ($ClientIdOverride) {
  $matches = @($apps | Where-Object { $_.client_id -ceq $ClientIdOverride })
  if ($matches.Count -ne 1) { Write-Err "ClientIdOverride must match exactly one current OAuth app"; exit 1 }
  $pick = $matches[0]
} else {
  $existing = @($apps | Where-Object { $_.status -eq 1 -and $_.public_client -eq $true -and $_.supabase_project_ref })
  if ($existing.Count -ne 1) { Write-Err "Select exactly one active public OAuth app with ClientIdOverride"; exit 1 }
  $pick = $existing[0]
}
$selectedClientId = [string]$pick.client_id
$supabaseProjectRef = [string]$pick.supabase_project_ref
if ($pick.status -ne 1 -or $pick.public_client -ne $true -or
    $selectedClientId -cnotmatch '^[A-Za-z0-9_-]+$' -or $supabaseProjectRef -cnotmatch '^[a-z0-9]{20}$') {
  Write-Err "Selected OAuth app must be active, public, and bound to a valid Supabase project"
  exit 1
}
$requiredScopes = @("openid", "profile", "email", "api:read", "api:write")
foreach ($scope in $requiredScopes) {
  if (@($pick.scopes) -cnotcontains $scope) {
    Write-Err "Selected OAuth app is missing required scope: $scope; update it in new-api first"
    exit 1
  }
}
Write-OK "Reuse OAuth client_id=$selectedClientId"
Write-OK "Bound Supabase project: $supabaseProjectRef"

$existingEnv = ""
if (Test-Path ".env.local") { $existingEnv = Get-Content ".env.local" -Raw }
$bindings = @{
  NEXT_PUBLIC_SUPABASE_URL = "https://$supabaseProjectRef.supabase.co"
  SUPABASE_URL = "https://$supabaseProjectRef.supabase.co"
  NEXT_PUBLIC_GATEWAY_CLIENT_ID = $selectedClientId
  GATEWAY_CLIENT_ID = $selectedClientId
  NEXT_PUBLIC_GATEWAY_BASE_URL = $serverUrl
  GATEWAY_BASE_URL = $serverUrl
  GATEWAY_PUBLIC_URL = $serverUrl
}
foreach ($name in $bindings.Keys) {
  $entries = @([regex]::Matches($existingEnv, "(?m)^\s*(?:export\s+)?${name}\s*=([^\r\n]*)\r?$"))
  if ($entries.Count -gt 1 -or ($entries.Count -eq 1 -and $entries[0].Groups[1].Value -cne $bindings[$name])) {
    throw "Existing $name binding differs or is malformed; use a separate working copy"
  }
  $processValue = [Environment]::GetEnvironmentVariable($name)
  if ($null -ne $processValue -and $processValue -cne $bindings[$name]) { throw "Environment $name binding differs; aborting" }
}
if (Test-Path "supabase/.temp/project-ref") {
  if ((Get-Content "supabase/.temp/project-ref" -Raw).Trim() -cne $supabaseProjectRef) {
    throw "Working copy is linked to another Supabase project; use a separate working copy"
  }
}
$vaultKey = ""
$keyEntries = @([regex]::Matches($existingEnv, '(?m)^\s*(?:export\s+)?GATEWAY_TOKEN_ENCRYPTION_KEY\s*=([^\r\n]*)\r?$'))
$keyDeclarations = @([regex]::Matches($existingEnv, '(?m)^\s*(?:export\s+)?GATEWAY_TOKEN_ENCRYPTION_KEY\b[^\r\n]*'))
if ($keyEntries.Count -gt 1 -or $keyDeclarations.Count -ne $keyEntries.Count) {
  throw "Duplicate or malformed gateway encryption key declaration (values suppressed)"
}
$processKey = [Environment]::GetEnvironmentVariable("GATEWAY_TOKEN_ENCRYPTION_KEY")
if ($keyEntries.Count -eq 1) { $vaultKey = $keyEntries[0].Groups[1].Value }
if ($null -ne $processKey) {
  if ($keyEntries.Count -eq 1 -and $processKey -cne $vaultKey) { throw "Gateway encryption key contexts differ (values suppressed)" }
  $vaultKey = $processKey
}
if ($keyEntries.Count -eq 1 -or $null -ne $processKey) {
  try { $decodedKey = [Convert]::FromBase64String($vaultKey) }
  catch { throw "Existing gateway encryption key is malformed (value suppressed)" }
  if ($decodedKey.Length -ne 32 -or [Convert]::ToBase64String($decodedKey) -cne $vaultKey) {
    throw "Existing gateway encryption key is invalid (value suppressed)"
  }
  foreach ($name in @("NEXT_PUBLIC_SUPABASE_URL", "NEXT_PUBLIC_GATEWAY_CLIENT_ID", "GATEWAY_PUBLIC_URL")) {
    if (-not [regex]::IsMatch($existingEnv, "(?m)^${name}=") -and $null -eq [Environment]::GetEnvironmentVariable($name)) {
      throw "Existing encryption key has no complete deployment binding; aborting"
    }
  }
} else {
  $keyBytes = New-Object byte[] 32
  $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $rng.GetBytes($keyBytes) } finally { $rng.Dispose() }
  $vaultKey = [Convert]::ToBase64String($keyBytes)
}

# --- 4. Supabase link ---
Write-Step "Supabase link --project-ref $supabaseProjectRef"
Invoke-BootstrapCli -Command "supabase" -Arguments @("link", "--project-ref", $supabaseProjectRef) | Out-Null

# --- 5. Supabase API keys ---
Write-Step "Fetch Supabase API keys"
$keysText = Invoke-BootstrapCli -Command "supabase" -Arguments @("projects", "api-keys", "--remote=$supabaseProjectRef", "--reveal")
# Match "anon     | <jwt>" and "service_role | <jwt>" from the column table output.
# Trim leading/trailing pipe + whitespace on each line.
$anonKey = (($keysText -split "`n" | Where-Object { $_ -match '^\s*\|?\s*anon\s+\|' } | Select-Object -First 1) -replace '^\s*\|?\s*anon\s+\|\s*', '' -replace '\s*\|.*', '').Trim()
$serviceRoleKey = (($keysText -split "`n" | Where-Object { $_ -match '^\s*\|?\s*service_role\s+\|' } | Select-Object -First 1) -replace '^\s*\|?\s*service_role\s+\|\s*', '' -replace '\s*\|.*', '').Trim()
if ($anonKey -notmatch '^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$' -or
    $serviceRoleKey -notmatch '^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$') {
  Write-Err "Could not resolve complete anon and service_role keys (output suppressed)"
  exit 1
}
Write-OK "Supabase keys resolved (values suppressed)"

# --- 6. Write .env.local ---
Write-Step "Write .env.local"
$envContent = @"
# auto-generated by scripts/bootstrap.ps1
NEXT_PUBLIC_SUPABASE_URL=https://$supabaseProjectRef.supabase.co
NEXT_PUBLIC_SUPABASE_ANON_KEY=$anonKey
SUPABASE_SECRET_KEY=$serviceRoleKey
GATEWAY_TOKEN_ENCRYPTION_KEY=$vaultKey
NEXT_PUBLIC_GATEWAY_BASE_URL=$serverUrl
GATEWAY_PUBLIC_URL=$serverUrl
NEXT_PUBLIC_GATEWAY_CLIENT_ID=$selectedClientId
APP_ORIGIN=http://localhost:3000
INITIAL_ADMIN_SUB=$($self.id)
"@
if ($existingEnv) {
  foreach ($line in $envContent -split "`n") {
    if ($line -notmatch '^([A-Z_]+)=') { continue }
    $name = $Matches[1]
    if ($name -eq "APP_ORIGIN" -and [regex]::IsMatch($existingEnv, '(?m)^APP_ORIGIN=')) { continue }
    $pattern = "(?m)^\s*(?:export\s+)?${name}\s*=[^\r\n]*\r?$"
    if ([regex]::IsMatch($existingEnv, $pattern)) {
      $existingEnv = [regex]::Replace($existingEnv, $pattern, $line.TrimEnd("`r"))
    } else { $existingEnv += "`n" + $line.TrimEnd("`r") }
  }
  $envContent = $existingEnv
}
Set-Content -Path ".env.local" -Value $envContent -Encoding utf8
Write-OK ".env.local written"

# --- 7. db push ---
Write-Step "supabase db push --remote=$supabaseProjectRef --yes"
Invoke-BootstrapCli -Command "supabase" -Arguments @("db", "push", "--remote=$supabaseProjectRef", "--yes") | Out-Null

# --- 8. Check custom:newapi provider ---
# Note: endpoint is /auth/v1/admin/custom-providers (plural), not /providers
# PUT /auth/v1/admin/config is deprecated (returns 404 in modern Supabase);
# localhost redirects are auto-allowed.
Write-Step "Check Supabase custom:newapi provider"
$customProvidersUrl = "https://$supabaseProjectRef.supabase.co/auth/v1/admin/custom-providers"
$authHeaders = @{
  apikey = $serviceRoleKey
  Authorization = "Bearer $serviceRoleKey"
}
try {
  $providers = (Invoke-RestMethod -Uri $customProvidersUrl -Headers $authHeaders -UseBasicParsing -ErrorAction Stop).providers
  $matchingProviders = @($providers | Where-Object { $_.identifier -ceq "custom:newapi" })
  if ($matchingProviders.Count -gt 1) { throw "Ambiguous custom:newapi providers" }
  $custom = $matchingProviders | Select-Object -First 1
  if ($custom) {
    Write-OK "custom:newapi exists"
    $needRecreate = $custom.issuer -cne $serverUrl -or $custom.client_id -cne $selectedClientId -or
      $custom.pkce_enabled -ne $true -or $custom.email_optional -ne $true
    foreach ($scope in $requiredScopes) {
      if (@($custom.scopes) -cnotcontains $scope) { $needRecreate = $true }
    }
    if ($needRecreate) {
      Write-Warn "provider mismatch; delete + recreate"
      Invoke-RestMethod -Uri "$customProvidersUrl/custom:newapi" -Method DELETE -Headers $authHeaders -UseBasicParsing | Out-Null
      $custom = $null
    }
  }
  if (-not $custom) {
    Write-Step "Create custom:newapi provider"
    $body = @{
      provider_type = "oidc"; identifier = "custom:newapi"; name = "new-api"
      issuer = $serverUrl; client_id = $selectedClientId; client_secret = "ignored-by-public-client"
      scopes = $requiredScopes
      pkce_enabled = $true; email_optional = $true
    } | ConvertTo-Json -Depth 5
    Invoke-RestMethod -Uri $customProvidersUrl -Method POST -Headers ($authHeaders + @{ "Content-Type" = "application/json" }) -Body $body -UseBasicParsing | Out-Null
    Write-OK "provider created"
  }
} catch {
  Write-Err "provider check/create failed (details suppressed to protect credentials)"
  exit 1
}

# --- 9. Authorize endpoint smoke test ---
Write-Step "Authorize endpoint smoke test"
$authUrl = "https://$supabaseProjectRef.supabase.co/auth/v1/authorize?provider=custom:newapi&redirect_to=http://localhost:3000/auth/callback"
$authorizeStatus = $null
try {
  $resp = Invoke-WebRequest -Uri $authUrl -Headers @{ apikey = $anonKey } -MaximumRedirection 0 -UseBasicParsing -ErrorAction Stop
  $authorizeStatus = [int]$resp.StatusCode
} catch {
  if ($_.Exception.Response) { $authorizeStatus = [int]$_.Exception.Response.StatusCode }
}
if ($authorizeStatus -notin @(302, 303, 307, 308)) {
  Write-Err "authorize test failed (response details suppressed)"
  exit 1
}
Write-OK "authorize -> $authorizeStatus"

# --- 10. Admin bootstrap ---
# new-api OAuth sub is an integer user id in provider-owned auth.identities.
# Never trust editable auth.users.raw_user_meta_data for an owner grant.
# app_admins.user_id must be auth.users.id (UUID), scoped by client_id.
Write-Step "Admin bootstrap: resolve auth.users UUID for sub=$($self.id)"
$identityFilter = @"
FROM auth.identities AS i
JOIN auth.users AS u ON u.id = i.user_id
WHERE i.provider = 'custom:newapi'
  AND i.identity_data->>'sub' = '$($self.id)'
  AND pg_catalog.jsonb_typeof(i.identity_data->'aud') = 'array'
  AND pg_catalog.jsonb_typeof(i.identity_data->'aud'->0) = 'string'
  AND i.identity_data->'aud'->>0 = '$selectedClientId'
  AND (SELECT count(*) FROM auth.identities AS linked
       WHERE linked.user_id = u.id AND linked.provider = 'custom:newapi') = 1
"@
$uuidQuery = "SELECT u.id::text $identityFilter;"
$uuidResult = Invoke-BootstrapCli -Command "supabase" -Arguments @("db", "query", "--remote=$supabaseProjectRef", "--read-only", $uuidQuery)
# Do not choose the first row or deduplicate matches: ambiguity must fail closed.
$uuidPattern = '(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b'
$uuidMatches = @([regex]::Matches($uuidResult, $uuidPattern))
if ($uuidMatches.Count -ne 1) {
  Write-Err "Expected exactly one custom:newapi identity/user for this sub and client; log in first or resolve ambiguity"
  exit 1
}
$adminUuid = $uuidMatches[0].Value
# Revalidate the unique identity immediately before the grant; never rely only
# on parsed CLI output. Migration 4 supplies the app-scoped unique constraint.
$adminInsert = @"
DO `$bootstrap`$
DECLARE resolved_user uuid;
BEGIN
  SELECT u.id INTO STRICT resolved_user $identityFilter;
  IF resolved_user <> '$adminUuid'::uuid THEN
    RAISE EXCEPTION 'Bootstrap identity changed';
  END IF;
  INSERT INTO public.app_admins (client_id, user_id, role, created_at)
  VALUES ('$selectedClientId', resolved_user, 'owner', now())
  ON CONFLICT (client_id, user_id) DO NOTHING;
END;
`$bootstrap`$;
"@
Invoke-BootstrapCli -Command "supabase" -Arguments @("db", "query", "--remote=$supabaseProjectRef", "--yes", $adminInsert) | Out-Null
Write-OK "App-scoped admin bootstrap applied"

# --- 11. Asset seed (optional) ---
if ($PackRoot) {
  Write-Step "Asset seed: bun run scripts/seed-assets.ts --pack-root $PackRoot --client-id $selectedClientId"
  Invoke-BootstrapCli -Command "bun" -Arguments @("run", "scripts/seed-assets.ts", "--pack-root", $PackRoot, "--client-id", $selectedClientId) | Out-Null
}

Write-Host "`n*** bootstrap done ***" -ForegroundColor Magenta
Write-Host "  1. bun run dev"
Write-Host "  2. http://localhost:3000/login -> OAuth flow"
Write-Host "  3. http://localhost:3000/admin (need login + admin bootstrap SQL applied)"
Write-Host "  4. Without -PackRoot: bun run scripts/seed-assets.ts --pack-root <path> --client-id $selectedClientId"
