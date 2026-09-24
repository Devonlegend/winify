# End-to-End Checklist

Follow this top to bottom to take a push all the way to a live HTTPS URL for
**both** a Docker project and an IIS project. Commands are PowerShell on the
control-center host unless noted.

Placeholders: replace `CC_HOST` (e.g. `http://127.0.0.1:8090`), `DOCKER_HOST`
(e.g. `10.0.0.9`), `WIN_HOST` (e.g. `10.0.0.5`), and the domains.

---

## Part 0 — Prerequisites

- [ ] Docker target: reachable over SSH; `git`, `docker`, and the `docker compose`
      plugin installed.
- [ ] Populate `deploy.known_hosts_file` from a trusted host-key source (SSH
      verification is fail-closed).
- [ ] IIS target: WinRM enabled (`winrm quickconfig`), an account that can stop
      services and recycle app pools; `git` and the build tooling installed.
- [ ] A repository with a Dockerfile (Docker) or a publishable web app (IIS).
- [ ] A DNS name for each app pointing at the Caddy host, and ports 80/443
      reachable from the internet (required for Let's Encrypt).

## Part 1 — Control center setup

- [ ] Build and configure:

      go build -o control-center ./cmd/control-center
      Copy-Item config.example.yaml config.yaml

- [ ] In `config.yaml` set `proxy.enabled: true`, `proxy.admin_url` to your Caddy
      admin API, and a listen address that is free.
- [ ] Start Caddy with its admin API reachable from the control-center:

      docker run -d --name caddy --network host caddy:2 caddy run --admin 127.0.0.1:2019

  The Caddy admin API is unauthenticated; keep it on loopback or a protected
  management network. Never publish port 2019 on a public interface.

- [ ] Add the encrypted credentials:

      "ssh-private-key"      | .\control-center cred add server-002-ssh
      "winrm-password"       | .\control-center cred add server-001-winrm
      "webhook-secret-001"   | .\control-center cred add proj-001-webhook
      "webhook-secret-002"   | .\control-center cred add proj-002-webhook

- [ ] Edit `servers.yaml` and `projects.yaml` for your hosts, paths, domains and
      ports (see the "Adding a Project or Server" guide). For Windows targets
      you can instead use **Servers → Onboard a Windows server** to provision
      and register the host from the UI.
- [ ] Start the control-center and sign in. The first-run page requires the
      setup token printed in the startup log; keep the default loopback listener
      until the admin account exists.

      .\control-center serve -config config.yaml

---

## Part 2 — Docker project (push → build → deploy → HTTPS)

- [ ] Push a commit to the project's branch.
- [ ] Simulate the webhook (replace the secret and repo values):

      $secret = "webhook-secret-001"
      $body   = '{"ref":"refs/heads/main","after":"abc1234def"}'
      $h = New-Object System.Security.Cryptography.HMACSHA256
      $h.Key = [Text.Encoding]::UTF8.GetBytes($secret)
      $sig = "sha256=" + (($h.ComputeHash([Text.Encoding]::UTF8.GetBytes($body)) | ForEach-Object { $_.ToString("x2") }) -join "")
      [IO.File]::WriteAllText("$env:TEMP\push.json", $body)
      curl.exe -i -X POST -H "X-GitHub-Event: push" -H "X-Hub-Signature-256: $sig" --data-binary "@$env:TEMP\push.json" $CC_HOST/webhooks/github/proj-001

- [ ] Expect `HTTP/1.1 202` and `{"status":"accepted","deployment_id":N}`.
      Resending the same `X-GitHub-Delivery` value should return `202` with
      `reason=duplicate delivery` and must not start a second deployment.
- [ ] Open the **Deployment** tab: the attempt goes `queued → running → success`
      with a log showing clone, `docker build`, `docker compose up -d`, health
      check, and proxy registration.
- [ ] On the Docker host: `docker ps` shows the running container.
- [ ] Open `https://<docker-domain>` in a browser; it loads over HTTPS with a
      valid certificate.
- [ ] `curl -s -H "Authorization: Bearer $API_TOKEN" $CC_HOST/api/v1/audit | ConvertFrom-Json | Select -First 5` shows the
      clone/build/compose commands with `action=deploy` and the deployment id.

## Part 3 — IIS project (push → validate → backup → deploy → HTTPS)

- [ ] Push a commit to the IIS project's branch.
- [ ] Send the signed webhook as above, but for `proj-002` and the IIS secret.
- [ ] Expect `202`; the log shows: sync repo, build, validate `web.config`,
      stop service/app pool, backup, copy files, start service, recycle app
      pool, smoke test, proxy registration.
- [ ] Open `https://<iis-domain>`; it loads over HTTPS.
- [ ] Confirm a timestamped backup exists on the Windows host under the
      configured `iis_backup_dir`.

---

## Part 4 — Failure paths (must be clear, never silent)

- [ ] **Bad signature:** resend the webhook with `X-Hub-Signature-256: sha256=deadbeef`.
      Expect `401 Unauthorized` and no deployment started.
- [ ] **Wrong branch:** send `{"ref":"refs/heads/dev",...}` with a valid
      signature. Expect `202` with `"reason":"branch mismatch"` and no deploy.
- [ ] **Unreachable server:** stop SSH/WinRM on a target. Within
      `monitoring.timeout_seconds` the **Monitoring** card turns red with a
      `down` pill and the connection error; it does not show stale numbers.
- [ ] **Failed deploy:** point a project at a bad Dockerfile path. The deploy
      ends `failed`, the Deployment tab shows the error banner and the full log,
      and the previous version keeps serving.
- [ ] **Ollama down:** stop Ollama and ask the assistant a question. It still
      returns a grounded answer (from docs or live data) rather than hanging or
      erroring.

## Part 5 — Rollback

- [ ] Deploy a known-good version, then deploy a bad one (or make a change you
      want to revert).
- [ ] On the Deployment tab, click **Roll back** for that project.
- [ ] Expect a new history entry with `trigger=rollback`; Docker reuses the
      previous image tag (no rebuild), IIS restores the latest backup.
- [ ] Re-open the site; the previous version is live again.

## Part 6 — Cleanup

- [ ] Stop the control-center (Ctrl-C) and remove the Caddy container if you no
      longer need it.
