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
- **Live config** — `hosts.yaml` is re-read on every request, so adding,
  editing, or removing a host takes effect immediately with no restart.
  (`PORT` and other process-level settings still need a restart.)

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
    user_services: [myapp-backend]

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
        user_services: [myapp.service]
```

- `local: true` marks the machine running `lanctl` itself (skips SSH).
- `mac` is required for Wake-on-LAN; `ip` is used for reachability checks.
- `ssh_key` can be set globally or per host/VM (default `~/.ssh/id_ed25519`).
- `nordvpn_token` enables the VPN repair action (or set per host).
- `services` are system units (`systemctl`); `user_services` are systemd user
  units (`systemctl --user`). For remote hosts these run in the SSH user's
  user manager, so that user needs lingering enabled
  (`loginctl enable-linger <user>`). For `local: true` hosts they run in the
  lanctl process user's manager. Status, logs, and start/stop/restart all
  respect the scope; logs are read with `journalctl --user`.

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

### Remote install

From any machine with SSH access to the target — no Go toolchain needed on
either side:

```bash
# From local dist/ builds (run `make cross` first)
deploy/install-remote.sh --host mybox --user operator --local-host mybox -y

# Or straight from the latest GitHub release (nothing to build or store)
deploy/install-remote.sh --host mybox --arch arm64 --source release -y
```

The target arch is auto-detected (`--arch` overrides), the bundle is streamed
over SSH into a remote temp dir that is removed afterwards, and `install.sh`
runs remotely under sudo. Binaries come from a fresh local `dist/` when
available, else the latest release (explicit `--source dist` with a stale
`dist/` fails fast — run `make cross` to refresh it). Add `--config-remote
<url>` to enable config sync in the same step. `--dry-run` previews the plan
with zero side effects; `--help` lists all options (custom dirs, ports,
service user, release version, extra ssh options).

## Keeping multiple boxes in sync

Running lanctl on several boxes? Keep one shared `hosts.yaml` in a private
git repo and let each box pull it — the file stays byte-identical everywhere:

1. In the shared file, omit `local:` and give every host (including self) an
   `ssh_user`. Prefer the global/default `ssh_key` so key paths resolve
   identically on all boxes.
2. On each box, set its identity in `$DEPLOY_DIR/.env`:
   `LANCTL_LOCAL_HOST=<this box's host name>` (or `<host>/<vm>`). The shared
   file's entries are marked local/cleared per box at load time.
3. Point the box at the private repo and install:
   `sudo CONFIG_GIT_REMOTE=git@config-repo.local:~/git-repos/lanctl-config.git
   LANCTL_LOCAL_HOST=mybox ./install.sh`
   This installs `lanctl-sync.sh` plus a timer that polls every 15 minutes.

Each poll fetches, validates with `lanctl --check`, waits 5 minutes, then
atomically swaps the file — no restart needed (see Live config above). A bad
config (unknown keys, or a `config_version` newer than the binary) is rejected
and the last-known-good file keeps serving. Validate manually any time:

```bash
lanctl --check                        # current file
lanctl --check --config /path/to/hosts.yaml
```

Emergency brakes: `lanctl-sync.sh hold` (sentinel file), `systemctl stop
lanctl-sync` during the delay window, or `systemctl mask
lanctl-sync.timer`. `lanctl-sync.sh status` shows the last sync state.
`hosts.yaml.last-good` is kept as a backup on every apply.

If a run reports the validator `lacks --check support`, the box's `lanctl`
binary predates `v0.2.0` — refresh it (`install-remote.sh --source release`)
and re-run; the staged config is left untouched.

Treat the shared file as secret (MACs, IPs, SSH users, tokens): private repo
only, mode `0600`, never in tracked files.

### Sync SSH access

The sync runs as the service user (`root` by default), so that user needs its
own SSH identity for the config repo — your user's key is not enough:

```bash
sudo -i
ssh-keygen -t ed25519 -N "" -f ~/.ssh/id_config_sync
# authorize the pubkey on the repo host (read-only deploy key), then:
ssh-keyscan config-repo.local >> ~/.ssh/known_hosts
```

Point the sync at them in `$DEPLOY_DIR/.env`:

```
CONFIG_GIT_SSH_KEY=/root/.ssh/id_config_sync
CONFIG_GIT_KNOWN_HOSTS=/root/.ssh/known_hosts
```

Without these, an SSH remote fails fast with a message naming the missing
setting instead of a cryptic git error. `install-remote.sh` provisions both
files in one step (`--git-key` / `--git-known-hosts`).

On the box hosting the repo itself, skip SSH entirely — set
`CONFIG_GIT_REMOTE` to the plain local path (e.g.
`/home/sync/git-repos/lanctl-config`); no key or `known_hosts` is needed.
Cross-user ownership (service user vs. repo owner) is handled automatically:
each run allowlists the remote in the service user's gitconfig.

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

## CLI

`lanctl-cli` is a small shell client for the REST API. It talks to a running
server over HTTP; it does not read `hosts.yaml` and performs no SSH itself.
Build it with `make build-cli` (installed to `/usr/local/bin` by `install.sh`).

```bash
lanctl-cli hosts                       # table of hosts and VMs
lanctl-cli hosts --online -o json      # JSON for scripting
lanctl-cli wake desktop                # Wake-on-LAN
lanctl-cli shutdown nas/nas-main -y    # shut down a VM without prompting
lanctl-cli service nas/nas-main nginx restart
lanctl-cli logs desktop lanctl.service -f        # follow (SSE)
lanctl-cli logs desktop lanctl.service -n 200    # last 200 lines
lanctl-cli vpn-repair desktop
lanctl-cli version
```

Targets are `HOST` or `HOST/VM`. The server address comes from `--server`/
`-s`, then `LANCTL_URL`, defaulting to `http://localhost:8003`. A scheme is
optional, so `LANCTL_URL=rpi.local:8004` implies `http://`:

```bash
export LANCTL_URL=rpi.local:8004   # e.g. add to ~/.bashrc
lanctl-cli hosts
```

Global flags: `-o/--output table|json|plain`, `--timeout`, `-q/--quiet`,
`--no-color`, `-y/--yes`. Exit codes: `0` success, `1` error, `2` usage.
`shutdown` prompts for confirmation unless `--yes` or JSON output is used;
when stdin is not a terminal it refuses without `--yes`.

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
