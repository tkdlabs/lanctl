# lanctl

Minimal LAN management tool — wake and shut down hosts via a browser.
Go port of the original `lanctl`, released as a standalone project.

## Architecture

Single Go HTTP server (standard library `net/http` + `ServeMux`).
Serves both the REST API and the static frontend (`frontend/index.html`,
`frontend/log.html`).

- Port: **8003** (`PORT` env overrides)
- Config: `hosts.yaml` — read from `$DEPLOY_DIR`, else the working directory
- Frontend: `frontend/*.html` — plain HTML/JS, no build step
- Binary: single static binary via `go build` (module `github.com/tkdlabs/lanctl`)

Packages:

- `internal/config` — YAML loading, host/VM lookup, SSH key cascade
- `internal/network` — WoL magic packet, SSH port checks
- `internal/sshops` — pooled SSH connect/run/stream (`golang.org/x/crypto/ssh`)
- `internal/localops` — local subprocess ops (systemctl, journalctl, shutdown)
- `internal/api` — HTTP handlers and the `operations` seam (see below)
- `internal/frontend`, `internal/httphandler`, `internal/version`

## Running

```bash
go run ./cmd/lanctl/
# or
make build && ./lanctl
# then open http://localhost:8003
```

## Testing

```bash
make test     # go test ./...
make vet
make cover
```

- `internal/api` tests use a **fake `operations` implementation** so no test ever
  touches a real host, service, or socket. Add new external calls to the
  `operations` interface in `internal/api/ops.go` and provide a fake.
- Real-system tests (systemctl/journalctl) live behind the `integration` build tag.
- Never write a test that can shut down, reboot, or reconfigure the test machine.
- `localops.Shutdown()` refuses to run unless `LANCTL_ALLOW_SHUTDOWN=1`.

## Deployment

- `install.sh` + `lanctl.service` install the binary and static frontend as a
  systemd unit (`DEPLOY_DIR`, default `/opt/lanctl`).
- `make cross` builds static linux binaries for `amd64`, `arm64`, and `arm`.
- Releases are cut by tagging `vX.Y.Z`; GitHub Actions cross-compiles and uploads
  the binaries.

## Secrets & private infrastructure

This repository is public. Real deployment details must never appear in tracked
files, commit messages, issues, or pull requests.

- Never include host names/FQDNs, IP or MAC addresses, SSH users or key paths,
  VPN/NordVPN tokens or meshnet hostnames, unit/service names, deploy paths,
  ports, or hardware details of real machines.
- Use documentation placeholders in every example, test, issue, and commit:
  RFC 5737 IPs (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`),
  `example.com` / `*.local`, and generic names such as `desktop`, `nas`, `myapp`.
- `hosts.yaml` and `.env` are gitignored and are the only place real inventory
  belongs; keep secrets in them or in the environment, never in tracked files.
- When fixing a live deployment, keep its hostnames, paths, and service names
  out of issues and commit messages — describe the change generically and keep
  the specifics in the private config.
- If private details are found in a commit, issue, or comment, remove them and
  report it; do not repeat them in the follow-up.

## Conventions

- Go: `gofmt`, `go vet`, `go mod tidy`, standard module layout
- File a GitHub issue (`gh issue create`) before starting any feature, bug fix,
  or design; reference its number in the commit message (e.g. `... (#12)`)
- Commit after the feature/fix is completed and verified, then `git push`
- Work on a single bug or feature or design unless strictly necessary
- Each commit corresponds to a single feature, bug fix, or design doc
- Add tests with each feature:
  - Unit tests for edge cases and thorough coverage
  - Integration tests for basic end-to-end scenarios (API as a black box)
  - More than 80% of tests should be unit tests
  - Backend coverage target: 80%+
- Feature requests: `features/` as separate `.md` files; rename to
  `done_<name>.md` when complete
- Bug reports: `bugs/` as separate `.md` files; rename to `done_<name>.md` when fixed
- Design docs: `design/` as `.md` files
