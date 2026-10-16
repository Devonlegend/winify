---
title: Tunnel for CGNAT
description: Give a host behind CGNAT a public HTTPS URL with a Cloudflare tunnel.
---

# Tunnel for CGNAT

Behind CGNAT or a private ISP link, the machine cannot accept inbound connections. Public-IP schemes (`sslip.io`) cannot work; the carrier's NAT address never reaches you, and no certificate can be issued. A tunnel makes an **outbound** connection and gives you a public HTTPS URL.

## What winify provisions (once)

Given an API token + domain, winify:

1. Resolves the zone for the domain.
2. Finds or creates the named tunnel.
3. Fetches the connector token (bearer credential, written only to a protected token file).
4. Sets ingress `*.domain → http://localhost:80` (Caddy), with a 404 catch-all.
5. Creates the proxied wildcard CNAME `*.domain → <tunnel-id>.cfargotunnel.com`.
6. Installs `cloudflared` as an NSSM service (`tunnel run --token-file <file>`).
7. Auto-adds `*.domain` to Caddy's `acme_skip` (served HTTP behind the tunnel terminator, avoiding redirect loops).

Everything is idempotent: re-running reuses tunnel, ingress, and DNS. Because wildcard ingress + DNS are created **once at setup**, per-deploy winify never calls the provider; it only registers the host in Caddy.

## Configure

```yaml
tunnel:
  enabled: true
  provider: cloudflare
  domain: example.com
  account_id: <cloudflare account id>
  tunnel_name: winify
  api_token_ref: "vault:cloudflare-api"
  origin: "http://localhost:80"
  connector_source: ""   # local cloudflared.exe (wins)
  connector_url: ""      # or download URL
  service_name: winify-tunnel
```

Store the provider token encrypted:

```powershell
.\winify.exe cred add cloudflare-api
```

Required token scopes: Account `Cloudflare Tunnel:Edit` + Zone `DNS:Edit` on the domain above. Least privilege; never logged.

## Operate

- Deploy apps with `domain: app.example.com`: they resolve through the wildcard and route in Caddy like any other host.
- The control-center UI itself is **not** auto-routed by the tunnel step. Exposing the admin panel publicly is a deliberate operator choice, keep it loopback/internal, or put Cloudflare Access (email OTP) in front.
- To un-publish, remove the ingress rule or stop the `winify-tunnel` service.

## Limits (V1)

- Cloudflare only (provider isolated behind a package; ngrok/Tailscale not implemented).
- Local host only (not over remote WinRM bootstrap).
