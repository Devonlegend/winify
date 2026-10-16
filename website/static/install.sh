#!/bin/sh
# winify quick installer for Linux (systemd).
#
#   curl -fsSL https://winify.space/install.sh | sh
#   curl -fsSL https://winify.space/install.sh | sh -s -- --port 8080 --version v0.1.0
#
# What it does:
#   1. Resolves version (explicit --version, $WINIFY_VERSION, or latest GitHub release).
#   2. Downloads winify-<version>-linux-<arch>.tar.gz + .sha256 from GitHub Releases.
#   3. Verifies SHA-256, installs to /usr/local/bin/winify.
#   4. Writes /etc/winify/config.yaml if missing, installs + starts systemd unit.
#
# Env overrides: WINIFY_VERSION, WINIFY_PORT, WINIFY_REPO, WINIFY_BASE_URL (Pages mirror).
set -eu

REPO="${WINIFY_REPO:-Devonlegend/winify}"
VERSION="${WINIFY_VERSION:-}"
PORT="${WINIFY_PORT:-8080}"
BINDIR="/usr/local/bin"
CONFDIR="/etc/winify"
CONFIG="$CONFDIR/config.yaml"
SERVICE="winify"

usage() {
  echo "usage: install.sh [--version vX.Y.Z] [--port 8080] [--no-service]" >&2
}

NO_SERVICE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --no-service) NO_SERVICE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown flag: $1" >&2; usage; exit 1 ;;
  esac
done

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }; }
need curl; need tar; need sha256sum; need uname

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "unsupported architecture: $ARCH (want amd64 or arm64)" >&2; exit 1 ;;
esac

if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
  echo "Resolving latest winify release..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep -m1 '"tag_name"' | cut -d'"' -f4)"
  [ -n "$VERSION" ] || { echo "could not resolve latest release" >&2; exit 1; }
fi
echo "Installing winify $VERSION (linux/$ARCH)..."

BASE="${WINIFY_BASE_URL:-https://github.com/$REPO/releases/download/$VERSION}"
ASSET="winify-${VERSION}-linux-${ARCH}.tar.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

curl -fsSL -o "$TMP/$ASSET" "$BASE/$ASSET"
curl -fsSL -o "$TMP/$ASSET.sha256" "$BASE/$ASSET.sha256"

(cd "$TMP" && sha256sum -c "$ASSET.sha256")

tar -xzf "$TMP/$ASSET" -C "$TMP"
BIN_SRC="$TMP/winify"
[ -f "$BIN_SRC" ] || BIN_SRC="$TMP/winify-linux-$ARCH"
[ -f "$BIN_SRC" ] || { echo "archive did not contain winify binary" >&2; exit 1; }

if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then SUDO="sudo"; else
    echo "run as root or install sudo (need $BINDIR, $CONFDIR, systemd)" >&2; exit 1
  fi
else
  SUDO=""
fi

$SUDO install -m 0755 "$BIN_SRC" "$BINDIR/winify"
$SUDO mkdir -p "$CONFDIR" /var/lib/winify/data
if [ ! -f "$CONFIG" ]; then
  $SUDO sh -c "cat > '$CONFIG' <<EOF
server:
  addr: \"127.0.0.1:$PORT\"
database:
  path: \"/var/lib/winify/data/control-center.db\"
proxy:
  enabled: false
bootstrap:
  enabled: true
  install_dir: \"/var/lib/winify\"
monitoring:
  enabled: true
assistant:
  enabled: false
EOF"
  echo "Wrote $CONFIG"
fi

if [ "$NO_SERVICE" -eq 0 ] && [ -d /run/systemd/system ]; then
  $SUDO sh -c "cat > /etc/systemd/system/$SERVICE.service <<EOF
[Unit]
Description=Winify
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BINDIR/winify serve -config $CONFIG
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF"
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable --now "$SERVICE"
  echo ""
  echo "winify $VERSION installed and started."
  echo "  URL:    http://localhost:$PORT"
  echo "  Config: $CONFIG"
  echo "  Logs:   journalctl -u $SERVICE -f"
else
  echo ""
  echo "winify $VERSION installed to $BINDIR/winify (service setup skipped)."
  echo "Run: $BINDIR/winify serve -config $CONFIG"
fi
