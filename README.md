# lanctl

Minimal LAN host manager. A single Go binary with an embedded-friendly web UI that
lets you wake, shut down, and monitor hosts and Proxmox VMs, tail systemd service
logs, and repair NordVPN meshnet connectivity — from your browser.

- **Zero runtime dependencies** — one static binary, no Python, no venv, no Node.
- **Web UI + REST API** on a single port (default `8003`).
- **Concurrency** — host and service status checks run in parallel.
- **SSH connection pooling** — reuse one SSH connection per host for all operations.
- **Cross-compiles** to `amd64`, `arm64`, and `arm` (Raspberry Pi).
- **Small** — ~9 MB static binary.

## Quick start

```bash
git clone https://github.com/tkdlabs/lanctl
cd lanctl
cp hosts.example.yaml hosts.yaml   # edit to add your hosts
go run ./cmd/lanctl/
# open http://localhost:8003
```

Or build a binary:

```bash
make build          # -> ./lanctl
./lanctl
```

`lanctl` reads `hosts.yaml` from `$DEPLOY_DIR`, falling back to the current
working directory. `PORT` overrides the listen port.

## Configuration

Copy [`hosts.example.yaml`](hosts.example.yaml) to `hosts.yaml` (gitignored) and
describe your machines:

```yaml
hosts:
  - name: desktop
    ip: 192.168.0.100
    mac: "AA:BB:CC:DD:EE:FF"
    ssh_user: tom
    services: [nginx, docker]

  - name: nas                 # Proxmox host
    type: proxmox
    ip: 192.168.0.10
    mac: "11:22:33:44:55:66"
    ssh_user: root
    vms:
      - name: nas-main
        vmid: 100
        ip: 192.168.0.200
        ssh_user: tom
        services: [lanctl.service]
```

- `local: true` marks the machine running `lanctl` itself (skips SSH).
- `mac` is required for Wake-on-LAN; `ip` is used for reachability checks.
- `ssh_key` can be set globally or per host/VM (default `~/.ssh/id_ed25519`).
- `nordvpn_token` enables the VPN repair action (or set per host).

See `hosts.example.yaml` for the full annotated schema.

## Install as a service

On a Linux host with systemd:

```bash
make cross                                   # optional: build dist/ binaries
sudo ./install.sh
sudo nano /opt/lanctl/hosts.yaml
sudo systemctl restart lanctl
```

`install.sh` installs the binary and frontend to `DEPLOY_DIR` (default
`/opt/lanctl`), writes an `EnvironmentFile`, installs `lanctl.service`, and
starts it. Override with environment variables, e.g.
`sudo DEPLOY_DIR=/srv/lanctl PORT=8080 ./install.sh`.

### Raspberry Pi

```bash
make cross
# copy dist/lanctl-linux-arm64 (Pi 4/5, 64-bit OS) or
#      dist/lanctl-linux-arm   (32-bit Raspbian) to the Pi, then:
sudo ./install.sh
```

## REST API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/version` | Build version |
| GET | `/api/hosts` | List hosts with online/service/VPN status |
| POST | `/api/hosts/{name}/wake` | Send Wake-on-LAN magic packet |
| POST | `/api/hosts/{name}/shutdown` | Shut down host (local or SSH) |
| GET | `/api/hosts/{name}/services/{svc}/logs` | Last N journal lines |
| GET | `/api/hosts/{name}/services/{svc}/logs/stream` | SSE journal follow |
| POST | `/api/hosts/{name}/services/{svc}/{action}` | `start` / `stop` / `restart` |
| POST | `/api/hosts/{name}/vpn-repair` | NordVPN repair stream (SSE) |
| POST | `/api/hosts/{name}/vms/{vm}/shutdown` | Shut down Proxmox VM |
| POST | `/api/hosts/{name}/vms/{vm}/vpn-repair` | VM NordVPN repair stream |
| GET | `/api/hosts/{name}/vms/{vm}/services/{svc}/logs` | VM journal lines |
| GET | `/api/hosts/{name}/vms/{vm}/services/{svc}/logs/stream` | VM SSE journal |
| POST | `/api/hosts/{name}/vms/{vm}/services/{svc}/{action}` | VM service control |

## Security

`lanctl` performs privileged operations (systemd control, journal access,
shutdown) on the machines it manages.

- **Local shutdown is disabled by default.** Set `LANCTL_ALLOW_SHUTDOWN=1`
  (written by `install.sh`) to allow `lanctl` to power off the machine it runs on.
- Run the service as a user with the necessary rights. The simplest setup runs as
  `root`. A least-privilege alternative is a dedicated user with passwordless
  `sudo` for `systemctl`, `journalctl`, and `shutdown`.
- Remote hosts are controlled over SSH with public-key auth; `lanctl` uses
  `InsecureIgnoreHostKey` to match a trust-on-first-use LAN workflow. Treat
  `hosts.yaml` as sensitive and keep it mode `0600`.

## Development

```bash
make test     # go test ./...
make vet      # go vet ./...
make cover    # coverage summary
make fmt      # gofmt -w .
```

Real-system tests (systemctl/journalctl) are gated behind a build tag:

```bash
go test -tags=integration ./internal/localops/
```

## License

MIT — see [LICENSE](LICENSE).
