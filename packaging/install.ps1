<#
.SYNOPSIS
  Installs winify as a Windows service on this host and starts it.

.DESCRIPTION
  Self-elevates, places winify.exe under %ProgramData%\winify\bin, writes a
  minimal config, fetches nssm.exe, installs the winify service and starts it.
  On first run the service provisions the host automatically: directories,
  WinRM, NSSM, Caddy, firewall and the local target. Then open the URL and
  create the admin account.

.EXAMPLE
  .\install.ps1
.EXAMPLE
  .\install.ps1 -Source .\winify.exe -Port 8080
#>
param(
    [string]$Source = "",                                    # winify.exe path or HTTPS URL
    [string]$Sha256 = "",                                    # required when Source is a URL
    [string]$SetupToken = "",                                # optional; generated for a new config
    [string]$Config = (Join-Path $env:ProgramData "winify\config.yaml"),
    [string]$ServiceName = "winify",
    [int]$Port = 8080
)
$ErrorActionPreference = "Stop"

# --- self-elevate ---
$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) {
    $a = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', "`"$PSCommandPath`"", '-Config', "`"$Config`"", '-ServiceName', "`"$ServiceName`"", '-Port', $Port)
    if ($Source) { $a += @('-Source', "`"$Source`"") }
    if ($Sha256) { $a += @('-Sha256', "`"$Sha256`"") }
    if ($SetupToken) { $a += @('-SetupToken', "`"$SetupToken`"") }
    Start-Process powershell -Verb RunAs -ArgumentList $a
    Write-Host "Elevation requested. Approve the UAC prompt to continue."
    return
}

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Assert-Hash([string]$Value, [string]$Name) {
    if ($Value -and $Value -notmatch '^[0-9a-fA-F]{64}$') { throw "$Name must be a 64-character SHA-256 hex digest." }
}
Assert-Hash $Sha256 "Sha256"

$root   = Join-Path $env:ProgramData "winify"
$binDir = Join-Path $root "bin"
$tools  = Join-Path $root "tools"
New-Item -ItemType Directory -Force -Path $binDir, $tools, (Join-Path $root "data") | Out-Null
$exe = Join-Path $binDir "winify.exe"
if (-not $SetupToken) {
    $tokenBytes = New-Object byte[] 32
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($tokenBytes) } finally { $rng.Dispose() }
    $SetupToken = "setup_" + ([Convert]::ToBase64String($tokenBytes).TrimEnd('=').Replace('+','-').Replace('/','_'))
}

# --- stop any existing service first so the binary is not locked ---
if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
    Write-Host "Stopping existing $ServiceName service..."
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
    & sc.exe delete $ServiceName | Out-Null
    Start-Sleep -Seconds 1
}

# --- resolve winify.exe ---
if (-not $Source) {
    $local = Join-Path $PSScriptRoot "winify.exe"
    if (Test-Path $local) { $Source = $local }
}
if (-not $Source) { throw "winify.exe not found. Pass -Source <path-or-url>." }
if ($Source -match '^https?://') {
    $uri = [Uri]$Source
    if ($uri.Scheme -ne "https") { throw "Remote winify.exe downloads must use HTTPS." }
    if (-not $Sha256) { throw "A -Sha256 digest is required when downloading winify.exe from a URL." }
    $download = Join-Path $env:TEMP ("winify-" + [Guid]::NewGuid().ToString("N") + ".exe")
    try {
        Write-Host "Downloading winify.exe..."
        Invoke-WebRequest -Uri $uri.AbsoluteUri -OutFile $download -UseBasicParsing
        $actual = (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash
        if ($actual -ine $Sha256) { throw "winify.exe SHA-256 mismatch: expected $Sha256, got $actual" }
        Move-Item -LiteralPath $download -Destination $exe -Force
    } finally {
        Remove-Item -LiteralPath $download -Force -ErrorAction SilentlyContinue
    }
} else {
    Copy-Item -LiteralPath $Source -Destination $exe -Force
    if ($Sha256) {
        $actual = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash
        if ($actual -ine $Sha256) { throw "winify.exe SHA-256 mismatch: expected $Sha256, got $actual" }
    }
}

# --- nssm.exe (public domain, pinned archive) ---
# SHA-256 for nssm-2.24-101-g897c7ad.zip as published by Chocolatey/NSSM.
$nssmArchiveHash = "99F5045FFFBFFB745D67FE3A065A953C4A3D9C253B868892D9B685B0EE7D07B8"
$nssm = Join-Path $tools "nssm.exe"
if (-not (Test-Path $nssm)) {
    Write-Host "Fetching nssm.exe..."
    $zip = Join-Path $env:TEMP "nssm.zip"
    $tmp = Join-Path $env:TEMP "nssm-install"
    Invoke-WebRequest -Uri "https://nssm.cc/ci/nssm-2.24-101-g897c7ad.zip" -OutFile $zip -UseBasicParsing
    $nssmActual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash
    if ($nssmActual -ine $nssmArchiveHash) { throw "NSSM archive SHA-256 mismatch: expected $nssmArchiveHash, got $nssmActual" }
    if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $found = Get-ChildItem $tmp -Recurse -Filter nssm.exe | Where-Object { $_.FullName -match 'win64' } | Select-Object -First 1
    if (-not $found) { $found = Get-ChildItem $tmp -Recurse -Filter nssm.exe | Select-Object -First 1 }
    Copy-Item $found.FullName $nssm -Force
    Remove-Item -Recurse -Force $tmp, $zip -ErrorAction SilentlyContinue
}

# --- minimal config (absolute paths: a service's working directory is System32) ---
$createdConfig = $false
if (-not (Test-Path $Config)) {
    $createdConfig = $true
    New-Item -ItemType Directory -Force -Path (Split-Path $Config) | Out-Null
    $db = Join-Path $root "data\control-center.db"
    $nssmHash = (Get-FileHash -LiteralPath $nssm -Algorithm SHA256).Hash
    $yaml = @"
server:
  addr: "127.0.0.1:$Port"
auth:
  setup_token: '$SetupToken'
database:
  path: '$db'
files:
  servers: '$root\servers.yaml'
  projects: '$root\projects.yaml'
proxy:
  enabled: true
  admin_url: "http://127.0.0.1:2019"
  server_name: "srv0"
deploy:
  nssm_source: '$nssm'
  nssm_sha256: '$nssmHash'
bootstrap:
  service_name: '$ServiceName'
"@
    Set-Content -Path $Config -Value $yaml -Encoding UTF8
    Write-Host "Wrote $Config"
}

# --- install and start the service ---
$bin = '"' + $exe + '" serve -config "' + $Config + '"'
New-Service -Name $ServiceName -BinaryPathName $bin -StartupType Automatic -DisplayName "winify (DevOps Control Center)" | Out-Null
& sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/5000/restart/5000 | Out-Null
Start-Service -Name $ServiceName

Write-Host ""
Write-Host "winify installed and started."
Write-Host "  Service : $ServiceName"
Write-Host "  URL     : http://localhost:$Port"
Write-Host "  Config  : $Config"
if ($createdConfig) { Write-Host "  Setup token (first-run registration): $SetupToken" }
Write-Host ""
Write-Host "First run provisions this host automatically (directories, WinRM, NSSM, Caddy, firewall, local target)."
Write-Host "Open http://localhost:$Port and create the admin account."
