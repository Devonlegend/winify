# Contributing to Winify

Thanks for your interest in `winify`. This is a small, single-maintainer,
self-hosted project, so please **open an issue to discuss anything non-trivial
before** you spend time on a pull request. It keeps effort from going to waste
and keeps the scope tight.

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Ways to contribute

- Report a reproducible bug (use the bug report template).
- Propose a feature or a design change (use the feature request template).
- Improve the documentation in [`website/docs/`](website/docs/): it is the
  canonical source for both the public site and the in-app assistant.
- Fix a bug or land an agreed feature with tests.

Security issues are **not** handled in the issue tracker. See
[SECURITY.md](SECURITY.md).

## Development setup

Requirements:

- Go 1.27 or newer.
- Node.js 20+ and npm, only if you are working on the documentation site.

Build and run the server from source (PowerShell):

```powershell
go build -o control-center ./cmd/control-center
Copy-Item config.example.yaml config.yaml
.\control-center serve -config config.yaml
```

The equivalent on Linux or macOS:

```bash
go build -o control-center ./cmd/control-center
cp config.example.yaml config.yaml
./control-center serve -config config.yaml
```

Open `http://127.0.0.1:8080` and sign in with the one-time setup token printed
in the service log. On first run the listener is loopback-only.

### Documentation site

```bash
cd website
npm ci
npm start        # local preview
npm run build    # must pass; onBrokenLinks is set to throw
npm run typecheck
```

`website/docs/` is the canonical documentation. The subset the in-app assistant
ingests (`internal/assistant/docs/*.md`) is generated, never hand-edited:

```bash
./scripts/sync-assistant-docs.sh          # regenerate
./scripts/sync-assistant-docs.sh --check  # what CI runs
```

## Before you open a pull request

Run the same checks CI runs:

```bash
gofmt -l .            # must print nothing
go vet ./...
go test ./...
go build ./...
```

Then keep the change focused:

- One logical change per pull request.
- Include tests for behavior changes; bugs should come with a regression test.
- Update the docs when you change config keys, CLI flags, API routes, or any
  user-visible behavior.
- Do not reformat or refactor unrelated code, it makes review harder.

## Coding guidelines

- **Think before coding.** State your assumptions; if the request is ambiguous,
  ask in the issue first.
- **Simplicity first.** Write the minimum code that solves the problem. No
  speculative abstractions, no flexibility nobody asked for.
- **Prefer the standard library** over a new dependency unless there is a clear
  reason; note the reason in a one-line comment.
- **Security-sensitive code** (`internal/deployment`, `internal/auth`, and
  anything that touches credentials or executes commands on a target server)
  needs a short comment explaining what it can do to that server.
- The platform owns the deployment lifecycle directly over SSH/WinRM. Do not
  route the core deploy loop through an external CI system.

## Commit messages

This repository uses [Conventional Commits](https://www.conventionalcommits.org/):

```text
fix(deployment): canonicalize repo tree root
docs(site): concepts, how-to guides and quickstart content
ci(docs): add website lockfile
```

Common types: `feat`, `fix`, `docs`, `ci`, `chore`, `ui`, `refactor`, `test`.

Commit types drive automated versioning and the changelog, so choose them
deliberately. See [RELEASING.md](RELEASING.md).

## Pull request review

- Keep the description concrete: what changed, why, and how you tested it.
- Link the issue it closes, for example `Closes #12`.
- CI must be green before merge. The maintainer may squash on merge.
- Filling in the pull request template helps the review move faster.
