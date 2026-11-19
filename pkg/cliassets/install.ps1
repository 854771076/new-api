param([Parameter(Mandatory=$true)][string]$Server)
$ErrorActionPreference = 'Stop'
$Server = $Server.TrimEnd('/')
$uri = [Uri]$Server
if ($uri.Scheme -ne 'https' -or $uri.UserInfo -or $uri.Query -or $uri.Fragment) { throw 'An HTTPS server URL is required' }
$arch = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
switch ($arch) { 'x64' { $arch = 'amd64' }; 'arm64' { }; default { throw 'Unsupported architecture' } }
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
  $binary = Join-Path $tempDir 'newapi-cli.exe'
  $response = Invoke-WebRequest -UseBasicParsing -MaximumRedirection 0 -Uri "$Server/api/cli/download/newapi-cli-windows-$arch.exe" -OutFile $binary -PassThru
  $expected = $response.Headers['X-Checksum-Sha256']
  if (-not $expected -or (Get-FileHash -Algorithm SHA256 -LiteralPath $binary).Hash -ne $expected) { throw 'Checksum mismatch' }
  & $binary self-update --run --server $Server
  if ($LASTEXITCODE -ne 0) { throw 'Installation failed' }
  $binDir = Join-Path $HOME '.newapi-cli/bin'
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (($userPath -split ';') -notcontains $binDir) { [Environment]::SetEnvironmentVariable('Path', "$binDir;$userPath", 'User') }
  $env:Path = "$binDir;$env:Path"
  Write-Output 'Installed CLI and both skills. Restart your terminal to refresh PATH.'
} finally { Remove-Item -LiteralPath $tempDir -Recurse -Force }
