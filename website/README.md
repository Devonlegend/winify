# winify docs site (Docusaurus v3)

Canonical source: `website/docs/`. Public build deploys to GitHub Pages via `.github/workflows/docs.yml`.

In-app assistant subset (`internal/assistant/docs/*.md`) is **generated** from this site:

```bash
./scripts/sync-assistant-docs.sh --check   # CI: fail if stale
./scripts/sync-assistant-docs.sh           # regenerate
```

## Develop

```bash
cd website
npm ci
npm start        # http://localhost:3000/winify/
npm run build    # must pass with onBrokenLinks: throw
npm run typecheck
```

## Rules

- One `h1` per page, Docusaurus frontmatter (`title`, `description`) required.
- Relative links only (`../how-to/...`), fenced code blocks with a language tag.
- `##` sections are the RAG chunk unit, keep them task-sized.
- Never hand-edit `internal/assistant/docs/*.md`; edit here and re-sync.
