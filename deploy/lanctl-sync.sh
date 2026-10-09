#!/usr/bin/env bash
#
# lanctl-sync.sh — apply a shared hosts.yaml from a private git config repo.
#
# The shared inventory is byte-identical on every box; each box self-identifies
# via LANCTL_LOCAL_HOST in its own .env (see features/07_config_sync.md).
# Config is re-read by lanctl on every request, so applying needs no restart.
#
# Subcommands:
#   run      Fetch, validate, wait out SYNC_DELAY, then apply (systemd runs this)
#   hold     Engage the emergency stop (creates $DEPLOY_DIR/SYNC_DISABLED)
#   resume   Release the emergency stop
#   status   Show hold state, last sync state, and settings
#
# Settings (from the EnvironmentFile, i.e. $DEPLOY_DIR/.env):
#   CONFIG_GIT_REMOTE   git remote, e.g. git@config-repo.local:~/git-repos/lanctl-config.git
#   CONFIG_GIT_BRANCH   branch to track                 (default: main)
#   SYNC_DELAY          seconds to wait before applying (default: 300)
#
# Kill switches: `lanctl-sync.sh hold`, `systemctl stop lanctl-sync.service`
# during the delay, or `systemctl mask lanctl-sync.timer`.
#
set -euo pipefail

DEPLOY_DIR="${DEPLOY_DIR:-/opt/lanctl}"
CONFIG_FILE="$DEPLOY_DIR/hosts.yaml"
BACKUP_FILE="$DEPLOY_DIR/hosts.yaml.last-good"
WORK_DIR="$DEPLOY_DIR/.config-sync"
MIRROR_DIR="$WORK_DIR/repo.git"
STATE_FILE="$WORK_DIR/state.json"
FAILED_FILE="$WORK_DIR/FAILED"
DISABLED_FILE="${SYNC_DISABLED_FILE:-$DEPLOY_DIR/SYNC_DISABLED}"
BIN="$DEPLOY_DIR/lanctl"

REMOTE="${CONFIG_GIT_REMOTE:-}"
BRANCH="${CONFIG_GIT_BRANCH:-main}"
DELAY="${SYNC_DELAY:-300}"

log() { echo "lanctl-sync: $*"; }
fail() {
  echo "lanctl-sync: error: $*" >&2
  if [ -d "$WORK_DIR" ]; then
    printf '%s\n' "$*" > "$FAILED_FILE"
  fi
  exit 1
}

usage() {
  echo "usage: $(basename "$0") run|hold|resume|status" >&2
  exit 2
}

cmd_hold() {
  touch "$DISABLED_FILE"
  log "sync held ($DISABLED_FILE created)"
}

cmd_resume() {
  rm -f "$DISABLED_FILE"
  log "sync resumed"
}

cmd_status() {
  if [ -f "$DISABLED_FILE" ]; then
    echo "hold:      HELD ($DISABLED_FILE exists)"
  else
    echo "hold:      off"
  fi
  echo "remote:    ${REMOTE:-<unset>}"
  echo "branch:    $BRANCH"
  echo "delay:     ${DELAY}s"
  if [ -f "$STATE_FILE" ]; then
    echo "state:     $(cat "$STATE_FILE")"
  else
    echo "state:     <never synced>"
  fi
  if [ -f "$FAILED_FILE" ]; then
    echo "last failure:"
    sed 's/^/  /' "$FAILED_FILE"
  fi
  if [ -f "$CONFIG_FILE" ]; then
    echo "config:    $CONFIG_FILE present"
  else
    echo "config:    $CONFIG_FILE MISSING"
  fi
}

write_state() { # <status> <rev>
  printf '{"status":"%s","rev":"%s","at":"%s"}\n' \
    "$1" "$2" "$(date -u +%FT%TZ)" > "$STATE_FILE"
}

cmd_run() {
  if [ -f "$DISABLED_FILE" ]; then
    log "sync held ($DISABLED_FILE exists); doing nothing"
    exit 0
  fi
  [ -n "$REMOTE" ] || fail "CONFIG_GIT_REMOTE is not set; refusing to sync from nowhere"
  [ -x "$BIN" ] || fail "validator $BIN is missing or not executable"
  command -v git >/dev/null 2>&1 || fail "git is not installed"

  mkdir -p "$WORK_DIR"
  if [ ! -d "$MIRROR_DIR" ]; then
    log "cloning config mirror from $REMOTE"
    git clone --quiet --mirror "$REMOTE" "$MIRROR_DIR" \
      || fail "git clone failed (check remote and deploy key)"
  fi
  git --git-dir="$MIRROR_DIR" remote set-url origin "$REMOTE"
  git --git-dir="$MIRROR_DIR" fetch --quiet origin "$BRANCH" \
    || fail "git fetch failed for branch $BRANCH"
  rev="$(git --git-dir="$MIRROR_DIR" rev-parse FETCH_HEAD)"

  staged="$(mktemp "$WORK_DIR/staged.XXXXXX.yaml")"
  trap 'rm -f "$staged"' EXIT
  git --git-dir="$MIRROR_DIR" show "FETCH_HEAD:hosts.yaml" > "$staged" \
    || fail "branch $BRANCH has no hosts.yaml at its root"

  if [ -f "$CONFIG_FILE" ] && cmp -s "$staged" "$CONFIG_FILE"; then
    log "no changes (rev $rev)"
    write_state "ok" "$rev"
    exit 0
  fi

  if ! "$BIN" --check --config "$staged" >/dev/null 2>&1; then
    err="$("$BIN" --check --config "$staged" 2>&1 || true)"
    write_state "failed" "$rev"
    fail "staged config (rev $rev) rejected: $err"
  fi
  log "staged rev $rev validated; applying in ${DELAY}s (stop the service or run 'hold' to abort)"

  trap 'kill "$sleep_pid" 2>/dev/null; log "aborted during apply delay"; exit 0' TERM INT
  sleep "$DELAY" &
  sleep_pid=$!
  wait "$sleep_pid" || exit 0
  trap - TERM INT

  if [ -f "$DISABLED_FILE" ]; then
    log "held during delay; not applying"
    write_state "held" "$rev"
    exit 0
  fi

  if [ -f "$CONFIG_FILE" ]; then
    cp -p "$CONFIG_FILE" "$BACKUP_FILE"
  fi
  chmod 0600 "$staged"
  mv -f "$staged" "$CONFIG_FILE"
  trap - EXIT
  rm -f "$FAILED_FILE"
  write_state "ok" "$rev"
  log "applied rev $rev (no restart needed; config is re-read per request)"
}

case "${1:-}" in
  run)    cmd_run ;;
  hold)   cmd_hold ;;
  resume) cmd_resume ;;
  status) cmd_status ;;
  *)      usage ;;
esac
