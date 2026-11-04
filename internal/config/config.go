package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultSSHKey = "~/.ssh/id_ed25519"

// SupportedConfigVersion is the config schema version this binary reads.
// A hosts.yaml declaring a newer config_version is rejected so that a
// config written for a newer binary is never silently misread.
const SupportedConfigVersion = 3

// Hubspace holds the account credentials used for smart-plug control.
type Hubspace struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

// Plug describes a smart plug. Name is the local lanctl identity; Affects is
// optional metadata naming the hosts that depend on this plug. Phase 1
// supports Hubspace power outlets only.
type Plug struct {
	Name            string   `yaml:"name"`
	Provider        string   `yaml:"provider,omitempty"`
	DeviceName      string   `yaml:"device_name,omitempty"`
	DeviceID        string   `yaml:"device_id,omitempty"`
	PowerOffSeconds int      `yaml:"power_off_seconds,omitempty"`
	Affects         []string `yaml:"affects,omitempty"`
}

const (
	// PlugProviderHubspace is the only supported smart-plug provider.
	PlugProviderHubspace = "hubspace"
	// defaultPowerOffSeconds is the power-cycle off delay when unset.
	defaultPowerOffSeconds = 30
	// maxPowerOffSeconds bounds a possibly long, manual hard-power cut.
	maxPowerOffSeconds = 600
)

// Config represents the top-level hosts.yaml configuration.
type Config struct {
	ConfigVersion int       `yaml:"config_version,omitempty"`
	SSHKey        string    `yaml:"ssh_key,omitempty"`
	NordVPNToken  string    `yaml:"nordvpn_token,omitempty"`
	Hubspace      *Hubspace `yaml:"hubspace,omitempty"`
	Plugs         []Plug    `yaml:"plugs,omitempty"`
	Hosts         []Host    `yaml:"hosts"`
}

// Host represents a single host entry. A Proxmox host has VMs nested under it.
type Host struct {
	Name         string   `yaml:"name"`
	Type         string   `yaml:"type,omitempty"`
	IP           string   `yaml:"ip"`
	MAC          string   `yaml:"mac"`
	SSHUser      string   `yaml:"ssh_user,omitempty"`
	SSHKey       string   `yaml:"ssh_key,omitempty"`
	Broadcast    string   `yaml:"broadcast,omitempty"`
	NordVPNHost  string   `yaml:"nordvpn_hostname,omitempty"`
	Local        bool     `yaml:"local,omitempty"`
	Services     []string `yaml:"services,omitempty"`
	UserServices []string `yaml:"user_services,omitempty"`
	// Plug is the legacy v2 host-attached form. It is converted into a
	// top-level plug during Load and cleared afterwards.
	Plug *Plug `yaml:"plug,omitempty"`
	VMs  []VM  `yaml:"vms,omitempty"`
}

// VM represents a virtual machine under a Proxmox host.
type VM struct {
	Name         string   `yaml:"name"`
	VMID         int      `yaml:"vmid,omitempty"`
	IP           string   `yaml:"ip"`
	SSHUser      string   `yaml:"ssh_user,omitempty"`
	SSHKey       string   `yaml:"ssh_key,omitempty"`
	NordVPNHost  string   `yaml:"nordvpn_hostname,omitempty"`
	Local        bool     `yaml:"local,omitempty"`
	Services     []string `yaml:"services,omitempty"`
	UserServices []string `yaml:"user_services,omitempty"`
}

// ServiceScope identifies the systemd instance that owns a configured service.
type ServiceScope string

const (
	// ScopeSystem is the system-wide systemd instance (systemctl).
	ScopeSystem ServiceScope = "system"
	// ScopeUser is a systemd user instance: that of the configured ssh_user on
	// both local and remote hosts (systemctl --user). On local hosts an empty
	// ssh_user falls back to the lanctl process user.
	ScopeUser ServiceScope = "user"
)

// Load reads and parses the hosts.yaml config file.
//
// It checks $DEPLOY_DIR/hosts.yaml first, then falls back to the directory
// of the running binary, then the working directory.
func Load() (Config, error) {
	return LoadFile(Path())
}

// Path returns the resolved hosts.yaml path Load reads from: $DEPLOY_DIR,
// then the working directory, then the bare "hosts.yaml" fallback.
func Path() string {
	return findConfigPath()
}

// LoadFile reads and parses the config file at the given path. Decoding is
// strict: unknown keys fail instead of being silently dropped, so a config
// written for a newer binary is rejected rather than misread.
func LoadFile(path string) (Config, error) {
	cfg := Config{}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	if cfg.ConfigVersion > SupportedConfigVersion {
		return cfg, fmt.Errorf("config requires schema v%d; this binary supports v%d — upgrade lanctl first",
			cfg.ConfigVersion, SupportedConfigVersion)
	}

	if err := applyLocalOverride(&cfg); err != nil {
		return cfg, err
	}
	applySecretEnvOverrides(&cfg)

	if len(cfg.Hosts) == 0 {
		return cfg, fmt.Errorf("no hosts found in %s. Copy hosts.example.yaml and edit it", path)
	}

	if err := normalizePlugs(&cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// applyLocalOverride honors $LANCTL_LOCAL_HOST: a host name, or "host/vm" to
// mark a VM local. The env var is authoritative — YAML-declared local flags
// are cleared first — so one shared hosts.yaml can serve every box, with each
// box self-identifying via its own environment.
func applyLocalOverride(cfg *Config) error {
	name := os.Getenv("LANCTL_LOCAL_HOST")
	if name == "" {
		return nil
	}

	for i := range cfg.Hosts {
		cfg.Hosts[i].Local = false
		for j := range cfg.Hosts[i].VMs {
			cfg.Hosts[i].VMs[j].Local = false
		}
	}

	if hostName, vmName, ok := strings.Cut(name, "/"); ok {
		for i := range cfg.Hosts {
			if cfg.Hosts[i].Name != hostName {
				continue
			}
			for j := range cfg.Hosts[i].VMs {
				if cfg.Hosts[i].VMs[j].Name == vmName {
					cfg.Hosts[i].VMs[j].Local = true
					return nil
				}
			}
			return fmt.Errorf("LANCTL_LOCAL_HOST %q: host %q has no VM %q (vms: %s)",
				name, hostName, vmName, joinOrNone(vmNames(cfg.Hosts[i])))
		}
		return fmt.Errorf("LANCTL_LOCAL_HOST %q: no host %q (hosts: %s)",
			name, hostName, strings.Join(hostNames(cfg), ", "))
	}

	for i := range cfg.Hosts {
		if cfg.Hosts[i].Name == name {
			cfg.Hosts[i].Local = true
			return nil
		}
	}
	return fmt.Errorf("LANCTL_LOCAL_HOST %q: no host with that name (hosts: %s)",
		name, strings.Join(hostNames(cfg), ", "))
}

// applySecretEnvOverrides lets process-level secrets override hosts.yaml.
// This keeps a shared config file free of tokens when a box injects them via
// its EnvironmentFile; an empty variable leaves the YAML value in place.
func applySecretEnvOverrides(cfg *Config) {
	if v := os.Getenv("NORDVPN_TOKEN"); v != "" {
		cfg.NordVPNToken = v
	}
	user := os.Getenv("HUBSPACE_USERNAME")
	pass := os.Getenv("HUBSPACE_PASSWORD")
	if user != "" || pass != "" {
		if cfg.Hubspace == nil {
			cfg.Hubspace = &Hubspace{}
		}
		if user != "" {
			cfg.Hubspace.Username = user
		}
		if pass != "" {
			cfg.Hubspace.Password = pass
		}
	}
}

// normalizePlugs converts the legacy v2 host-attached form, validates plug
// names/references, and applies provider/delay defaults.
func normalizePlugs(cfg *Config) error {
	hostSet := make(map[string]bool, len(cfg.Hosts))
	for _, h := range cfg.Hosts {
		hostSet[h.Name] = true
	}

	// v2 configs attached a plug to a host. Convert it to a first-class plug
	// named after the host so existing CLI/API workflows keep working.
	for i := range cfg.Hosts {
		h := &cfg.Hosts[i]
		if h.Plug == nil {
			continue
		}
		legacy := *h.Plug
		legacy.Name = h.Name
		legacy.Affects = []string{h.Name}
		cfg.Plugs = append(cfg.Plugs, legacy)
		h.Plug = nil
	}

	seen := make(map[string]bool, len(cfg.Plugs))
	for i := range cfg.Plugs {
		p := &cfg.Plugs[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return fmt.Errorf("plug #%d: name is required", i+1)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate plug name %q", p.Name)
		}
		seen[p.Name] = true

		if p.Provider == "" {
			p.Provider = PlugProviderHubspace
		} else if !strings.EqualFold(p.Provider, PlugProviderHubspace) {
			return fmt.Errorf("plug %q: unsupported provider %q (only %q is supported)",
				p.Name, p.Provider, PlugProviderHubspace)
		} else {
			p.Provider = PlugProviderHubspace
		}
		if strings.TrimSpace(p.DeviceID) == "" && strings.TrimSpace(p.DeviceName) == "" {
			return fmt.Errorf("plug %q: needs device_id or device_name", p.Name)
		}
		if p.PowerOffSeconds < 0 || p.PowerOffSeconds > maxPowerOffSeconds {
			return fmt.Errorf("plug %q: power_off_seconds must be between 0 and %d",
				p.Name, maxPowerOffSeconds)
		}
		if p.PowerOffSeconds == 0 {
			p.PowerOffSeconds = defaultPowerOffSeconds
		}

		if len(p.Affects) > 0 {
			affects := make([]string, 0, len(p.Affects))
			seenAffects := make(map[string]bool, len(p.Affects))
			for _, name := range p.Affects {
				name = strings.TrimSpace(name)
				if name == "" {
					return fmt.Errorf("plug %q: affects contains an empty host name", p.Name)
				}
				if !hostSet[name] {
					return fmt.Errorf("plug %q: affects references unknown host %q (hosts: %s)",
						p.Name, name, joinOrNone(hostNames(cfg)))
				}
				if seenAffects[name] {
					return fmt.Errorf("plug %q: affects lists host %q more than once", p.Name, name)
				}
				seenAffects[name] = true
				affects = append(affects, name)
			}
			p.Affects = affects
		} else {
			p.Affects = nil
		}
	}
	return nil
}

// HubspaceCredentials returns the configured account credentials. Credentials
// are optional at load time so a shared config can be used on boxes that do
// not need plugs; callers report a clear error when they are empty.
func (c Config) HubspaceCredentials() (username, password string) {
	if c.Hubspace == nil {
		return "", ""
	}
	return c.Hubspace.Username, c.Hubspace.Password
}

// HasPlugs reports whether any smart plug is configured.
func (c Config) HasPlugs() bool {
	return len(c.Plugs) > 0
}

// PlugNames lists configured plug names, for diagnostics.
func (c Config) PlugNames() []string {
	names := make([]string, 0, len(c.Plugs))
	for _, p := range c.Plugs {
		names = append(names, p.Name)
	}
	return names
}

// PlugByName resolves a first-class plug by its local name.
func (c Config) PlugByName(name string) (Plug, bool) {
	for _, p := range c.Plugs {
		if p.Name == name {
			return p, true
		}
	}
	return Plug{}, false
}

// PlugsForHost returns plugs whose affects list names host.
func (c Config) PlugsForHost(host string) []Plug {
	out := make([]Plug, 0)
	for _, p := range c.Plugs {
		for _, name := range p.Affects {
			if name == host {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// OffDuration returns the configured power-cycle off delay.
func (p Plug) OffDuration() time.Duration {
	seconds := p.PowerOffSeconds
	if seconds <= 0 {
		seconds = defaultPowerOffSeconds
	}
	return time.Duration(seconds) * time.Second
}

// hostNames returns the configured host names for diagnostics.
func hostNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Hosts))
	for _, h := range cfg.Hosts {
		names = append(names, h.Name)
	}
	return names
}

// vmNames returns a host's VM names for diagnostics.
func vmNames(h Host) []string {
	names := make([]string, 0, len(h.VMs))
	for _, vm := range h.VMs {
		names = append(names, vm.Name)
	}
	return names
}

// joinOrNone joins names for single-line diagnostics.
func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// findConfigPath resolves the path to hosts.yaml.
// Priority: $DEPLOY_DIR/hosts.yaml → current working directory → empty path.
func findConfigPath() string {
	if deployDir := os.Getenv("DEPLOY_DIR"); deployDir != "" {
		p := filepath.Join(ExpandTilde(deployDir), "hosts.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	wd, err := os.Getwd()
	if err == nil {
		p := filepath.Join(wd, "hosts.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "hosts.yaml"
}

// --- Helper functions ---

// IsProxmox returns true if the host is a Proxmox hypervisor.
func IsProxmox(h Host) bool {
	return strings.EqualFold(h.Type, "proxmox")
}

// GetVM retrieves a VM by name from a Proxmox host. Returns the VM and true
// if found, or a zero VM and false otherwise.
func GetVM(host Host, vmName string) (VM, bool) {
	if !IsProxmox(host) {
		return VM{}, false
	}
	for _, vm := range host.VMs {
		if vm.Name == vmName {
			return vm, true
		}
	}
	return VM{}, false
}

// GetHost retrieves a host by name from the config.
func GetHost(cfg Config, name string) (Host, bool) {
	for _, h := range cfg.Hosts {
		if h.Name == name {
			return h, true
		}
	}
	return Host{}, false
}

// ResolveSSHKey resolves the SSH key path for a host using the cascade:
// host SSHKey → global Config.SSHKey → default. The returned path always has a
// leading ~ expanded, so callers that stat or open it (not just the SSH dialer)
// see the real location; the unexpanded default previously produced false
// "missing key" diagnostics.
func ResolveSSHKey(cfg Config, h Host) string {
	if h.SSHKey != "" {
		return ExpandTilde(h.SSHKey)
	}
	if cfg.SSHKey != "" {
		return ExpandTilde(cfg.SSHKey)
	}
	return ExpandTilde(defaultSSHKey)
}

// ResolveVMSSHKey resolves the SSH key path for a VM using the cascade:
// VM SSHKey → parent Host SSHKey → global Config.SSHKey → default. Like
// ResolveSSHKey, the result has a leading ~ expanded.
func ResolveVMSSHKey(cfg Config, host Host, vm VM) string {
	if vm.SSHKey != "" {
		return ExpandTilde(vm.SSHKey)
	}
	if host.SSHKey != "" {
		return ExpandTilde(host.SSHKey)
	}
	if cfg.SSHKey != "" {
		return ExpandTilde(cfg.SSHKey)
	}
	return ExpandTilde(defaultSSHKey)
}

// FindService resolves a host's configured service name to its systemd scope.
// user_services take precedence over services so the same name can be moved
// between scopes without ambiguity.
func FindService(host Host, serviceName string) (ServiceScope, bool) {
	return findService(host.Services, host.UserServices, serviceName)
}

// FindVMService resolves a VM's configured service name to its systemd scope.
func FindVMService(vm VM, serviceName string) (ServiceScope, bool) {
	return findService(vm.Services, vm.UserServices, serviceName)
}

func findService(services, userServices []string, serviceName string) (ServiceScope, bool) {
	for _, svc := range userServices {
		if svc == serviceName {
			return ScopeUser, true
		}
	}
	for _, svc := range services {
		if svc == serviceName {
			return ScopeSystem, true
		}
	}
	return "", false
}

// ExpandTilde replaces a leading ~ with the user's home directory.
func ExpandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		if home == "" {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
