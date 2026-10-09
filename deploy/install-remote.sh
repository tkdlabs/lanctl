#!/usr/bin/env bash
#
# install-remote.sh — install lanctl on a remote host over SSH.
#
# No Go toolchain is required on either side: binaries come from a local
# dist/ tree (make cross) or a GitHub Releases tarball. The bundle is
# streamed over SSH into a remote temp dir that is removed afterwards, so
# peak disk use is the extracted tree (~10 MB in /tmp) and nothing stays
# behind. An existing remote hosts.yaml is never touched.
#
# Usage:
#   deploy/install-remote.sh --host mybox --user operator --local-host mybox \
#     --config-remote git@config-repo.local:~/git-repos/lanctl-config.git
#
#   deploy/install-remote.sh --host mybox --arch arm64 --source release -y
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() { echo "error: $*" >&2; exit 1; }
log() { echo "install-remote: $*"; }

# CHECK_MARKER: usage text of the --check flag (see lanctl-sync.sh).
# dist/ binaries predating it are stale: the sync would refuse them, so
# shipping them is refused here instead.
CHECK_MARKER="validate hosts.yaml and exit"

# dist_binary_state <arch>: echo ok|missing|stale for the local dist binary.
dist_binary_state() {
  local bin="$SCRIPT_DIR/dist/lanctl-linux-$1"
  if [ ! -f "$bin" ]; then echo missing; return 0; fi
  if grep -a -q -F "$CHECK_MARKER" "$bin" 2>/dev/null; then echo ok; else echo stale; fi
}

usage() {
  cat <<'EOF'
usage: install-remote.sh --host HOST [options]

  --host HOST        target hostname or IP (required)
  --user USER        ssh user (default: current user)
  --ssh-port PORT    ssh port (default: 22)
  --arch ARCH        target arch: amd64|arm64|arm (default: auto-detect)
  --source SRC       dist|release|auto (default: auto = fresh dist/, else release)
  --version TAG      release tag for --source release (default: latest)
  --repo SLUG        GitHub repo slug for releases (default: tkdlabs/lanctl)
  --dir DIR          remote install dir (default: /opt/lanctl)
  --service-port P   lanctl HTTP port (default: 8003)
  --run-user USER    service user (default: root)
  --local-host NAME  LANCTL_LOCAL_HOST for the target (host or host/vm)
  --config-remote U  CONFIG_GIT_REMOTE for the target (enables sync timer)
  --branch B         CONFIG_GIT_BRANCH for the target (default: main)
  --sync-delay SECS  SYNC_DELAY for the target (default: 300)
  --git-key FILE     local private key to provision as the target's sync key
  --git-known-hosts FILE  local known_hosts to provision for the repo host
  --ssh-opt OPT      extra ssh option (repeatable)
  -y, --yes          skip confirmation prompt
  -n, --dry-run      print the plan; change nothing
  -h, --help         this help
EOF
  exit "${1:-0}"
}

# map_arch <uname -m>: print our arch suffix or fail.
map_arch() {
  case "$1" in
    x86_64|amd64)     echo amd64 ;;
    aarch64|arm64)    echo arm64 ;;
    armv7l|armv6l|arm) echo arm ;;
    *) return 1 ;;
  esac
}

# assemble_dist_bundle <arch> <destdir>: build the release-tarball layout
# from the local checkout under <destdir>/lanctl-linux-<arch>.
assemble_dist_bundle() {
  local arch="$1" dest="$2"
  local dir="$dest/lanctl-linux-$arch" bin="dist/lanctl-linux-$arch"
  [ -f "$SCRIPT_DIR/$bin" ] || return 1
  mkdir -p "$dir/frontend" "$dir/deploy"
  install -m 0755 "$SCRIPT_DIR/$bin" "$dir/lanctl"
  if [ -f "$SCRIPT_DIR/dist/lanctl-cli-linux-$arch" ]; then
    install -m 0755 "$SCRIPT_DIR/dist/lanctl-cli-linux-$arch" "$dir/lanctl-cli"
  fi
  install -m 0644 "$SCRIPT_DIR"/frontend/*.html "$dir/frontend/"
  install -m 0755 "$SCRIPT_DIR/deploy/lanctl-sync.sh" "$dir/deploy/"
  install -m 0644 "$SCRIPT_DIR/deploy/lanctl-sync.service" "$dir/deploy/"
  install -m 0644 "$SCRIPT_DIR/deploy/lanctl-sync.timer" "$dir/deploy/"
  install -m 0755 "$SCRIPT_DIR/install.sh" "$dir/install.sh"
  install -m 0644 "$SCRIPT_DIR/lanctl.service" "$dir/"
  install -m 0644 "$SCRIPT_DIR/hosts.example.yaml" "$dir/"
}

main() {
  local host="" ssh_user="" ssh_port="22" arch="" source="auto" version="latest"
  local repo="tkdlabs/lanctl" remote_dir="/opt/lanctl" service_port="8003"
  local run_user="root" service_name="lanctl" allow_shutdown="1"
  local local_host="" config_remote="" config_branch="main" sync_delay="300"
  local git_key="" git_known=""
  local yes=0 dryrun=0
  local -a ssh_opts=()

  while [ $# -gt 0 ]; do
    case "$1" in
      --host)          host="$2"; shift 2 ;;
      --user)          ssh_user="$2"; shift 2 ;;
      --ssh-port)      ssh_port="$2"; shift 2 ;;
      --arch)          arch="$2"; shift 2 ;;
      --source)        source="$2"; shift 2 ;;
      --version)       version="$2"; shift 2 ;;
      --repo)          repo="$2"; shift 2 ;;
      --dir)           remote_dir="$2"; shift 2 ;;
      --service-port)  service_port="$2"; shift 2 ;;
      --run-user)      run_user="$2"; shift 2 ;;
      --local-host)    local_host="$2"; shift 2 ;;
      --config-remote) config_remote="$2"; shift 2 ;;
      --branch)        config_branch="$2"; shift 2 ;;
      --sync-delay)    sync_delay="$2"; shift 2 ;;
      --git-key)       git_key="$2"; shift 2 ;;
      --git-known-hosts) git_known="$2"; shift 2 ;;
      --ssh-opt)       ssh_opts+=("$2"); shift 2 ;;
      -y|--yes)        yes=1; shift ;;
      -n|--dry-run)    dryrun=1; shift ;;
      -h|--help)       usage 0 ;;
      --) shift; break ;;
      -*) die "unknown option $1 (see --help)" ;;
      *) die "unexpected argument $1 (see --help)" ;;
    esac
  done

  [ -n "$host" ] || die "--host is required (see --help)"
  if [ -z "$ssh_user" ]; then ssh_user="$(id -un)"; fi
  case "$source" in dist|release|auto) ;; *) die "--source must be dist|release|auto" ;; esac
  if [ -n "$arch" ]; then
    map_arch "$arch" >/dev/null || die "--arch must be amd64|arm64|arm"
    arch="$(map_arch "$arch")"
  fi
  command -v ssh >/dev/null 2>&1 || die "ssh is not installed"
  command -v tar >/dev/null 2>&1 || die "tar is not installed"

  local -a ssh_base=(ssh -p "$ssh_port" "${ssh_opts[@]}" "$ssh_user@$host")

  # --- resolve arch + source (dry-run may skip ssh-dependent steps) ---
  local arch_how="auto-detect"
  if [ -z "$arch" ]; then
    if [ "$dryrun" -eq 1 ]; then
      arch="<auto-detect>"
    else
      log "detecting target arch"
      machine="$("${ssh_base[@]}" 'uname -m')" || die "arch detect failed (ssh to $host)"
      arch="$(map_arch "$machine")" || die "unsupported target arch: $machine"
      arch_how="auto-detect ($machine)"
    fi
  else
    arch_how="flag"
  fi

  local src_how="" tarball="" stagedir=""
  if [ "$source" = "auto" ]; then
    if [[ "$arch" == \<* ]]; then
      source="dist"
    else
      case "$(dist_binary_state "$arch")" in
        ok) source="dist" ;;
        missing)
          source="release"
          log "no local dist binary; falling back to release"
          ;;
        stale)
          source="release"
          log "local dist binary predates --check support; falling back to release (run 'make cross' to refresh dist/)"
          ;;
      esac
    fi
  fi
  if [ "$source" = "dist" ]; then
    if [[ "$arch" == \<* ]]; then
      src_how="dist/ (file check skipped in dry-run)"
    else
      case "$(dist_binary_state "$arch")" in
        ok) src_how="dist/lanctl-linux-$arch" ;;
        missing) die "dist/lanctl-linux-$arch not found; run 'make cross' or use --source release" ;;
        stale) die "dist/lanctl-linux-$arch predates --check support; run 'make cross' or use --source release" ;;
      esac
    fi
  else
    if [ "$version" = "latest" ]; then
      src_how="release latest ($repo)"
    else
      src_how="release $version ($repo)"
    fi
    if command -v gh >/dev/null 2>&1; then
      src_how="$src_how via gh"
    elif command -v curl >/dev/null 2>&1; then
      src_how="$src_how via curl"
    else
      die "--source release needs gh or curl"
    fi
  fi

  # --- env passed to the remote install.sh (non-empty only) ---
  local env_args=""
  add_env() { if [ -n "$2" ]; then env_args+=" $(printf '%q' "$1=$2")"; fi; }
  add_env DEPLOY_DIR "$remote_dir"
  add_env PORT "$service_port"
  add_env RUN_USER "$run_user"
  add_env SERVICE_NAME "$service_name"
  add_env LANCTL_ALLOW_SHUTDOWN "$allow_shutdown"
  add_env LANCTL_LOCAL_HOST "$local_host"
  add_env CONFIG_GIT_REMOTE "$config_remote"
  add_env CONFIG_GIT_BRANCH "$config_branch"
  add_env SYNC_DELAY "$sync_delay"
  # Sync SSH identity is provisioned onto the target below; wire its paths.
  if [ -n "$git_key" ]; then
    [ -f "$git_key" ] || die "--git-key $git_key not found"
    add_env CONFIG_GIT_SSH_KEY "$remote_dir/.config-sync/ssh_key"
  fi
  if [ -n "$git_known" ]; then
    [ -f "$git_known" ] || die "--git-known-hosts $git_known not found"
    add_env CONFIG_GIT_KNOWN_HOSTS "$remote_dir/.config-sync/known_hosts"
  fi

  if [ "$dryrun" -eq 1 ]; then
    echo "target:  $ssh_user@$host (ssh port $ssh_port)"
    echo "arch:    $arch ($arch_how)"
    echo "source:  $src_how"
    echo "env:$env_args"
    if [ -n "$git_key" ]; then
      echo "keys:    $git_key -> $remote_dir/.config-sync/ssh_key (0600, $run_user)"
    fi
    if [ -n "$git_known" ]; then
      echo "keys:    $git_known -> $remote_dir/.config-sync/known_hosts (0644, $run_user)"
    fi
    echo "remote:  mktemp -d, stream bundle, sudo env … bash <tmp>/lanctl-linux-$arch/install.sh, verify, cleanup"
    return 0
  fi

  if [ "$yes" -eq 0 ]; then
    if [ -t 0 ]; then
      read -rp "Install lanctl ($arch, $source) on $ssh_user@$host:$remote_dir? [y/N] " ans || ans=""
      [[ "$ans" == [yY]* ]] || die "aborted"
    else
      die "refusing without -y when stdin is not a terminal"
    fi
  fi

  # --- fetch / assemble the bundle locally ---
  local bundle_dir="lanctl-linux-$arch" cleanup_local="" remote_tmp=""
  cleanup_all() {
    [ -n "${cleanup_local:-}" ] && rm -rf "$cleanup_local" 2>/dev/null || true
    [ -n "${remote_tmp:-}" ] && "${ssh_base[@]}" "rm -rf $remote_tmp" >/dev/null 2>&1 || true
  }
  trap cleanup_all EXIT
  if [ "$source" = "dist" ]; then
    stagedir="$(mktemp -d /tmp/lanctl-bundle.XXXXXX)"
    cleanup_local="$stagedir"
    assemble_dist_bundle "$arch" "$stagedir" \
      || die "dist/lanctl-linux-$arch not found; run 'make cross' or use --source release"
  else
    local file="lanctl-linux-$arch.tar.gz" dl
    dl="$(mktemp /tmp/lanctl-release.XXXXXX.tar.gz)"
    cleanup_local="$dl"
    if command -v gh >/dev/null 2>&1; then
      if [ "$version" = "latest" ]; then
        log "downloading $file (latest) via gh"
        gh release download -p "$file" -D "$(dirname "$dl")" --repo "$repo" --clobber \
          || die "release download failed"
        mv "$(dirname "$dl")/$file" "$dl"
      else
        log "downloading $file ($version) via gh"
        gh release download "$version" -p "$file" -D "$(dirname "$dl")" --repo "$repo" --clobber \
          || die "release download failed"
        mv "$(dirname "$dl")/$file" "$dl"
      fi
    else
      local base
      if [ "$version" = "latest" ]; then
        base="https://github.com/$repo/releases/latest/download"
      else
        base="https://github.com/$repo/releases/download/$version"
      fi
      log "downloading $base/$file via curl"
      curl -fsSL -o "$dl" "$base/$file" || die "release download failed"
    fi
    tarball="$dl"
  fi

  # --- ship it (streamed; nothing stored on either side) ---
  log "creating remote temp dir"
  # shellcheck disable=SC2029
  remote_tmp="$("${ssh_base[@]}" 'mktemp -d /tmp/lanctl-install.XXXXXX')" \
    || die "ssh to $host failed"
  log "streaming bundle to $host:$remote_tmp"
  if [ -n "$tarball" ]; then
    cat "$tarball" | "${ssh_base[@]}" "tar -xz -C $remote_tmp" || die "transfer failed"
    rm -f "$tarball"
  else
    tar -cz -C "$stagedir" "$bundle_dir" | "${ssh_base[@]}" "tar -xz -C $remote_tmp" || die "transfer failed"
    rm -rf "$stagedir"
  fi
  cleanup_local=""

  # --- install (sudo may need a tty for the password) ---
  local -a ssh_tty=()
  if ! "${ssh_base[@]}" 'sudo -n true' >/dev/null 2>&1; then
    if [ -t 0 ]; then
      ssh_tty=(-t)
    else
      log "warning: passwordless sudo unavailable and stdin is not a terminal; sudo may fail"
    fi
  fi

  # --- provision the sync SSH identity before install.sh runs ---
  if [ -n "$git_key" ] || [ -n "$git_known" ]; then
    log "provisioning sync SSH identity on $host"
    "${ssh_base[@]}" "${ssh_tty[@]}" \
      "sudo install -d -m 0700 -o $run_user -g $run_user $remote_dir/.config-sync" \
      || die "cannot create $remote_dir/.config-sync on $host"
    if [ -n "$git_key" ]; then
      scp -P "$ssh_port" "${ssh_opts[@]}" "$git_key" "$ssh_user@$host:$remote_tmp/git_ssh_key" \
        || die "key upload failed"
      "${ssh_base[@]}" "${ssh_tty[@]}" \
        "sudo install -o $run_user -g $run_user -m 0600 $remote_tmp/git_ssh_key $remote_dir/.config-sync/ssh_key" \
        || die "key install failed"
    fi
    if [ -n "$git_known" ]; then
      scp -P "$ssh_port" "${ssh_opts[@]}" "$git_known" "$ssh_user@$host:$remote_tmp/git_known_hosts" \
        || die "known_hosts upload failed"
      "${ssh_base[@]}" "${ssh_tty[@]}" \
        "sudo install -o $run_user -g $run_user -m 0644 $remote_tmp/git_known_hosts $remote_dir/.config-sync/known_hosts" \
        || die "known_hosts install failed"
    fi
  fi

  log "running install.sh on $host"
  # shellcheck disable=SC2029
  "${ssh_base[@]}" "${ssh_tty[@]}" "cd $remote_tmp/$bundle_dir && sudo env$env_args bash install.sh" \
    || die "remote install failed"

  log "verifying service"
  if "${ssh_base[@]}" "systemctl is-active $service_name" >/dev/null 2>&1; then
    log "$service_name is active on $host"
  else
    log "warning: $service_name is not active; check 'systemctl status $service_name' on $host"
  fi
  log "done (remote temp dir removed)"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  main "$@"
fi
