# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file is maintained by
[release-please](https://github.com/googleapis/release-please); see
[RELEASING.md](RELEASING.md).

## [0.1.2](https://github.com/Devonlegend/winify/compare/v0.1.1...v0.1.2) (2026-10-16)


### Documentation

* lead with the problem Winify solves and fix the mobile nav ([c4c1c5d](https://github.com/Devonlegend/winify/commit/c4c1c5d0a696013097035706019131708986505a))
* replace em dashes with commas and colons in prose ([732c7ee](https://github.com/Devonlegend/winify/commit/732c7eec1af465cd146a331b2280df7ce50c399e))

## [0.1.1](https://github.com/Devonlegend/winify/compare/v0.1.0...v0.1.1) (2026-10-16)


### Bug Fixes

* **packaging:** parse byte-array checksum asset in install.ps1 ([0305cfb](https://github.com/Devonlegend/winify/commit/0305cfb0f6c998a5068ddfdfe653a49145b1b5b3))


### Documentation

* date v0.1.0 in the changelog and add the release badge ([bbcfc7b](https://github.com/Devonlegend/winify/commit/bbcfc7bfbd82cc7787abe38d4aa10e9042e5c34d))
* refresh the docs theme and rename the product to Winify ([1a9894a](https://github.com/Devonlegend/winify/commit/1a9894aec0fb80d7b18a34de0cc709b81103c607))

## [0.1.0](https://github.com/Devonlegend/winify/releases/tag/v0.1.0) (2026-10-16)

### Added

- Initial public release of Winify: a self-hosted
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
