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
    [string]$Source = "",                                    # winify.exe path or URL
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
    Start-Process powershell -Verb RunAs -ArgumentList $a
    Write-Host "Elevation requested. Approve the UAC prompt to continue."
    return
}

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$root   = Join-Path $env:ProgramData "winify"
$binDir = Join-Path $root "bin"
$tools  = Join-Path $root "tools"
New-Item -ItemType Directory -Force -Path $binDir, $tools, (Join-Path $root "data") | Out-Null
$exe = Join-Path $binDir "winify.exe"

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
    Write-Host "Downloading winify.exe..."
    Invoke-WebRequest -Uri $Source -OutFile $exe -UseBasicParsing
} else {
    Copy-Item -LiteralPath $Source -Destination $exe -Force
}

# --- nssm.exe (public domain, pinned build) ---
$nssm = Join-Path $tools "nssm.exe"
if (-not (Test-Path $nssm)) {
    Write-Host "Fetching nssm.exe..."
    $zip = Join-Path $env:TEMP "nssm.zip"
    $tmp = Join-Path $env:TEMP "nssm-install"
    Invoke-WebRequest -Uri "https://nssm.cc/ci/nssm-2.24-101-g897c7ad.zip" -OutFile $zip -UseBasicParsing
    if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $found = Get-ChildItem $tmp -Recurse -Filter nssm.exe | Where-Object { $_.FullName -match 'win64' } | Select-Object -First 1
    if (-not $found) { $found = Get-ChildItem $tmp -Recurse -Filter nssm.exe | Select-Object -First 1 }
    Copy-Item $found.FullName $nssm -Force
    Remove-Item -Recurse -Force $tmp, $zip -ErrorAction SilentlyContinue
}

# --- minimal config (absolute paths: a service's working directory is System32) ---
if (-not (Test-Path $Config)) {
    New-Item -ItemType Directory -Force -Path (Split-Path $Config) | Out-Null
    $db = Join-Path $root "data\control-center.db"
    $yaml = @"
server:
  addr: "127.0.0.1:$Port"
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
Write-Host ""
Write-Host "First run provisions this host automatically (directories, WinRM, NSSM, Caddy, firewall, local target)."
Write-Host "Open http://localhost:$Port and create the admin account."
