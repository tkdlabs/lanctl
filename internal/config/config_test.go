package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()

	yamlData := `
ssh_key: ~/.ssh/test_key
nordvpn_token: mytoken
hosts:
  - name: desktop
    ip: 192.168.0.100
    mac: "AA:BB:CC:DD:EE:FF"
    ssh_user: tom
    services:
      - nginx
  - name: self
    ip: 192.168.0.236
    mac: "11:22:33:44:55:66"
    local: true
    services:
      - lanctl-backend`

	cfgPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEPLOY_DIR", dir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.SSHKey != "~/.ssh/test_key" {
		t.Errorf("SSHKey = %q, want %q", cfg.SSHKey, "~/.ssh/test_key")
	}
	if cfg.NordVPNToken != "mytoken" {
		t.Errorf("NordVPNToken = %q, want %q", cfg.NordVPNToken, "mytoken")
	}
	if len(cfg.Hosts) != 2 {
		t.Fatalf("Hosts len = %d, want 2", len(cfg.Hosts))
	}

	h0 := cfg.Hosts[0]
	if h0.Name != "desktop" {
		t.Errorf("Hosts[0].Name = %q, want %q", h0.Name, "desktop")
	}
	if h0.IP != "192.168.0.100" {
		t.Errorf("Hosts[0].IP = %q, want %q", h0.IP, "192.168.0.100")
	}
	if len(h0.Services) != 1 || h0.Services[0] != "nginx" {
		t.Errorf("Hosts[0].Services = %v, want [nginx]", h0.Services)
	}

	h1 := cfg.Hosts[1]
	if !h1.Local {
		t.Error("Hosts[1].Local = false, want true")
	}
}

func TestLoad_ProxmoxHostWithVMs(t *testing.T) {
	dir := t.TempDir()

	yamlData := `
hosts:
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
          - lanctl-backend.service`

	cfgPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEPLOY_DIR", dir)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	h := cfg.Hosts[0]
	if !IsProxmox(h) {
		t.Error("IsProxmox(h) = false, want true")
	}
	if len(h.VMs) != 1 {
		t.Fatalf("VMs len = %d, want 1", len(h.VMs))
	}

	vm := h.VMs[0]
	if vm.Name != "nas-main" {
		t.Errorf("VM.Name = %q, want %q", vm.Name, "nas-main")
	}
	if vm.VMID != 100 {
		t.Errorf("VM.VMID = %d, want 100", vm.VMID)
	}
}

func TestLoad_NoHosts(t *testing.T) {
	dir := t.TempDir()

	yamlData := `ssh_key: ~/.ssh/test_key`

	cfgPath := filepath.Join(dir, "hosts.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlData), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEPLOY_DIR", dir)
	_, err := Load()
	if err == nil {
		t.Error("expected error for empty hosts list, got nil")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Setenv("DEPLOY_DIR", "/nonexistent/path")
	defer os.Unsetenv("DEPLOY_DIR")

	_, err := Load()
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestGetVM(t *testing.T) {
	host := Host{
		Type: "proxmox",
		VMs: []VM{
			{Name: "vm1", VMID: 100},
			{Name: "vm2", VMID: 101},
		},
	}

	vm, ok := GetVM(host, "vm2")
	if !ok {
		t.Fatal("expected vm2 to be found")
	}
	if vm.VMID != 101 {
		t.Errorf("VMID = %d, want 101", vm.VMID)
	}

	_, ok = GetVM(host, "nonexistent")
	if ok {
		t.Error("expected nonexistent VM to return false")
	}

	nonProxmox := Host{Name: "desktop"}
	_, ok = GetVM(nonProxmox, "anything")
	if ok {
		t.Error("expected GetVM on non-proxmox host to return false")
	}
}

func TestGetHost(t *testing.T) {
	cfg := Config{
		Hosts: []Host{
			{Name: "desktop"},
			{Name: "self"},
		},
	}

	h, ok := GetHost(cfg, "self")
	if !ok {
		t.Fatal("expected self to be found")
	}
	if h.Name != "self" {
		t.Errorf("Name = %q, want %q", h.Name, "self")
	}

	_, ok = GetHost(cfg, "missing")
	if ok {
		t.Error("expected missing host to return false")
	}
}

func TestResolveSSHKey_Cascade(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	defer os.Unsetenv("HOME")

	cfg := Config{
		SSHKey: "~/.ssh/global_key",
	}

	host := Host{Name: "test", SSHKey: "~/.ssh/host_key"}
	if got := ResolveSSHKey(cfg, host); got != "/home/user/.ssh/host_key" {
		t.Errorf("ResolveSSHKey = %q, want %q", got, "/home/user/.ssh/host_key")
	}

	hostNoKey := Host{Name: "test2"}
	if got := ResolveSSHKey(cfg, hostNoKey); got != "/home/user/.ssh/global_key" {
		t.Errorf("ResolveSSHKey fallback = %q, want %q", got, "/home/user/.ssh/global_key")
	}

	cfgNoKey := Config{}
	if got := ResolveSSHKey(cfgNoKey, hostNoKey); got != "~/.ssh/id_ed25519" {
		t.Errorf("ResolveSSHKey default = %q, want %q", got, "~/.ssh/id_ed25519")
	}
}

func TestResolveVMSSHKey_Cascade(t *testing.T) {
	t.Setenv("HOME", "/home/user")
	defer os.Unsetenv("HOME")

	cfg := Config{SSHKey: "~/.ssh/global"}
	host := Host{SSHKey: "~/.ssh/host"}
	vm := VM{Name: "test-vm", SSHKey: "~/.ssh/vm"}

	if got := ResolveVMSSHKey(cfg, host, vm); got != "/home/user/.ssh/vm" {
		t.Errorf("with vm key = %q, want %q", got, "/home/user/.ssh/vm")
	}

	vm2 := VM{Name: "test-vm2"}
	if got := ResolveVMSSHKey(cfg, host, vm2); got != "/home/user/.ssh/host" {
		t.Errorf("with host key = %q, want %q", got, "/home/user/.ssh/host")
	}

	host2 := Host{}
	vm3 := VM{Name: "test-vm3"}
	if got := ResolveVMSSHKey(cfg, host2, vm3); got != "/home/user/.ssh/global" {
		t.Errorf("with global key = %q, want %q", got, "/home/user/.ssh/global")
	}

	cfg2 := Config{}
	if got := ResolveVMSSHKey(cfg2, host2, vm3); got != "~/.ssh/id_ed25519" {
		t.Errorf("default = %q, want %q", got, "~/.ssh/id_ed25519")
	}
}

func TestValidateService(t *testing.T) {
	host := Host{
		Services: []string{"nginx", "docker"},
	}

	if !ValidateService(host, "nginx") {
		t.Error("expected nginx to be valid")
	}
	if ValidateService(host, "redis") {
		t.Error("expected redis to be invalid")
	}

	empty := Host{}
	if ValidateService(empty, "anything") {
		t.Error("expected service to be invalid on empty host")
	}
}

func TestValidateVMService(t *testing.T) {
	vm := VM{
		Services: []string{"lanctl.service"},
	}

	if !ValidateVMService(vm, "lanctl.service") {
		t.Error("expected lanctl.service to be valid")
	}
	if ValidateVMService(vm, "other") {
		t.Error("expected other to be invalid")
	}
}

func TestIsProxmox(t *testing.T) {
	if !IsProxmox(Host{Type: "proxmox"}) {
		t.Error("expected proxmox to match")
	}
	if !IsProxmox(Host{Type: "Proxmox"}) {
		t.Error("expected Proxmox (capitalized) to match")
	}
	if IsProxmox(Host{Type: "standard"}) {
		t.Error("expected standard to not match")
	}
	if IsProxmox(Host{}) {
		t.Error("expected empty type to not match")
	}
}

func TestExpandTilde(t *testing.T) {
	t.Setenv("HOME", "/home/testuser")

	if got := expandTilde("~/foo"); got != "/home/testuser/foo" {
		t.Errorf("expandTilde(~/foo) = %q, want %q", got, "/home/testuser/foo")
	}
	if got := expandTilde("/abs/path"); got != "/abs/path" {
		t.Errorf("expandTilde(/abs/path) = %q, want %q", got, "/abs/path")
	}
}
