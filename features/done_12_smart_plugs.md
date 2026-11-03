# Feature 12: Hubspace Smart Plugs

Issue: #25.

## Problem

lanctl can wake a host and shut it down over SSH, but it cannot switch the
power itself. A smart plug is useful when a host is unresponsive (hard power
cycle) or when its power should be controlled independently.

Phase 1 adds manual status/on/off/cycle control for Hubspace (Home Depot /
Afero) power outlets. The native host watchdog and local BLE control are
separate efforts.

## Design

### Config (schema v2)

```yaml
config_version: 2

hubspace:
  username: user@example.com
  password: "..."
  # HUBSPACE_USERNAME / HUBSPACE_PASSWORD override these when set.

hosts:
  - name: nas
    type: proxmox
    ip: 192.0.2.10
    mac: "AA:BB:CC:DD:EE:FF"
    ssh_user: admin
    plug:
      provider: hubspace        # optional; defaults to hubspace
      device_name: "nas-plug"   # or a stable device_id
      power_off_seconds: 30     # default 30
```

- `SupportedConfigVersion` becomes 2. Version 0/1 configs still load; a v3
  config is rejected with the existing upgrade message.
- A plug must name a device (`device_id` or `device_name`). `provider` must be
  empty or `hubspace`.
- Missing credentials are not a load error: a shared config may be used on a
  box that does not need plugs. The plug endpoints return a clear error and
  `notices` warns.
- Env overrides are applied after YAML decoding, so the environment wins.

### Go client

`internal/hubspace` implements the subset of the Hubspace/Afero protocol needed
for power outlets:

1. Keycloak OIDC login with PKCE against the `hubspace_android` client,
   using a cookie jar and no redirects. The login form action is parsed from
   the HTML, not reconstructed.
2. `/v1/users/me` to discover account IDs.
3. `/v1/accounts/{id}/metadevices?expansions=state` to discover devices and
   their functions. Metadata calls use the `semantics2.afero.net` Host header.
4. `GET` the device state and `PUT` the `power` category function with
   `on`/`off` and a Unix-ms `lastUpdateTime`.
5. Re-authenticate once on a 401. Use the real `expires_in`; do not assume a
   long token lifetime.

The package has no lanctl imports and uses injectable endpoint URLs so tests
run against a fake Keycloak/Afero server.

`Cycle` turns the plug off, waits `power_off_seconds`, then turns it on using
a detached context. If the off request fails ambiguously, it makes a best-effort
attempt to restore power. The error clearly says when the plug may be off.

### API

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/plugs` | Configured plugs and current state |
| GET | `/api/hosts/{name}/plug` | One host's plug status |
| POST | `/api/hosts/{name}/plug/on` | Turn the plug on |
| POST | `/api/hosts/{name}/plug/off` | Turn the plug off |
| POST | `/api/hosts/{name}/plug/cycle` | Off, wait, on |
| GET | `/api/hubspace/devices` | Discover controllable outlets |

- Hubspace calls go through the `operations` seam and are faked in tests.
- `/api/hosts` returns plug metadata but not live state, so the 10s host poll
  never contacts Hubspace.
- Plug state is fetched separately by the UI at a slower cadence.
- A per-host lock returns 409 if a plug operation is already running.

### CLI/UI

```
lanctl-cli plugs
lanctl-cli plug status HOST
lanctl-cli plug on HOST
lanctl-cli plug off HOST -y
lanctl-cli plug cycle HOST -y
lanctl-cli hubspace devices
```

`off` and `cycle` confirm unless `--yes`/JSON. `cycle` uses a longer timeout
because the server waits for the turn-on.

The UI shows the plug name/state and On/Off/Cycle buttons for configured hosts,
with confirmations for Off/Cycle. A Hubspace failure leaves the host table
usable and shows `unknown` for state.

### Notices

`/api/notices` warns when a plug is configured but Hubspace credentials are
unresolved. It stays local-only and never contacts Hubspace.

## Non-goals

- Native watchdog loop (future phase).
- BLE/local control (separate reverse-engineering effort).
- Lights, fans, locks, timers, or other Hubspace device classes.
- Automatically cutting the plug after `shutdown`.

## Acceptance criteria

- [x] `lanctl --check` accepts v2 + plug config and rejects bad provider/missing
      device refs.
- [x] `HUBSPACE_USERNAME`/`HUBSPACE_PASSWORD` override YAML; `--check` works
      without credentials on a shared config.
- [x] `GET /api/plugs` and the per-host status/action routes work against fake
      ops, including unknown host/device, missing credentials, and invalid
      action.
- [x] A canceled cycle still turns the plug back on (fake-ops test).
- [x] CLI commands and JSON/table output are covered by unit tests.
- [x] UI renders controls only for configured hosts and confirms Off/Cycle.
- [x] No credentials or tokens appear in logs, errors, JSON, or tracked files.
- [x] `make test`, `make vet`, and `make cover` pass; live verification is
      status-only against the real plug.

## Status

Done (#25). Verified against the live plug for login, device discovery, and
state reads; no live power-cycle was performed.
