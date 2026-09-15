# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

Existing: Go (net/http + chi) server-rendered `html/template` with embedded static
assets (vanilla CSS + minimal JS). The redesign keeps this stack and adds
progressive enhancement (fetch + SSE) — no JS framework, no build step. Binding
for all UI work in this repo.

## Users

A single self-hosting operator (technical, comfortable on the command line,
learning Go) who runs a mix of Windows/IIS and Linux/Docker workloads and wants
to deploy and monitor them from one place. They arrive to *do a job* — ship an
app, check whether a deploy succeeded, add a server — usually mid-task and
impatient, not to browse. Secondary: the same person acting as admin
(credentials, API tokens, audit).

## Product Purpose

A self-hosted DevOps control center that owns the deployment lifecycle: it
connects directly to target servers (SSH for Docker/Linux, WinRM for
Windows/IIS), builds and deploys applications on a signed webhook or a manual
action, fronts each deployment with a reverse proxy and automatic TLS, and polls
metrics over the same connections. Success means an operator can go from "a git
repo" to "a live HTTPS URL" with a few guided steps, and can see exactly what
happened when something fails.

## Positioning

It treats **Windows/IIS as a first-class deploy target alongside Docker** in one
self-hosted tool (the dominant alternative is Linux/Docker only), and it ships a
**local, offline AI assistant** grounded in the platform's own docs plus live
deploy/monitoring data. It also keeps a **command-level audit log** of every
remote action.

## Operating Context

- Self-hosted, single admin, SQLite, single Go binary.
- Inventory is seeded from `servers.yaml` / `projects.yaml` on a fresh install,
  then the database is authoritative and managed from the dashboard.
- Deploys are triggered by a push webhook (HMAC-verified) or the Deploy button.
- Monitoring runs over the same SSH/WinRM connections; no agent.
- The assistant uses local ChromaDB + Ollama (never a cloud LLM).
- Reverse proxy/TLS is Caddy's admin API.

## Capabilities and Constraints

- Targets: `docker` (SSH) and `iis` (WinRM). One server per target type in v1.
- Docker deploy sources: `dockerfile`, `compose` (repo compose file), `image`
  (prebuilt registry image). IIS deploys via robocopy + app-pool/service control.
- Rollback: Docker reuses the previous image/commit; IIS restores the latest
  timestamped backup.
- REST API at `/api/v1` with scoped bearer tokens; session auth for the UI.
- Deferred to v2 (do not build without being asked): multi-server orchestration,
  one-click service catalog, team/RBAC, off-site backups, auto language/framework
  detection, private-registry auth, Git OAuth.
- The redesign must not break existing routes, the REST API, or the deploy
  pipeline. IIS keeps a documented subset of Docker-centric features.

## Brand Commitments

Name: "DevOps Control Center" (repo: winify). The user has made **Coolify the
binding UX reference**: sidebar IA (Project → Environment → Resource), a guided
"New Resource" flow, and a dense dark console aesthetic. No logo or brand assets
exist.

## Evidence on Hand

No marketing content, testimonials, screenshots, or brand assets exist. Do not
fabricate any. Real UI content is limited to what the platform produces:
deployments, logs, metrics, audit rows, servers, projects, credentials, tokens.

## Product Principles

1. **Guided over raw.** Prefer a wizard with smart defaults over a wall of
   fields; expose advanced options only on demand.
2. **The operator is mid-task.** Fast, scannable, consistent; status is always
   visible; failures explain themselves.
3. **Honest state.** Never show stale or guessed data — down is down, pending is
   pending, and errors are surfaced, not swallowed.
4. **Server-rendered and dependency-light.** No framework; enhancement is
   progressive and degrades to plain HTML.
5. **One lifecycle, both target types.** IIS and Docker share history, audit,
   monitoring and proxy; differences are documented, not hidden.

## Accessibility & Inclusion

No product-specific standard was established. Baseline expectation for a
technical console: keyboard-operable forms and modals, visible focus, and
sufficient color contrast in the dark theme.
