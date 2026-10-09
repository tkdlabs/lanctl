# Feature 10: Remote Install over SSH

Issue: #13.

## Problem

`install.sh` is local-only: it requires root and systemd on the machine it
runs on. Installing on remote boxes is manual ssh + scp + run. The pre-port
`deploy.py` had `--host/--user/--arch` remote deployment; the Go port dropped
it.

## Design

New `deploy/install-remote.sh`. Constraints from the start: **no Go toolchain
anywhere** (deploying machine or target) and **minimal disk footprint** on
both sides.

### Sources

`--source dist|release|auto` (default `auto` → `dist/` when the needed arch
binary exists, else the GitHub Releases tarball).

- `dist`: assembles the exact release-tarball layout from the checkout
  (`lanctl` + `lanctl-cli` binaries, `frontend/`, `deploy/`,
  `lanctl.service`, `hosts.example.yaml`, `install.sh`).
- `release`: downloads `lanctl-linux-<arch>.tar.gz` for `--version`
  (default `latest`) from `--repo` (default `tkdlabs/lanctl`), via `gh`
  when present else `curl` on the public asset URL.

### Transfer

The bundle is **streamed** (`tar | ssh ... tar -x`) into a remote `mktemp`
dir — no tarball stored on either side. Peak remote disk is the extracted
tree (~10 MB in `/tmp`), removed by trap on success or failure. Nothing is
cloned on the target.

### Execution

`ssh <target> sudo env <non-empty vars> bash <tmp>/install.sh`:

- Pass-through: `DEPLOY_DIR`, service `PORT`, `RUN_USER`,
  `LANCTL_LOCAL_HOST`, `CONFIG_GIT_REMOTE`, `CONFIG_GIT_BRANCH`,
  `SYNC_DELAY` — so one command sets per-box identity and sync in place.
- Arch defaults to auto-detect (`ssh <target> uname -m` → `amd64|arm64|arm`,
  same mapping as `install.sh detect_cli`); `--arch` overrides.
- Post-install `systemctl is-active` check over the same transport.

### Safety

- Confirmation prompt unless `-y`; `--dry-run` prints the full plan
  (arch, source, env, remote commands) with zero side effects.
- Never touches an existing remote `hosts.yaml` (`install.sh` preserves it).
- Fits the tarball-first direction of `features/09_packaging.md`.

## Acceptance criteria

- [ ] No Go toolchain required on either side.
- [ ] `--dry-run` performs no ssh writes and no downloads.
- [ ] Streamed transfer leaves no bundle files behind on success.
- [ ] Auto-detect maps `x86_64`/`aarch64`/`armv7l` correctly; unknown arch is
      a clear error.
- [ ] Missing `dist/` binary errors with a pointer to `make cross` or
      `--source release`.

## Status

Done (#13).
