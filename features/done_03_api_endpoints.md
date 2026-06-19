# Feature 03: API Endpoints

## Summary

Implement all REST API endpoints to reach feature parity with the Python lanctl backend.

## Endpoints implemented

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/hosts` | List all hosts with online/service/VPN status (concurrent) |
| POST | `/api/hosts/{name}/wake` | Send WoL magic packet |
| POST | `/api/hosts/{name}/shutdown` | Shutdown host (local or SSH) |
| GET | `/api/hosts/{name}/services/{service}/logs` | Last N journal lines |
| GET | `/api/hosts/{name}/services/{service}/logs/stream` | SSE journal follow |
| POST | `/api/hosts/{name}/services/{service}/{action}` | start/stop/restart service |
| POST | `/api/hosts/{name}/vpn-repair` | NordVPN repair stream |
| POST | `/api/hosts/{name}/vms/{vm}/shutdown` | Shutdown Proxmox VM |
| GET | `/api/hosts/{name}/vms/{vm}/services/{service}/logs` | VM journal lines |
| GET | `/api/hosts/{name}/vms/{vm}/services/{service}/logs/stream` | VM SSE stream |
| POST | `/api/hosts/{name}/vms/{vm}/services/{service}/{action}` | VM service control |

## New packages

- `internal/network` — WoL magic packet (UDP broadcast), SSH port check
- `internal/sshops` — SSH connect/run/stream via `golang.org/x/crypto/ssh`
- `internal/localops` — Local subprocess operations (systemctl, journalctl)
- `internal/api` — HTTP handlers, concurrent host/VM status checks

## Status

Done. Tests: 43 tests across 4 packages. Coverage: api 80.6%, config 94.7%, network 82.6%.
