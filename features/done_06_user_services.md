# Feature 06: systemd User Services

## Summary

Support services that live in a systemd **user** instance (`systemctl --user`)
alongside the existing system-wide services, for both hosts and Proxmox VMs.
Previously every service was controlled with `systemctl` and read with
`journalctl`, so user units (common for personal backends such as
`myapp-backend`) could not be monitored or controlled.

## Configuration

Each host and VM gains an optional `user_services:` list next to `services:`:

```yaml
hosts:
  - name: desktop
    ssh_user: tom
    services: [nginx]              # system units
    user_services: [myapp-backend] # systemd --user units
```

- On remote hosts, user services belong to the **SSH user's** user manager.
  The SSH user needs lingering (`loginctl enable-linger <user>`) or an active
  session; remote commands are prefixed with
  `XDG_RUNTIME_DIR=/run/user/$(id -u)` so `systemctl --user` can find the bus.
- On `local: true` hosts, they belong to the **lanctl process user's** user
  manager.
- A name in both lists resolves to the user scope.

## Behavior

| Operation | System service | User service |
|-----------|----------------|--------------|
| Status | `systemctl is-active` | `systemctl --user is-active` |
| Control | `sudo systemctl <action>` (local/SSH) | `systemctl --user <action>` (no sudo) |
| Logs | `journalctl -u` | `journalctl --user -u` |

API routes are unchanged: scope is resolved from `hosts.yaml` by service name.
`GET /api/hosts` now includes a `user_services` array per host/VM and merges
both scopes into `service_statuses`. The web UI marks user services with a
`user` badge; `lanctl-cli` appends `(user)` in table/plain output.

## Implementation

- `internal/config` — `UserServices` fields, `FindService`/`FindVMService`
  returning `ScopeSystem` or `ScopeUser`.
- `internal/localops` — `--user` support plus `XDG_RUNTIME_DIR` defaulting via
  `runUser`.
- `internal/sshops` — command builders for `systemctl`/`journalctl` with
  `--user` and the remote runtime dir prefix.
- `internal/api` — scope threaded through the `operations` seam; status checks
  query each scope and merge results.
- `internal/client`, `cmd/lanctl-cli`, `frontend` — expose and render the scope.

## Status

Done. Unit tests cover config lookup, command construction for both scopes
(local and SSH), API routing, and client decoding.
