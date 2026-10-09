# Feature 07: Config Sync Across Boxes

Issue: #12.

## Problem

lanctl runs on multiple boxes in a LAN. Keeping each box's `hosts.yaml` in
sync is manual (scp), and the file is not identical across boxes: `local: true`
marks the machine lanctl runs on, so a bit-for-bit shared file is wrong for all
but one box. The file is also secret (MACs, IPs, SSH users, NordVPN token) and
must never appear in tracked files.

## Design

One shared inventory, byte-identical on every box, plus a per-box identity.
Distribution via a private git config repo; application via a sync script with
a delay and layered kill switches. Binary updates are explicitly out of scope
(see `features/09_packaging.md`).

### Key insight

Handlers call `config.Load()` fresh on every request, so replacing
`$DEPLOY_DIR/hosts.yaml` takes effect on the next request with **no restart**.

### 1. Per-box identity via env

New env var `LANCTL_LOCAL_HOST`: a host `name`, or `host/vm` to mark a VM
local. Applied in `config.Load()` after YAML parse:

- set `Local=true` on the match, clear `Local` on all others;
- unknown name → hard error (a typo must not silently disable local ops);
- unset → today's behavior (YAML `local:` fields honored, backward compatible).

The shared `hosts.yaml` therefore omits `local:` entirely. Each box's delta
lives in its `$DEPLOY_DIR/.env` (already per-box, mode `0600`).

`ssh_user` stays per-host/VM in the shared file: it describes how to reach the
*target*, so it is the same value from every box. For file identity, omit
per-host `ssh_key` in favor of the global default (`~/.ssh/id_ed25519`),
which resolves identically when lanctl runs as the same user (root) on all
boxes. SSH key distribution itself is out of scope.

### 2. Schema safety

Plain `yaml.Unmarshal` silently drops unknown fields, so a `hosts.yaml`
written for a newer binary would be misread (e.g. a renamed field yields an
empty value and SSH fails at runtime). Two guards:

- **Strict decoding** with `yaml.Decoder.KnownFields(true)`: unknown/renamed
  keys fail loudly. Side effect by design — an older binary *rejects* configs
  using newer keys instead of guessing.
- **`config_version:`** (absent/0 = 1) plus a `SupportedConfigVersion`
  constant in the binary. If `cfg.ConfigVersion > SupportedConfigVersion`,
  `Load` returns a distinct error naming both versions and telling the
  operator to upgrade lanctl first. Catches same-key/different-meaning
  changes that strictness cannot.

### 3. Validation CLI

`lanctl --check [--config PATH]`: load, print OK or the error, exit 0/1, no
server. Schema/version errors are surfaced verbatim. Used by the sync as the
pre-apply gate (and handy for CI).

### 4. Sync script (`deploy/`)

`deploy/lanctl-sync.sh` with subcommands `run|hold|resume|status`:

1. `run`: exit 0 if the `$DEPLOY_DIR/SYNC_DISABLED` sentinel exists.
2. `git fetch` a mirror under `$DEPLOY_DIR/.config-sync/`, then
   `git show FETCH_HEAD:hosts.yaml`.
3. No-op when identical to the current file; otherwise stage and
   `lanctl --check --config <staged>`.
4. On failure: keep last-known-good, write `.config-sync/FAILED` and a
   machine-readable `.config-sync/state.json`, log to the journal, exit
   non-zero. **Never swap a failing config.**
5. On success: `sleep $SYNC_DELAY` (default 300s) with a SIGTERM trap,
   re-check the sentinel, back up to `hosts.yaml.last-good`, atomic `mv`,
   record the applied rev in `state.json`. No restart.

`lanctl-sync.service` (oneshot, `EnvironmentFile`, `TimeoutStartSec=900`,
`KillMode=mixed`) + `lanctl-sync.timer` (`OnBootSec=2min`,
`OnUnitActiveSec=15min`, `Persistent=true`).

**Layered kill switches:** `lanctl-sync hold` (sentinel), `systemctl stop`
during the delay (SIGTERM aborts pre-apply), `systemctl mask
lanctl-sync.timer`.

### 5. Rollout

1. Private config repo holds `hosts.yaml` (mode `0600`), `local:` removed,
   `ssh_user` on every host (including self).
2. Each box: `LANCTL_LOCAL_HOST=<self>` and `CONFIG_GIT_REMOTE=<url>` in
   `.env`; install `deploy/` script + units; enable the timer.
3. Verify staging/apply, then test `hold` before `resume`.

Real remotes, hostnames, IPs, and tokens live only in per-box `.env` and the
private config repo — never in tracked files, examples, commits, or issues.

## Acceptance criteria

- [ ] Identical `hosts.yaml` + different `LANCTL_LOCAL_HOST` gives each box
      its own correct local host.
- [ ] Unknown YAML key fails `Load`/`--check`.
- [ ] `config_version` newer than the binary fails with an upgrade message.
- [ ] Bad staged config never replaces last-known-good; failure is visible
      (`state.json`, journal, non-zero exit).
- [ ] `hold` blocks; stopping during the delay aborts pre-apply.
- [ ] Config application requires no lanctl restart.

## Status

In progress (#12).
