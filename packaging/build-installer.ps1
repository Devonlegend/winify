# Builds winify.exe into packaging\payload and, when Inno Setup is installed,
# compiles the GUI installer (packaging\winify.iss) into packaging\dist.
#
#   powershell -ExecutionPolicy Bypass -File packaging\build-installer.ps1
#
# Without Inno Setup the payload is still produced, so install.ps1 can be run
# directly.
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
$payload = Join-Path $PSScriptRoot "payload"
New-Item -ItemType Directory -Force -Path $payload | Out-Null

Write-Host "==> Building winify.exe"
Push-Location $root
try {
    go build -o (Join-Path $payload "winify.exe") ./cmd/control-center
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
    Pop-Location
}
Copy-Item (Join-Path $PSScriptRoot "install.ps1") (Join-Path $payload "install.ps1") -Force

$iscc = Get-Command iscc.exe -ErrorAction SilentlyContinue
if (-not $iscc) {
    Write-Warning "Inno Setup (iscc.exe) not found; payload is ready at $payload."
    Write-Warning "Install Inno Setup 6 and re-run to produce the .exe installer."
    return
}

Write-Host "==> Compiling installer with Inno Setup"
& $iscc.Source (Join-Path $PSScriptRoot "winify.iss")
if ($LASTEXITCODE -ne 0) { throw "iscc failed" }
Write-Host "Installer written to packaging\dist"
