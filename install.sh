#!/usr/bin/env bash
#
# install.sh — install lanctl as a systemd service on a Linux host.
#
# Copies the binary and static frontend into $DEPLOY_DIR, writes an
# EnvironmentFile, installs a systemd unit, and starts the service.
#
# Usage:
#   sudo ./install.sh
#   sudo DEPLOY_DIR=/opt/lanctl PORT=8003 RUN_USER=lanctl ./install.sh
#
# Environment variables (all optional):
#   DEPLOY_DIR           Install directory                (default: /opt/lanctl)
#   PORT                 HTTP port                        (default: 8003)
#   RUN_USER             User the service runs as         (default: root)
#   SERVICE_NAME         systemd unit name                (default: lanctl)
#   LANCTL_ALLOW_SHUTDOWN  Allow local shutdown (1/0)     (default: 1)
#   BIN_SRC              Prebuilt binary to install       (default: ./lanctl)
#   CLI_SRC              Prebuilt client to install       (default: ./lanctl-cli or dist/)
#
# The optional client binary (lanctl-cli) is installed to /usr/local/bin when
# one is found; its absence is not an error.
#
# If no prebuilt binary is found and the Go toolchain is available, it is
# built automatically.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DEPLOY_DIR="${DEPLOY_DIR:-/opt/lanctl}"
PORT="${PORT:-8003}"
RUN_USER="${RUN_USER:-root}"
SERVICE_NAME="${SERVICE_NAME:-lanctl}"
LANCTL_ALLOW_SHUTDOWN="${LANCTL_ALLOW_SHUTDOWN:-1}"
BIN_SRC="${BIN_SRC:-$SCRIPT_DIR/lanctl}"
FRONTEND_SRC="$SCRIPT_DIR/frontend"
CONFIG_SRC="$SCRIPT_DIR/hosts.example.yaml"
UNIT_SRC="$SCRIPT_DIR/lanctl.service"

die() { echo "error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "must be run as root (try: sudo ./install.sh)"
[ -f "$UNIT_SRC" ] || die "missing $UNIT_SRC"

# Build the binary if it was not supplied.
if [ ! -f "$BIN_SRC" ]; then
  if command -v go >/dev/null 2>&1; then
    echo "==> Building lanctl"
    ( cd "$SCRIPT_DIR" && CGO_ENABLED=0 go build -trimpath -o lanctl ./cmd/lanctl/ )
    BIN_SRC="$SCRIPT_DIR/lanctl"
  else
    die "no binary at $BIN_SRC and 'go' is not installed"
  fi
fi

echo "==> Installing to $DEPLOY_DIR"
install -d -m 0755 "$DEPLOY_DIR" "$DEPLOY_DIR/frontend"
install -m 0755 "$BIN_SRC" "$DEPLOY_DIR/lanctl"

echo "==> Installing frontend files"
find "$FRONTEND_SRC" -maxdepth 1 -name '*.html' -exec install -m 0644 {} "$DEPLOY_DIR/frontend/" \;

# Optionally install the client CLI (non-fatal when absent).
detect_cli() {
  if [ -n "${CLI_SRC:-}" ] && [ -f "$CLI_SRC" ]; then
    echo "$CLI_SRC"; return 0
  fi
  if [ -f "$SCRIPT_DIR/lanctl-cli" ]; then
    echo "$SCRIPT_DIR/lanctl-cli"; return 0
  fi
  local arch=""
  case "$(uname -m)" in
    x86_64|amd64)     arch=amd64 ;;
    aarch64|arm64)    arch=arm64 ;;
    armv7l|armv6l|arm) arch=arm ;;
  esac
  if [ -n "$arch" ] && [ -f "$SCRIPT_DIR/dist/lanctl-cli-linux-$arch" ]; then
    echo "$SCRIPT_DIR/dist/lanctl-cli-linux-$arch"
  fi
  return 0
}

CLI_BIN="$(detect_cli)"
if [ -n "$CLI_BIN" ]; then
  echo "==> Installing lanctl-cli to /usr/local/bin"
  install -m 0755 "$CLI_BIN" /usr/local/bin/lanctl-cli
else
  echo "==> lanctl-cli not found (run 'make build-cli' to build it); skipping client install"
fi

if [ -f "$CONFIG_SRC" ]; then
  install -m 0644 "$CONFIG_SRC" "$DEPLOY_DIR/hosts.example.yaml"
fi

if [ ! -f "$DEPLOY_DIR/hosts.yaml" ]; then
  echo "==> Creating $DEPLOY_DIR/hosts.yaml from example (edit it to add your hosts)"
  cp "$DEPLOY_DIR/hosts.example.yaml" "$DEPLOY_DIR/hosts.yaml"
  chmod 0600 "$DEPLOY_DIR/hosts.yaml"
else
  echo "==> Keeping existing $DEPLOY_DIR/hosts.yaml"
fi

echo "==> Writing $DEPLOY_DIR/.env"
cat > "$DEPLOY_DIR/.env" <<EOF
PORT=$PORT
DEPLOY_DIR=$DEPLOY_DIR
LANCTL_ALLOW_SHUTDOWN=$LANCTL_ALLOW_SHUTDOWN
EOF
chmod 0600 "$DEPLOY_DIR/.env"

echo "==> Installing systemd unit $SERVICE_NAME.service"
sed -e "s|@RUN_USER@|$RUN_USER|g" -e "s|@DEPLOY_DIR@|$DEPLOY_DIR|g" \
  "$UNIT_SRC" > "/etc/systemd/system/$SERVICE_NAME.service"

systemctl daemon-reload
systemctl enable "$SERVICE_NAME.service"
systemctl restart "$SERVICE_NAME.service"

echo
echo "lanctl installed and running."
echo "  Config:  $DEPLOY_DIR/hosts.yaml"
echo "  Service: systemctl status $SERVICE_NAME"
echo "  URL:     http://localhost:$PORT"
echo
echo "Edit $DEPLOY_DIR/hosts.yaml, then: systemctl restart $SERVICE_NAME"
