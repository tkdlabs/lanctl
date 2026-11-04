# Feature 13: First-Class Plugs and Affects Bindings

Issue: #26. Supersedes the host-attached model in
`done_12_smart_plugs.md`.

## Problem

A plug is not necessarily attached to one managed host. It may power several
hosts (a rack or shared switch), or no managed host at all (a TV/sound
processor). The TV should not have to be enrolled as a lanctl host just to get
a power button.

## Design

Plugs are top-level entities. Optional `affects` metadata names the hosts that
depend on a plug; it is for display and safety, not automatic actions.

```yaml
config_version: 3

hubspace:
  username: user@example.com
  password: "..."

plugs:
  - name: tv
    provider: hubspace
    device_name: "Living Room TV"
    power_off_seconds: 5
  - name: rack
    provider: hubspace
    device_name: "Rack outlet"
    power_off_seconds: 30
    affects: [nas, desktop]

hosts:
  - name: nas
    type: proxmox
    ip: 192.0.2.10
    mac: "AA:BB:CC:DD:EE:FF"
    ssh_user: admin
```

- `name` is the local lanctl identity; `device_name`/`device_id` is Hubspace.
- `affects` is optional and currently validated against host names.
- One plug may affect zero, one, or many hosts. One host may have many plugs.
- `shutdown` and `wake` never touch plugs automatically.
- A shared plug cycle is a hard cut to every affected host; confirmations list
  them.

## Migration

A v2 config with `hosts[].plug` is converted internally to a plug named after
the host with `affects: [host]`. The old host-attached field is cleared. A
top-level plug with the same name is a duplicate-name error.

`SupportedConfigVersion` becomes 3. Older v2 binaries reject a v3 config with
the existing upgrade message.

## API

The host-scoped plug routes are removed; this API was never deployed.

| Method | Path |
|--------|------|
| GET | `/api/plugs` |
| GET | `/api/plugs/{name}` |
| POST | `/api/plugs/{name}/{on\|off\|cycle}` |
| GET | `/api/hubspace/devices` |

`/api/hosts` returns associated plug metadata (`plugs`) but no live Hubspace
state, so the 10s host poll stays fast.

## CLI/UI

- `lanctl-cli plugs` shows `NAME`, `DEVICE`, `AFFECTS`, `STATE`.
- `lanctl-cli plugs --host HOST` filters by association.
- `lanctl-cli plug status|on|off|cycle NAME`.
- `lanctl-cli hosts` gains a `PLUGS` column with associated names.
- UI has a separate Smart plugs table. Host rows show badges for associated
  plugs.
- On/Off confirmations name affected hosts when the plug has associations;
  Cycle always confirms and names them.

## Acceptance criteria

- [x] Standalone, single-host, multi-host, and multiple-plugs-per-host configs
      load.
- [x] Unknown/duplicate `affects` names and duplicate plug names fail
      `--check`.
- [x] v2 host `plug` configs still load via conversion.
- [x] Plug-centric API/CLI/UI tests pass.
- [x] Live verification is status-only; no live power-cycle.

## Status

Done (#26). Live login, discovery, and state reads verified; no live
power-cycle performed.
