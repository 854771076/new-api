# scripts/start-sentinel.ps1
# Bring up the new-api stack with a Redis Sentinel HA cluster on host port 30000.
# Requires: Docker Desktop with WSL2 / Docker Engine 24+; docker compose v2.
#
# Usage (from repo root):
#   pwsh -NoProfile -File scripts/start-sentinel.ps1
#   pwsh -NoProfile -File scripts/start-sentinel.ps1 -Rebuild
#   pwsh -NoProfile -File scripts/start-sentinel.ps1 -Down
#
# Logs are tee'd to logs/compose-sentinel-<timestamp>.log so long compose
# runs do not look silent in the terminal.

[CmdletBinding()]
param(
    [switch]$Rebuild,
    [switch]$Down
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

if (-not (Test-Path "$repoRoot\logs")) {
    New-Item -ItemType Directory -Path "$repoRoot\logs" -Force | Out-Null
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$logFile = Join-Path $repoRoot "logs\compose-sentinel-$stamp.log"

function Tee-Exec {
    param([string]$Title, [scriptblock]$Block)
    Write-Host "==> $Title" -ForegroundColor Cyan
    & $Block 2>&1 | Tee-Object -FilePath $logFile -Append
    if ($LASTEXITCODE -ne 0) {
        throw "$Title failed (exit $LASTEXITCODE). See $logFile"
    }
}

try {
    if ($Down) {
        Tee-Exec "docker compose down (sentinel overlay)" {
            docker compose -f docker-compose.yml -f docker-compose.sentinel.yml down
        }
        exit 0
    }

    if ($Rebuild) {
        Tee-Exec "docker compose build new-api" {
            docker compose -f docker-compose.yml -f docker-compose.sentinel.yml build new-api
        }
    }

    Tee-Exec "docker compose up -d (sentinel overlay)" {
        docker compose -f docker-compose.yml -f docker-compose.sentinel.yml up -d
    }

    Write-Host "`nWaiting for containers to become healthy..." -ForegroundColor Yellow
    Start-Sleep -Seconds 25

    Tee-Exec "container status" {
        docker ps --filter "name=new-api" --filter "name=redis" --filter "name=sentinel" --filter "name=postgres" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
    }

    Tee-Exec "sentinel master dump" {
        docker exec sentinel-1 redis-cli -p 26379 sentinel masters
    }

    $apiOk = $false
    try {
        $body = (Invoke-WebRequest -Uri "http://localhost:30000/api/status" -UseBasicParsing -TimeoutSec 10).Content
        $apiOk = $body -match '"success"\s*:\s*true'
        Write-Host "API status response: $body" -ForegroundColor Green
    } catch {
        Write-Host "API status probe failed: $_" -ForegroundColor Red
    }

    if ($apiOk) {
        Write-Host "`n[new-api sentinel stack] up on http://localhost:30000" -ForegroundColor Green
        exit 0
    }
    throw "new-api did not report success on /api/status"
} catch {
    Write-Host "`n[start-sentinel.ps1] FAILED: $_" -ForegroundColor Red
    Write-Host "Inspect logs: docker logs new-api ; cat $logFile" -ForegroundColor Red
    exit 1
}