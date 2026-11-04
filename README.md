# lanctl

Minimal LAN host manager. A single Go binary with an embedded-friendly web UI that
lets you wake, shut down, and monitor hosts and Proxmox VMs, control Hubspace
smart plugs, tail systemd service logs, and repair NordVPN meshnet connectivity —
from your browser.

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
- `nordvpn_token` enables the VPN repair action (or set per host). It can also
  be supplied as `NORDVPN_TOKEN` in the environment; the environment wins over
  `hosts.yaml`, so a shared config can omit the token.
- `services` are system units (`systemctl`); `user_services` are systemd user
  units (`systemctl --user`). For remote hosts these run in the SSH user's
  user manager, so that user needs lingering enabled
  (`loginctl enable-linger <user>`). For `local: true` hosts they run in the
  lanctl process user's manager. Status, logs, and start/stop/restart all
  respect the scope; logs are read with `journalctl --user`.

> **SSH keys are read as the lanctl process user, not as you.** `ssh_key` (and
> the `~` inside it) is resolved by the running process. Installed as a systemd
> service it runs as `RUN_USER` (`root` by default), so the default
> `~/.ssh/id_ed25519` means `/root/.ssh/id_ed25519` — a different file from your
> login shell's key, and the reason a warning can appear even though *your*
> keyless SSH works. That user must own or be able to read the key, and its
> public key must be authorized for each host's `ssh_user`. To reuse your own
> key, point `ssh_key` at it explicitly (e.g. `/home/tom/.ssh/id_ed25519`); to
> follow the shared-config model, provision a key in the service user's home on
> every box instead. The same rule is described for the config repo under
> "Sync SSH access".

See `hosts.example.yaml` for the full annotated schema.

### Smart plugs (Hubspace / Home Depot)

Phase 1 supports manual power control for Hubspace power outlets. Plugs are
first-class: a plug may power no managed host (for example a TV), one host, or
several hosts.

```yaml
config_version: 3

hubspace:
  username: user@example.com
  password: "..."

plugs:
  - name: tv                    # local lanctl name
    provider: hubspace          # optional; defaults to hubspace
    device_name: "Living Room TV"
    power_off_seconds: 5

  - name: nas
    device_name: "NAS plug"     # or a stable device_id
    power_off_seconds: 30
    affects: [nas]              # optional host dependencies

hosts:
  - name: nas
    type: proxmox
    ip: 192.0.2.10
    mac: "AA:BB:CC:DD:EE:FF"
    ssh_user: admin
```

`affects` is display/safety metadata: it names the hosts that depend on the
plug. It does not make `shutdown` or `wake` touch the plug automatically. A
plug may affect several hosts (`affects: [nas, desktop]`), and a host may have
several plugs.

`HUBSPACE_USERNAME` and `HUBSPACE_PASSWORD` override the YAML values. The
Hubspace API is unofficial and cloud-based; the plug needs internet access.
`cycle` is a hard power cut, not a graceful reboot. On/Off/Cycle confirmations
name the affected hosts; `cycle` always confirms. The native host watchdog is
not part of this feature. `lanctl-cli plug cycle` waits for the turn-on and
allows at least two minutes; raise `--timeout` if `power_off_seconds` is longer.

Legacy v2 configs with `hosts[].plug` still load: they are converted to a plug
named after the host with `affects: [host]`.

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
starts it. Set the directory with `--dir` (or `DEPLOY_DIR`), and any other
setting with environment variables, e.g.
`sudo ./install.sh --dir /srv/lanctl` or
`sudo DEPLOY_DIR=/srv/lanctl PORT=8080 ./install.sh`. Unknown flags are
rejected, so a typo can't silently fall back to the default directory.

Secrets such as `NORDVPN_TOKEN`, `HUBSPACE_USERNAME`, and `HUBSPACE_PASSWORD`
can come from the environment instead of `hosts.yaml`; `install.sh` writes them
to the mode-`0600` `.env`, quoted for systemd, and keeps them across reinstalls.
For a remote install, put them in a local file and pass `--env-file FILE` — the
file is uploaded over SSH and appended to the target's `.env`, so the values
never appear in process arguments or dry-run output.

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
lanctl-sync.timer`. `lanctl-sync.sh status` shows the last sync state
(`ok`, `failed`, `pending`, `held`, `aborted` with rev and time).
`hosts.yaml.last-good` is kept as a backup on every apply.

The web UI surfaces the same state as a banner, and `GET /api/notices`
returns it as JSON: config path/age, whether the running config loads, the
last sync result and how long ago it succeeded, and leveled notices for
problems (invalid config, failed/held/aborted sync, stale config, a missing
`nordvpn_token`, or SSH keys that do not exist). A successful sync older than
`LANCTL_STALE_AFTER` seconds (default 3600) is reported stale.

Note: a manual `systemctl start lanctl-sync.service` blocks until the run
finishes, including the 5-minute apply delay — this is the safety window
working, not a hang. Watch progress with `journalctl -f -u lanctl-sync`,
or start with `systemctl start --no-block lanctl-sync.service`.

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
| GET | `/api/notices` | Self-diagnostics: config health and sync freshness |
| GET | `/api/hosts` | List hosts with online/service/VPN status and associated plugs |
| POST | `/api/hosts/{name}/wake` | Send Wake-on-LAN magic packet |
| POST | `/api/hosts/{name}/shutdown` | Shut down host (local or SSH) |
| GET | `/api/hosts/{name}/services/{svc}/logs` | Last N journal lines |
| GET | `/api/hosts/{name}/services/{svc}/logs/stream` | SSE journal follow |
| POST | `/api/hosts/{name}/services/{svc}/{action}` | `start` / `stop` / `restart` |
| POST | `/api/hosts/{name}/vpn-repair` | NordVPN repair stream (SSE) |
| GET | `/api/plugs` | All configured smart plugs and state |
| GET | `/api/plugs/{name}` | One smart plug's state |
| POST | `/api/plugs/{name}/{on\|off\|cycle}` | Control a smart plug |
| GET | `/api/hubspace/devices` | Discover Hubspace power outlets |
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
lanctl-cli plugs
lanctl-cli plugs --host nas
lanctl-cli plug status tv
lanctl-cli plug on tv
lanctl-cli plug off tv -y
lanctl-cli plug cycle nas -y
lanctl-cli hubspace devices
lanctl-cli version
```

Host targets are `HOST` or `HOST/VM`; plug commands take a plug `NAME`. The
server address comes from `--server`/`-s`, then `LANCTL_URL`, defaulting to
`http://localhost:8003`. A scheme is optional, so `LANCTL_URL=rpi.local:8004`
implies `http://`:

```bash
export LANCTL_URL=rpi.local:8004   # e.g. add to ~/.bashrc
lanctl-cli hosts
```

Global flags: `-o/--output table|json|plain`, `--timeout`, `-q/--quiet`,
`--no-color`, `-y/--yes`. Exit codes: `0` success, `1` error, `2` usage.
`shutdown`, `plug off`, and `plug cycle` prompt for confirmation unless
`--yes` or JSON output is used; `plug on` also prompts when the plug lists
affected hosts. When stdin is not a terminal they refuse without `--yes`.

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
- Hubspace credentials can control every device on that account, not just the
  configured plugs. Keep them in the mode-`0600` config or EnvironmentFile, and
  prefer a dedicated account if practical. The Hubspace API is unofficial and
  can change without notice.

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
