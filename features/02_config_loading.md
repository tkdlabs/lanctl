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

- [ ] `config.Load(path)` reads and parses `hosts.yaml`
- [ ] Missing file returns a clear error
- [ ] `DEPLOY_DIR` env var support: prefer `$DEPLOY_DIR/hosts.yaml`, fall back to project root
- [ ] SSH key resolution cascade: VM ssh_key → parent host ssh_key → global ssh_key → `~/.ssh/id_ed25519`
- [ ] `isProxmox(host)` and `getVM(host, vmName)` helpers
- [ ] Service name validation helper
- [ ] `hosts.example.yaml` copied from Python version
