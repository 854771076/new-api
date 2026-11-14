# scripts/start-single.ps1
# Bring up the new-api stack with the original single-node Redis on host port 30000.
# This is the "before" baseline kept for rollback and side-by-side comparison.
#
# Usage (from repo root):
#   pwsh -NoProfile -File scripts/start-single.ps1
#   pwsh -NoProfile -File scripts/start-single.ps1 -Down

[CmdletBinding()]
param(
    [switch]$Down
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

if (-not (Test-Path "$repoRoot\logs")) {
    New-Item -ItemType Directory -Path "$repoRoot\logs" -Force | Out-Null
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$logFile = Join-Path $repoRoot "logs\compose-single-$stamp.log"

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
        Tee-Exec "docker compose down (single-node Redis)" {
            docker compose -f docker-compose.single.yml down
        }
        exit 0
    }

    Tee-Exec "docker compose up -d (single-node Redis)" {
        docker compose -f docker-compose.single.yml up -d
    }

    Write-Host "`nWaiting for containers to become healthy..." -ForegroundColor Yellow
    Start-Sleep -Seconds 20

    Tee-Exec "container status" {
        docker ps --filter "name=new-api" --filter "name=redis" --filter "name=postgres" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"
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
        Write-Host "`n[new-api single-node stack] up on http://localhost:30000" -ForegroundColor Green
        exit 0
    }
    throw "new-api did not report success on /api/status"
} catch {
    Write-Host "`n[start-single.ps1] FAILED: $_" -ForegroundColor Red
    Write-Host "Inspect logs: docker logs new-api ; cat $logFile" -ForegroundColor Red
    exit 1
}