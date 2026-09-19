# Design: `lanctl-cli` — shell client for lanctl

Status: proposed

## Context

`lanctl` is a single Go HTTP server exposing a REST API plus two SSE streams.
The web UI (`frontend/*.html`) is currently the only client. There is no way to
drive `lanctl` from a shell, a script, or a cron job without hand-rolling
`curl` with the right paths and JSON parsing.

This document specifies `lanctl-cli`, a thin command-line client that talks to
the existing API. It owns no configuration and performs no SSH/local
operations itself — it is purely an HTTP client. All privileged work stays on
the server, preserving the current security model.

## Goals

- Cover the full REST/SSE surface with shell-friendly commands.
- Human-readable output by default; machine-readable JSON on demand.
- Stream logs and VPN repair output live, cancellable with Ctrl-C.
- Stdlib only (`flag`, `net/http`, `encoding/json`) — no new module deps.
- Fully unit-testable against `httptest.Server`; never touches a real host.

## Non-goals

- Replacing the web UI.
- Local (`hosts.yaml`) config parsing or SSH in the client.
- Authentication (the server has none; see Security).

## Architecture

Two binaries from one module:

- `cmd/lanctl` — existing server (unchanged).
- `cmd/lanctl-cli` — new client (this document).

```
lanctl-cli ──HTTP/SSE──▶ lanctl server ──┬── local subprocess (systemctl/journalctl)
                                          └── SSH ──▶ managed hosts / VMs
```

The client is stateless aside from global flags and environment. It sends no
request bodies (the API takes none); all parameters are path segments or the
`lines` query parameter.

## Configuration

Server URL is resolved in this order:

1. `--server` / `-s` flag
2. `LANCTL_URL` environment variable
3. default `http://localhost:8003`

`--timeout` (default `15s`) bounds non-streaming requests. Streaming requests
are bounded by the `context` tied to Ctrl-C and end when the server closes the
stream.

## Command reference

```
lanctl-cli [global flags] <command> [args]
```

Targets address a host or a VM on a Proxmox host using `host` or `host/vm`,
mirroring the API path structure.

| Command | Description |
|---------|-------------|
| `hosts [--online]` | List hosts (and VMs) with status |
| `wake HOST` | Send Wake-on-LAN magic packet |
| `shutdown HOST[/VM]` | Shut down host or VM |
| `service HOST[/VM] SERVICE ACTION` | `start` / `stop` / `restart` |
| `logs HOST[/VM] SERVICE [-n N] [-f]` | Last N lines, or `-f` to follow (SSE) |
| `vpn-repair HOST[/VM]` | Run NordVPN repair, stream output |
| `version` | Print server build version |
| `help` | Usage |

### Global flags

| Flag | Default | Purpose |
|------|---------|---------|
| `-s, --server URL` | `http://localhost:8003` | Server base URL |
| `--timeout DUR` | `15s` | Non-streaming request timeout |
| `-o, --output FORMAT` | `table` | `table`, `json`, or `plain` |
| `-q, --quiet` | off | Suppress non-essential status text |
| `--no-color` | auto | Disable ANSI color |
| `-y, --yes` | off | Skip shutdown confirmation |
| `-h, --help` | | Usage |

## Output

- **`table`** (default) — aligned columns, TTY-aware color.
  `hosts` columns: `NAME  TYPE  IP  ONLINE  SERVICES  VPN`. VMs are indented
  beneath their Proxmox host.
- **`json`** — the raw decoded API response, pretty-printed. Stable for scripts.
- **`plain`** — tab/space-separated fields, no headers/color, for `awk`/`cut`.

`logs` and `vpn-repair` always write payload lines to stdout. Status messages
(e.g. `magic packet sent`) go to stderr, so `hosts -o json | jq` and
`logs ... > file` behave predictably.

### Examples

```bash
# What's up right now?
lanctl-cli hosts

# Wake the desktop and wait for SSH
lanctl-cli wake desktop

# Restart nginx on a Proxmox VM
lanctl-cli service nas/nas-main nginx restart

# Tail lanctl's own logs, then snapshot the last 200
lanctl-cli logs desktop lanctl.service -f
lanctl-cli logs desktop lanctl.service -n 200

# Script it
lanctl-cli hosts -o json | jq -r '.[] | select(.online) | .name'
```

## Wire mapping

| CLI invocation | Method | Path |
|----------------|--------|------|
| `hosts` | GET | `/api/hosts` |
| `version` | GET | `/api/version` |
| `wake H` | POST | `/api/hosts/{H}/wake` |
| `shutdown H` | POST | `/api/hosts/{H}/shutdown` |
| `shutdown H/V` | POST | `/api/hosts/{H}/vms/{V}/shutdown` |
| `service H S A` | POST | `/api/hosts/{H}/services/{S}/{A}` |
| `service H/V S A` | POST | `/api/hosts/{H}/vms/{V}/services/{S}/{A}` |
| `logs H S` | GET | `/api/hosts/{H}/services/{S}/logs?lines=N` |
| `logs H/V S` | GET | `/api/hosts/{H}/vms/{V}/services/{S}/logs?lines=N` |
| `logs -f H S` | GET | `/api/hosts/{H}/services/{S}/logs/stream` |
| `logs -f H/V S` | GET | `/api/hosts/{H}/vms/{V}/services/{S}/logs/stream` |
| `vpn-repair H` | POST | `/api/hosts/{H}/vpn-repair` |
| `vpn-repair H/V` | POST | `/api/hosts/{H}/vms/{V}/vpn-repair` |

All `{…}` segments are `url.PathEscape`d. The server matches names exactly
(case-sensitive), so escaping is required for correctness, not just safety.

## Streaming (SSE)

Both streams emit `data: <line>\n\n` events with a `: heartbeat` comment every
15s. `internal/client` provides one parser that ignores comments/blank lines and
yields event payloads. `logs -f` prints payload lines; `vpn-repair` prints them
with color for `[OK]`/`[FAIL]` markers and exits non-zero if any failure marker
was seen.

Ctrl-C cancels the request context and exits `0` (user-initiated), not an error.

## Errors and exit codes

Server errors are `{"detail": "..."}` with 400/404/500; the client decodes them
into `*APIError{Status, Detail}` and prints `detail` to stderr.

| Exit | Meaning |
|------|---------|
| `0` | Success (or user cancelled a stream) |
| `1` | API error, connection failure, or VPN repair reported a failure |
| `2` | Usage error (bad flags/args, unknown command) |

## Safety

- `shutdown` prompts `Shut down HOST? [y/N]` unless `--yes`, `-o json`, or
  stdin is not a TTY (in which case `--yes` is required).
- The client never bypasses server-side guards such as
  `LANCTL_ALLOW_SHUTDOWN`; a refusal surfaces as the server's `detail`.
- The API has no auth/transport security; the client is intended for the same
  trusted LAN as the web UI and should use `https://` / a tunnel for remote use.

## Package layout

```
internal/client/
  client.go      Client, APIError, Option, API methods
  sse.go         streamSSE helper + event parsing
  client_test.go httptest-backed tests for all methods
  sse_test.go    parser edge cases (heartbeats, partial chunks)
cmd/lanctl-cli/
  main.go        flag/env setup, dispatch, exit codes
  commands.go    one function per subcommand
  render.go      table/plain/json writers
  main_test.go   arg parsing + rendering to buffers
```

`internal/client` holds essentially all logic and is the coverage target (80%+).
`cmd/lanctl-cli` stays thin.

## API types (mirror server JSON)

```go
type Host struct {
    Name            string            `json:"name"`
    Type            string            `json:"type"`
    IP              string            `json:"ip"`
    MAC             string            `json:"mac"`
    Online          bool              `json:"online"`
    Local           bool              `json:"local"`
    Services        []string          `json:"services"`
    ServiceStatuses map[string]string `json:"service_statuses"`
    VPNHostname     *string           `json:"vpn_hostname"`
    VPNReachable    *bool             `json:"vpn_reachable"`
    VMs             *[]VM             `json:"vms,omitempty"`
}

type VM struct {
    Name            string            `json:"name"`
    VMID            int               `json:"vmid,omitempty"`
    IP              string            `json:"ip"`
    Online          bool              `json:"online"`
    Services        []string          `json:"services"`
    ServiceStatuses map[string]string `json:"service_statuses"`
    VPNHostname     *string           `json:"vpn_hostname"`
    VPNReachable    *bool             `json:"vpn_reachable"`
}
```

## Testing

- `internal/client`: `httptest.Server` returns canned JSON/SSE; assert method,
  path, query, decoding, and `*APIError` mapping for 400/404/500.
- `sse`: chunk-boundary and heartbeat cases.
- `cmd/lanctl-cli`: parse/validation and table/plain/json rendering to buffers.
- No test may reach a real host, service, or socket (per AGENTS.md).
- Real end-to-end smoke test can live behind the `integration` build tag later.

## Build & release

- `Makefile`: add `build-cli` → `./lanctl-cli`; include a
  `lanctl-cli-linux-{amd64,arm64,arm}` in `cross`; clean it up in `clean`.
- `install.sh`: install `lanctl-cli` to `/usr/local/bin` when present in
  `dist/`, non-fatally.
- `README.md`: add a "CLI" section mirroring the examples above.

## Future work

- Shell completion (`completion bash|zsh`).
- `~/.config/lanctl/config` for a persistent server URL.
- `--wait` on `wake` to poll `/api/hosts` until online.
- Optional server-side auth/token to make the API safe off-LAN.
