# Feature 08: Notices / Self-Diagnostics

Issue: #19 (follow-up to #12).

## Summary

Config sync (feature 07) writes machine-readable state to
`$DEPLOY_DIR/.config-sync/state.json` (plus a `FAILED` file and a
`SYNC_DISABLED` hold sentinel), but nothing read it. The web UI could not show
that a box's `hosts.yaml` was stale, that the last sync failed/held/aborted, or
that the running config was degraded (missing token, missing SSH keys).

`GET /api/notices` now exposes a local, filesystem-only self-diagnostic report,
and the host page renders it as a banner. This keeps the shell decoupled from
the API while giving the UI a catch for sync and config problems.

## Report

`internal/notices.Build(version, now)` returns a leveled report and never
contacts a host, service, or network:

- `status` — `ok`, `degraded` (any warning), or `error` (any error);
- `config_path`, `config_ok`, `config_error`, `host_count`, `local_host`,
  `config_age_seconds` (file mtime);
- `sync` — `status`, `rev`, `at`, `age_seconds`, `detail` (from `FAILED`);
- `notices` — each with `level` (`info`/`warning`/`error`), a stable `code`,
  and a human `message`.

## Checks

| Code | Level | Trigger |
|------|-------|---------|
| `config_invalid` | error | `config.Load` fails (missing/unknown key, bad version, no hosts) |
| `missing_vpn_token` | warning | a host/VM declares `nordvpn_hostname` but `nordvpn_token` is unset |
| `missing_ssh_key` | warning | a remote host/VM's resolved SSH key file does not exist |
| `sync_held` | warning | the `SYNC_DISABLED` sentinel exists |
| `never_synced` | warning | `CONFIG_GIT_REMOTE` is set but no `state.json` exists |
| `sync_state_unreadable` | warning | `state.json` is not valid JSON |
| `sync_stale` | warning | last `ok` sync older than `LANCTL_STALE_AFTER` (default 3600s) |
| `sync_failed` | error | sync state `failed`, or a `FAILED` file is present |
| `sync_pending` | info | validated config waiting out the apply delay |
| `sync_held_state` / `sync_aborted` | warning | last run held/aborted during the delay |
| `sync_unknown` | warning | an unrecognized state value |

## Implementation

- `internal/config` — exported `Path()` (resolved `hosts.yaml`) and
  `ExpandTilde`.
- `internal/notices` — report builder, checks, `LANCTL_STALE_AFTER` override.
- `internal/api` — `GET /api/notices` handler.
- `frontend/index.html` — notices banner on the host page, refreshed each poll.

## Status

Done. Unit tests cover every code path with a temp `DEPLOY_DIR`; the handler
is tested through the API mux. No test touches a real host, service, or socket.
