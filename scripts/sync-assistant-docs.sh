#!/usr/bin/env bash
# Sync the in-app assistant knowledge base from the canonical Docusaurus docs.
# The assistant embeds internal/assistant/docs/*.md (go:embed) and chunks on "##",
# so the subset must stay small and task-oriented. Never hand-edit the output.
#
#   ./scripts/sync-assistant-docs.sh          # regenerate
#   ./scripts/sync-assistant-docs.sh --check  # CI: fail if stale
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/website/docs"
DST="$ROOT/internal/assistant/docs"
CHECK=0
if [[ "${1:-}" == "--check" ]]; then CHECK=1; fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Generated-file banner (Go-embed safe: Markdown comment).
banner() {
  printf '<!-- GENERATED from website/docs — do not edit by hand. Edit the source and run scripts/sync-assistant-docs.sh. -->\n\n'
}

# Strip YAML frontmatter, drop the source H1 title (callers emit their own),
# and demote any remaining single-# headings to ## so each generated
# file keeps exactly one H1 (the assistant chunks on ## sections).
body() {
  awk 'NR==1 && /^---$/{p=1;next} p&&/^---$/{p=0;next} !p' "$1" \
    | awk 'BEGIN{skipped=0} /^# / && !skipped {skipped=1; next} {print}' \
    | sed 's/^# \(.*\)/## \1/'
}

build_setup_guide() {
  { banner
    printf '# Setup Guide\n\n'
    printf '## Windows quickstart (summary)\n\n'
    body "$SRC/quickstart/windows.md"
    printf '\n'
    sed -n '/^# Monitoring/,/^## Configuration/p' "$SRC/concepts/monitoring.md" | tail -n +2 | sed 's/^# \(.*\)/## \1/'
    printf '\n'
    sed -n '/^## Roll back/,/^## Health gate/p' "$SRC/how-to/deploy-rollback.md"
  } > "$tmp/setup-guide.md"
}

build_adding() {
  { banner
    printf '# Adding a Project or Server\n\n'
    body "$SRC/how-to/add-server.md"
    printf '\n'
    body "$SRC/how-to/add-project.md"
  } > "$tmp/adding-project-server.md"
}

build_troubleshooting() {
  { banner
    printf '# Troubleshooting\n\n'
    body "$SRC/troubleshooting/index.md"
  } > "$tmp/troubleshooting.md"
}

build_faq() {
  { banner
    printf '# FAQ\n\n'
    body "$SRC/troubleshooting/faq.md"
  } > "$tmp/faq.md"
}

build_api() {
  { banner
    printf '# REST API\n\n'
    body "$SRC/reference/api.md"
  } > "$tmp/api.md"
}

build_setup_guide
build_adding
build_troubleshooting
build_faq
build_api

if [[ "$CHECK" == "1" ]]; then
  status=0
  for f in setup-guide.md adding-project-server.md troubleshooting.md faq.md api.md; do
    if ! diff -q "$tmp/$f" "$DST/$f" >/dev/null 2>&1; then
      echo "stale: internal/assistant/docs/$f — run ./scripts/sync-assistant-docs.sh"
      status=1
    fi
  done
  exit "$status"
fi

for f in setup-guide.md adding-project-server.md troubleshooting.md faq.md api.md; do
  cp "$tmp/$f" "$DST/$f"
  echo "synced internal/assistant/docs/$f"
done
