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
#   LANCTL_LOCAL_HOST    This box's host name (or host/vm) for shared configs
#   CONFIG_GIT_REMOTE    Private git remote for config sync (enables the timer)
#   CONFIG_GIT_BRANCH    Tracked branch for config sync   (default: main)
#   SYNC_DELAY           Seconds to wait before applying a synced config (default: 300)
#   SYNC_SERVICE_NAME    Sync timer unit name             (default: lanctl-sync)
#
# The optional client binary (lanctl-cli) is installed to /usr/local/bin when
# one is found; its absence is not an error.
#
# The optional config sync (deploy/lanctl-sync.sh + timer) is installed when
# its sources are present; the timer is enabled only when CONFIG_GIT_REMOTE
# is set (via the environment or a previous install's .env).
#
# If no prebuilt binary is found and the Go toolchain is available, it is
# built automatically.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DEPLOY_DIR="${DEPLOY_DIR:-/opt/lanctl}"
RUN_USER="${RUN_USER:-root}"
SERVICE_NAME="${SERVICE_NAME:-lanctl}"
SYNC_SERVICE_NAME="${SYNC_SERVICE_NAME:-lanctl-sync}"
BIN_SRC="${BIN_SRC:-$SCRIPT_DIR/lanctl}"
FRONTEND_SRC="$SCRIPT_DIR/frontend"
CONFIG_SRC="$SCRIPT_DIR/hosts.example.yaml"
UNIT_SRC="$SCRIPT_DIR/lanctl.service"
SYNC_SRC="$SCRIPT_DIR/deploy/lanctl-sync.sh"
SYNC_SERVICE_SRC="$SCRIPT_DIR/deploy/lanctl-sync.service"
SYNC_TIMER_SRC="$SCRIPT_DIR/deploy/lanctl-sync.timer"

die() { echo "error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "must be run as root (try: sudo ./install.sh)"
[ -f "$UNIT_SRC" ] || die "missing $UNIT_SRC"

# Keep saved per-box settings across reinstalls; explicit environment wins.
if [ -f "$DEPLOY_DIR/.env" ]; then
  while IFS='=' read -r key value; do
    case "$key" in
      PORT|LANCTL_ALLOW_SHUTDOWN|LANCTL_LOCAL_HOST|CONFIG_GIT_REMOTE|CONFIG_GIT_BRANCH|SYNC_DELAY)
        if [ -z "${!key+x}" ]; then
          printf -v "$key" '%s' "$value"
        fi
        ;;
    esac
  done < <(grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$DEPLOY_DIR/.env")
fi

LANCTL_LOCAL_HOST="${LANCTL_LOCAL_HOST:-}"
CONFIG_GIT_REMOTE="${CONFIG_GIT_REMOTE:-}"
CONFIG_GIT_BRANCH="${CONFIG_GIT_BRANCH:-main}"
SYNC_DELAY="${SYNC_DELAY:-300}"
PORT="${PORT:-8003}"
LANCTL_ALLOW_SHUTDOWN="${LANCTL_ALLOW_SHUTDOWN:-1}"

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
# This box's identity in a shared hosts.yaml (host name, or host/vm).
# The shared file omits local:; this var marks the local entry instead.
LANCTL_LOCAL_HOST=$LANCTL_LOCAL_HOST
# Private git remote holding the shared hosts.yaml. Empty disables sync.
# e.g. CONFIG_GIT_REMOTE=git@config-repo.local:~/git-repos/lanctl-config.git
CONFIG_GIT_REMOTE=$CONFIG_GIT_REMOTE
CONFIG_GIT_BRANCH=$CONFIG_GIT_BRANCH
# Seconds a validated config waits before applying (time to hold/stop).
SYNC_DELAY=$SYNC_DELAY
EOF
chmod 0600 "$DEPLOY_DIR/.env"

echo "==> Installing systemd unit $SERVICE_NAME.service"
sed -e "s|@RUN_USER@|$RUN_USER|g" -e "s|@DEPLOY_DIR@|$DEPLOY_DIR|g" \
  "$UNIT_SRC" > "/etc/systemd/system/$SERVICE_NAME.service"

if [ -f "$SYNC_SRC" ] && [ -f "$SYNC_SERVICE_SRC" ] && [ -f "$SYNC_TIMER_SRC" ]; then
  echo "==> Installing lanctl-sync.sh to $DEPLOY_DIR"
  install -m 0755 "$SYNC_SRC" "$DEPLOY_DIR/lanctl-sync.sh"
  echo "==> Installing systemd units $SYNC_SERVICE_NAME.service/.timer"
  sed -e "s|@RUN_USER@|$RUN_USER|g" -e "s|@DEPLOY_DIR@|$DEPLOY_DIR|g" \
    "$SYNC_SERVICE_SRC" > "/etc/systemd/system/$SYNC_SERVICE_NAME.service"
  install -m 0644 "$SYNC_TIMER_SRC" "/etc/systemd/system/$SYNC_SERVICE_NAME.timer"
else
  echo "==> deploy/ sync files not found; skipping config-sync install"
fi

systemctl daemon-reload
systemctl enable "$SERVICE_NAME.service"
systemctl restart "$SERVICE_NAME.service"

if [ -f "/etc/systemd/system/$SYNC_SERVICE_NAME.timer" ]; then
  if [ -n "$CONFIG_GIT_REMOTE" ]; then
    systemctl enable "$SYNC_SERVICE_NAME.timer"
    systemctl restart "$SYNC_SERVICE_NAME.timer"
  else
    echo "==> CONFIG_GIT_REMOTE is empty; sync timer installed but not enabled."
    echo "    Set it in $DEPLOY_DIR/.env, then: systemctl enable --now $SYNC_SERVICE_NAME.timer"
  fi
fi

echo
echo "lanctl installed and running."
echo "  Config:  $DEPLOY_DIR/hosts.yaml"
echo "  Service: systemctl status $SERVICE_NAME"
if [ -n "$CONFIG_GIT_REMOTE" ]; then
  echo "  Sync:    systemctl status $SYNC_SERVICE_NAME.timer"
fi
echo "  URL:     http://localhost:$PORT"
echo
echo "Edit $DEPLOY_DIR/hosts.yaml — changes apply live, no restart needed."
echo "Restart only for .env changes: systemctl restart $SERVICE_NAME"
