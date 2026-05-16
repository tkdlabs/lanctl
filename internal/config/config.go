package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultSSHKey = "~/.ssh/id_ed25519"

// Config represents the top-level hosts.yaml configuration.
type Config struct {
	SSHKey       string `yaml:"ssh_key,omitempty"`
	NordVPNToken string `yaml:"nordvpn_token,omitempty"`
	Hosts        []Host `yaml:"hosts"`
}

// Host represents a single host entry. A Proxmox host has VMs nested under it.
type Host struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type,omitempty"`
	IP          string   `yaml:"ip"`
	MAC         string   `yaml:"mac"`
	SSHUser     string   `yaml:"ssh_user,omitempty"`
	SSHKey      string   `yaml:"ssh_key,omitempty"`
	Broadcast   string   `yaml:"broadcast,omitempty"`
	NordVPNHost string   `yaml:"nordvpn_hostname,omitempty"`
	Local       bool     `yaml:"local,omitempty"`
	Services    []string `yaml:"services,omitempty"`
	VMs         []VM     `yaml:"vms,omitempty"`
}

// VM represents a virtual machine under a Proxmox host.
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

// Load reads and parses the hosts.yaml config file.
//
// It checks $DEPLOY_DIR/hosts.yaml first, then falls back to the directory
// of the running binary, then the working directory.
func Load() (Config, error) {
	cfg := Config{}

	path := findConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	if len(cfg.Hosts) == 0 {
		return cfg, fmt.Errorf("no hosts found in %s. Copy hosts.example.yaml and edit it", path)
	}

	return cfg, nil
}

// findConfigPath resolves the path to hosts.yaml.
// Priority: $DEPLOY_DIR/hosts.yaml → current working directory → empty path.
func findConfigPath() string {
	if deployDir := os.Getenv("DEPLOY_DIR"); deployDir != "" {
		p := filepath.Join(expandTilde(deployDir), "hosts.yaml")
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
// host SSHKey → global Config.SSHKey → default.
func ResolveSSHKey(cfg Config, h Host) string {
	if h.SSHKey != "" {
		return expandTilde(h.SSHKey)
	}
	if cfg.SSHKey != "" {
		return expandTilde(cfg.SSHKey)
	}
	return defaultSSHKey
}

// ResolveVMSSHKey resolves the SSH key path for a VM using the cascade:
// VM SSHKey → parent Host SSHKey → global Config.SSHKey → default.
func ResolveVMSSHKey(cfg Config, host Host, vm VM) string {
	if vm.SSHKey != "" {
		return expandTilde(vm.SSHKey)
	}
	if host.SSHKey != "" {
		return expandTilde(host.SSHKey)
	}
	if cfg.SSHKey != "" {
		return expandTilde(cfg.SSHKey)
	}
	return defaultSSHKey
}

// ValidateService checks if the service name exists in the host's service list.
func ValidateService(host Host, serviceName string) bool {
	for _, svc := range host.Services {
		if svc == serviceName {
			return true
		}
	}
	return false
}

// ValidateVMService checks if the service name exists in the VM's service list.
func ValidateVMService(vm VM, serviceName string) bool {
	for _, svc := range vm.Services {
		if svc == serviceName {
			return true
		}
	}
	return false
}

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		if home == "" {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
