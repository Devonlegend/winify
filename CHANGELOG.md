# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-16

### Added

- Initial public release of the DevOps Control Center (`winify`): a self-hosted
  deployment platform that owns the deployment lifecycle for Windows
  IIS/services and Docker targets.
- In-dashboard management of servers, projects, and AES-256-GCM encrypted
  credentials; deploy from the UI or on a signed push webhook.
- Deploy targets for Windows native service (`winsvc`), IIS (`iis`), and
  Docker/Linux (`docker`) over SSH.
- Scoped REST API at `/api/v1` with bearer tokens.
- Per-project HMAC webhook verification and deploy history with rollback.
- Automatic HTTPS via Caddy's admin API (Let's Encrypt, no certbot step).
- Agentless monitoring of CPU, memory, disk, uptime, and service state over the
  existing SSH/WinRM connections.
- Local assistant (ChromaDB + Ollama) grounded in the guides and live deploy and
  monitoring data.
- Command-level audit log of every remote command.
- One-line installers for Windows (`install.ps1`) and Linux (`install.sh`) with
  SHA-256 verification of release assets.
- Public documentation site at [winify.space](https://winify.space).

[Unreleased]: https://github.com/Devonlegend/winify/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Devonlegend/winify/releases/tag/v0.1.0
