# Feature 02: Config loading and YAML parsing

## Description

Port the `hosts.yaml` config loading logic. Parse host entries including standard hosts,
Proxmox hosts with VMs, and all optional fields (ssh_key, broadcast, nordvpn_hostname,
services, local flag).

## Config schema

Matches existing `hosts.yaml` — no format changes needed:

```yaml
ssh_key: ~/.ssh/id_ed25519    # optional, global default
nordvpn_token: "token"        # optional, global

hosts:
  - name: self
    ip: 192.168.0.236
    mac: "AA:BB:CC:DD:EE:FF"
    local: true
    services:
      - lanctl-backend

  - name: desktop
    ip: 192.168.0.100
    mac: "AA:BB:CC:DD:EE:FF"
    ssh_user: tom
    services:
      - nginx
      - docker

  - name: nas
    type: proxmox
    ip: 192.168.0.10
    mac: "11:22:33:44:55:66"
    ssh_user: root
    vms:
      - name: nas-main
        vmid: 100
        ip: 192.168.0.200
        ssh_user: tom
        services:
          - lanctl-backend.service
```

## Types

```go
type Config struct {
    SSHKey       string   `yaml:"ssh_key,omitempty"`
    NordVPNToken string   `yaml:"nordvpn_token,omitempty"`
    Hosts        []Host   `yaml:"hosts"`
}

type Host struct {
    Name           string   `yaml:"name"`
    Type           string   `yaml:"type,omitempty"` // "standard" or "proxmox"
    IP             string   `yaml:"ip"`
    MAC            string   `yaml:"mac"`
    SSHUser        string   `yaml:"ssh_user,omitempty"`
    SSHKey         string   `yaml:"ssh_key,omitempty"`
    Broadcast      string   `yaml:"broadcast,omitempty"`
    NordVPNHost    string   `yaml:"nordvpn_hostname,omitempty"`
    Local          bool     `yaml:"local,omitempty"`
    Services       []string `yaml:"services,omitempty"`
    VMs            []VM     `yaml:"vms,omitempty"`
}

type VM struct {
    Name        string   `yaml:"name"`
    VMID        int      `yaml:"vmid,omitempty"`
    IP          string   `yaml:"ip"`
    SSHUser     string   `yaml:"ssh_user,omitempty"`
    SSHKey      string   `yaml:"ssh_key,omitempty"`
    NordVPNHost string   `yaml:"nordvpn_hostname,omitempty"`
    Local       bool     `yaml:"local,omitempty"`
    Services    []string `yaml:"services,omitempty"`
}
```

## Acceptance criteria

- [x] `config.Load(path)` reads and parses `hosts.yaml`
- [x] Missing file returns a clear error
- [x] `DEPLOY_DIR` env var support: prefer `$DEPLOY_DIR/hosts.yaml`, fall back to project root
- [x] SSH key resolution cascade: VM ssh_key → parent host ssh_key → global ssh_key → `~/.ssh/id_ed25519`
- [x] `IsProxmox(host)` and `GetVM(host, vmName)` helpers
- [x] Service name validation helper
- [x] `hosts.example.yaml` copied from Python version

## Unit tests

| Test | Coverage |
|------|----------|
| `TestLoad_ValidConfig` | config parsing, standard hosts |
| `TestLoad_ProxmoxHostWithVMs` | Proxmox host + VM parsing |
| `TestLoad_NoHosts` | error on empty hosts list |
| `TestLoad_MissingFile` | error on missing file |
| `TestGetVM` | VM lookup, non-proxmox fallback |
| `TestGetHost` | host lookup by name |
| `TestResolveSSHKey_Cascade` | host key → global key → default |
| `TestResolveVMSSHKey_Cascade` | vm key → host key → global key → default |
| `TestValidateService` | service validation on host |
| `TestValidateVMService` | service validation on VM |
| `TestIsProxmox` | case-insensitive type check |
| `TestExpandTilde` | home dir expansion |

**Total**: 12 tests, **94.7% coverage**
