# Builds a local Linux "target server" (Docker-in-Docker + sshd), creates a
# sample git repo on it, and stores the credentials in the dev database.
# Run from anywhere:  powershell -ExecutionPolicy Bypass -File dev\setup.ps1
$ErrorActionPreference = "Continue"

$dev = $PSScriptRoot
$root = Split-Path -Parent $dev
$targetName = "cc-target"
$imageName = "cc-target:latest"
$hostPort = 18080
$sshPort = 2222

function Fail($msg) { Write-Error $msg; exit 1 }

Write-Host "==> Building the target image (dind + sshd + git + docker compose)"
docker build -t $imageName "$dev\target" 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "docker build failed" }

Write-Host "==> (Re)starting the target container"
$existing = docker ps -aq -f "name=^${targetName}$" 2>$null
if ($existing) { docker rm -f $targetName 2>$null | Out-Null }
docker run -d --name $targetName --privileged -p "${sshPort}:22" -p "${hostPort}:${hostPort}" $imageName | Out-Null
if ($LASTEXITCODE -ne 0) { Fail "docker run failed" }

Write-Host "==> Waiting for dockerd + sshd inside the target"
$ready = $false
for ($i = 0; $i -lt 40; $i++) {
    docker exec $targetName sh -c "docker info >/dev/null 2>&1" 2>$null
    if ($LASTEXITCODE -eq 0) { $ready = $true; break }
    Start-Sleep -Seconds 2
}
if (-not $ready) { Write-Warning "Target not ready; inspect with: docker logs $targetName" }

Write-Host "==> Generating an SSH key and installing it on the target"
$keyDir = Join-Path $dev "keys"
New-Item -ItemType Directory -Force -Path $keyDir | Out-Null
$key = Join-Path $keyDir "id_ed25519"
if (-not (Test-Path $key)) {
    cmd.exe /c "ssh-keygen -t ed25519 -q -N `"`" -f `"$key`"" | Out-Null
}
$pub = (Get-Content "$key.pub" -Raw).Trim()
docker exec $targetName sh -c "mkdir -p /root/.ssh && printf '%s\n' '$pub' > /root/.ssh/authorized_keys && chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys" 2>$null
if ($LASTEXITCODE -ne 0) { Fail "failed to install the SSH key on the target" }

Write-Host "==> Creating the sample app repository on the target"
docker exec $targetName sh -c "rm -rf /srv/sample-app && mkdir -p /srv/sample-app" 2>$null
docker cp "$dev\sample-app\server.py" "${targetName}:/srv/sample-app/server.py" 2>$null
docker cp "$dev\sample-app\Dockerfile" "${targetName}:/srv/sample-app/Dockerfile" 2>$null
docker exec $targetName sh -c "cd /srv/sample-app && git init -q -b main && git config user.email demo@example.com && git config user.name demo && git add -A && git commit -qm init" 2>$null
if ($LASTEXITCODE -ne 0) { Fail "failed to create the sample repo" }
$sha = (docker exec $targetName git -C /srv/sample-app rev-parse HEAD).Trim()

Write-Host "==> Verifying SSH from the host"
$remote = ssh -i $key -p $sshPort -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL -o LogLevel=ERROR root@127.0.0.1 "docker info --format '{{.ServerVersion}}'" 2>&1
if ("$remote" -match "^\d") { Write-Host "    SSH OK, inner Docker $remote" } else { Write-Warning "SSH check output: $remote" }

Write-Host "==> Building control-center and storing credentials in dev/data"
Push-Location $root
try {
    go build -o "$dev\control-center.exe" ./cmd/control-center
    if ($LASTEXITCODE -ne 0) { Fail "go build failed" }
    $cc = "$dev\control-center.exe"
    Get-Content $key -Raw | & $cc cred -config dev/config.yaml add server-docker-ssh
    "demo-secret" | & $cc cred -config dev/config.yaml add demo-webhook
} finally {
    Pop-Location
}

Write-Host ""
Write-Host "Target ready."
Write-Host "  SSH target : root@127.0.0.1:$sshPort"
Write-Host "  App URL    : http://localhost:$hostPort (after a deploy)"
Write-Host "  Commit SHA : $sha"
Write-Host ""
Write-Host "Start the control-center:"
Write-Host "  go run ./cmd/control-center -config dev/config.yaml"
Write-Host ""
Write-Host "Trigger a deploy in another terminal:"
Write-Host "  .\dev\deploy.ps1"
