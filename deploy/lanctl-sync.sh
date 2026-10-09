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
#   CONFIG_GIT_REMOTE   git remote: ssh:// URL, scp-like host:path, or a plain
#                       local path on the box hosting the repo (no SSH then).
#                       e.g. git@config-repo.local:~/git-repos/lanctl-config.git
#   CONFIG_GIT_SSH_KEY  private key the service user uses for SSH remotes.
#   CONFIG_GIT_KNOWN_HOSTS  known_hosts file pinning the repo host's key.
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

# is_ssh_remote <url>: true for ssh:// URLs and scp-like host:path remotes
# (a colon before the first slash — git's own interpretation). Plain paths
# and file:// / https:// URLs are not SSH transport.
is_ssh_remote() {
  case "$1" in
    ssh://*) return 0 ;;
    *://*) return 1 ;;
  esac
  case "$1" in
    *:*)
      local before="${1%%/*}"
      case "$before" in
        *:* ) return 0 ;;
      esac
      return 1 ;;
    *) return 1 ;;
  esac
}

# git_ssh_command: print the ssh command git must use for SSH remotes.
# Non-interactive (no password prompts) with an explicit key and pinned host
# key, so a missing setup fails fast with a message naming the setting.
git_ssh_command() {
  local key="${CONFIG_GIT_SSH_KEY:-}" known="${CONFIG_GIT_KNOWN_HOSTS:-}"
  if [ -z "$key" ]; then
    echo "CONFIG_GIT_SSH_KEY is not set; the service user needs its own SSH key for the config repo" >&2
    return 1
  fi
  if [ ! -f "$key" ]; then
    echo "CONFIG_GIT_SSH_KEY=$key: file not found" >&2
    return 1
  fi
  if [ -z "$known" ]; then
    echo "CONFIG_GIT_KNOWN_HOSTS is not set; pin the repo host key (ssh-keyscan)" >&2
    return 1
  fi
  if [ ! -f "$known" ]; then
    echo "CONFIG_GIT_KNOWN_HOSTS=$known: file not found" >&2
    return 1
  fi
  printf 'ssh -i %s -o IdentitiesOnly=yes -o UserKnownHostsFile=%s -o BatchMode=yes -o ConnectTimeout=20' \
    "$key" "$known"
}

# CHECK_MARKER: usage text of the --check flag. Binaries predating --check
# (v0.2.0) lack it; run as validators they ignore argv and start a server
# instead (port clash or worse). Greping the binary never executes it, and
# usage strings survive stripped (-s -w) builds.
CHECK_MARKER="validate hosts.yaml and exit"

# check_validator: refuse a $BIN without --check support.
check_validator() {
  if grep -a -q -F "$CHECK_MARKER" "$BIN" 2>/dev/null; then
    return 0
  fi
  echo "validator $BIN lacks --check support — upgrade lanctl to v0.2.0 or newer" >&2
  return 1
}
# git_safe_env: print VAR=value lines git needs for this remote. Local-path
# remotes live outside the service user's ownership (e.g. root reading an
# admin's repo), which modern git refuses as "dubious ownership" — allowlist
# the configured remote (raw and resolved) for our invocations only.
# SSH remotes need nothing: transport runs as the SSH user server-side.
git_safe_env() {
  if is_ssh_remote "$REMOTE"; then
    return 0
  fi
  local path="$REMOTE"
  case "$path" in
    file://*) path="${path#file://}" ;;
  esac
  local resolved
  resolved="$(realpath -m "$path" 2>/dev/null || echo "$path")"
  echo "GIT_CONFIG_COUNT=2"
  echo "GIT_CONFIG_KEY_0=safe.directory"
  echo "GIT_CONFIG_VALUE_0=$path"
  echo "GIT_CONFIG_KEY_1=safe.directory"
  echo "GIT_CONFIG_VALUE_1=$resolved"
}

cmd_run() {
  if [ -f "$DISABLED_FILE" ]; then
    log "sync held ($DISABLED_FILE exists); doing nothing"
    exit 0
  fi
  [ -n "$REMOTE" ] || fail "CONFIG_GIT_REMOTE is not set; refusing to sync from nowhere"
  [ -x "$BIN" ] || fail "validator $BIN is missing or not executable"
  if ! verr="$(check_validator 2>&1 >/dev/null)"; then
    fail "validator check: $verr"
  fi
  command -v git >/dev/null 2>&1 || fail "git is not installed"

  # SSH remotes need an explicit identity for the service user; local paths
  # (e.g. on the box hosting the repo) need none.
  if is_ssh_remote "$REMOTE"; then
    if ! GIT_SSH_COMMAND="$(git_ssh_command 2>&1)"; then
      fail "git ssh setup: $GIT_SSH_COMMAND"
    fi
    export GIT_SSH_COMMAND
  else
    while IFS='=' read -r key value; do
      [ -n "$key" ] && export "$key=$value"
    done < <(git_safe_env)
    # Cross-user ownership (service user vs. repo owner) makes modern git
    # refuse local remotes. The discovery-time check only trusts config
    # files, so allowlist the remote in the service user's own gitconfig
    # (idempotent — the exact remedy git itself prescribes).
    safe_path="$REMOTE"
    case "$safe_path" in
      file://*) safe_path="${safe_path#file://}" ;;
    esac
    safe_path="$(realpath -m "$safe_path" 2>/dev/null || echo "$safe_path")"
    if ! git config --global --get-all safe.directory 2>/dev/null | grep -qxF "$safe_path"; then
      if git config --global --add safe.directory "$safe_path" 2>/dev/null; then
        log "allowlisted $safe_path in git safe.directory"
      else
        log "warning: cannot update git safe.directory; if clone fails, run as the service user: git config --global --add safe.directory $safe_path"
      fi
    fi
  fi

  mkdir -p "$WORK_DIR"
  if [ -d "$MIRROR_DIR" ] && ! git --git-dir="$MIRROR_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    log "removing stale mirror dir left by a failed clone"
    rm -rf "$MIRROR_DIR"
  fi
  if [ ! -d "$MIRROR_DIR" ]; then
    log "cloning config mirror from $REMOTE"
    git clone --quiet --mirror "$REMOTE" "$MIRROR_DIR" \
      || fail "git clone failed (check CONFIG_GIT_REMOTE; for SSH remotes also CONFIG_GIT_SSH_KEY/CONFIG_GIT_KNOWN_HOSTS)"
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

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  case "${1:-}" in
    run)    cmd_run ;;
    hold)   cmd_hold ;;
    resume) cmd_resume ;;
    status) cmd_status ;;
    *)      usage ;;
  esac
fi
