# Sends a signed GitHub-style push webhook to the running control-center to
# trigger a deploy of the local demo project.
param(
    [string]$Sha = "",
    [string]$Ref = "refs/heads/main",
    [string]$Secret = "demo-secret",
    [string]$Project = "proj-docker",
    [string]$Base = "http://127.0.0.1:8090"
)

if (-not $Sha) {
    $Sha = (docker exec cc-target git -C /srv/sample-app rev-parse HEAD).Trim()
}

$body = @{ ref = $Ref; after = $Sha } | ConvertTo-Json -Compress
$hmac = New-Object System.Security.Cryptography.HMACSHA256
$hmac.Key = [Text.Encoding]::UTF8.GetBytes($Secret)
$sig = "sha256=" + (($hmac.ComputeHash([Text.Encoding]::UTF8.GetBytes($body)) | ForEach-Object { $_.ToString("x2") }) -join "")

$tmp = Join-Path $env:TEMP "cc-push.json"
[IO.File]::WriteAllText($tmp, $body)

Write-Host "POST $Base/webhooks/github/$Project  (commit $Sha)"
curl.exe -i -X POST -H "X-GitHub-Event: push" -H "X-Hub-Signature-256: $sig" --data-binary "@$tmp" "$Base/webhooks/github/$Project"
