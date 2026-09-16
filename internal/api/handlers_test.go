package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tom/ai-dev/frontends/lanctl-go/internal/network"
)

// fakeOps is a hermetic implementation of operations. It performs no I/O so
// tests never touch a real host, service, or network.
type fakeOps struct {
	checkSSHPort      bool
	shutdownErr       error
	serviceControlErr error
	journalLinesOut   string
	journalLinesErr   error
	sshShutdownErr    error
	sshServiceCtlErr  error
	sshJournalErr     error

	shutdownCalls int
}

func (f *fakeOps) SendMagicPacket(mac, broadcast string, port int) error {
	_, err := network.BuildMagicPacket(mac)
	return err
}

func (f *fakeOps) CheckSSHPort(host string, timeout time.Duration) bool {
	return f.checkSSHPort
}

func (f *fakeOps) Shutdown() error {
	f.shutdownCalls++
	return f.shutdownErr
}

func (f *fakeOps) ServiceStatuses(services []string) (map[string]string, error) {
	return statusMap(services), nil
}

func (f *fakeOps) ServiceControl(service, action string) error { return f.serviceControlErr }

func (f *fakeOps) JournalLines(service string, n int) (string, error) {
	return f.journalLinesOut, f.journalLinesErr
}

func (f *fakeOps) StreamJournal(service string, w http.ResponseWriter, r *http.Request) {}

func (f *fakeOps) SSHShutdown(ip, user, keyPath string) error { return f.sshShutdownErr }

func (f *fakeOps) SSHServiceStatuses(ip, user, keyPath string, services []string) (map[string]string, error) {
	return statusMap(services), nil
}

func (f *fakeOps) SSHServiceControl(ip, user, keyPath, service, action string) error {
	return f.sshServiceCtlErr
}

func (f *fakeOps) SSHJournalLines(ip, user, keyPath, service string, n int) (string, error) {
	return f.journalLinesOut, f.sshJournalErr
}

func (f *fakeOps) SSHStreamJournal(ip, user, keyPath, service string, w http.ResponseWriter, r *http.Request) {
}

func (f *fakeOps) StreamVPNRepair(ip, user, keyPath, token string, w http.ResponseWriter, r *http.Request) {
}

func statusMap(services []string) map[string]string {
	m := make(map[string]string, len(services))
	for _, s := range services {
		m[s] = "active"
	}
	return m
}

// useFakeOps swaps the package operations for a fake and restores it after the test.
func useFakeOps(t *testing.T) *fakeOps {
	t.Helper()
	f := &fakeOps{}
	prev := ops
	ops = f
	t.Cleanup(func() { ops = prev })
	return f
}

// writeConfig creates a hosts.yaml in a temp dir and sets DEPLOY_DIR to it.
func writeConfig(t *testing.T, content string) (cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hosts.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEPLOY_DIR", dir)
	return func() {}
}

func doRequest(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	mux := http.NewServeMux()
	RegisterRoutes(mux)
	mux.ServeHTTP(rr, req)
	return rr
}

func TestGetHosts_ReturnsHostList(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: myhost
    ip: 192.168.1.1
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "GET", "/api/hosts")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var result []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 host, got %d", len(result))
	}
	if result[0]["name"] != "myhost" {
		t.Errorf("expected name 'myhost', got %v", result[0]["name"])
	}
	if result[0]["type"] != "standard" {
		t.Errorf("expected type 'standard', got %v", result[0]["type"])
	}
}

func TestGetHosts_BadConfig(t *testing.T) {
	useFakeOps(t)
	t.Setenv("DEPLOY_DIR", t.TempDir()) // no hosts.yaml → error
	rr := doRequest(t, "GET", "/api/hosts")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestGetHosts_ProxmoxIncludesVMs(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 192.168.1.10
    mac: "aa:bb:cc:dd:ee:01"
    ssh_user: root
    vms:
      - name: vm1
        vmid: 100
        ip: 192.168.1.20
        ssh_user: user
`)
	rr := doRequest(t, "GET", "/api/hosts")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var result []map[string]any
	json.Unmarshal(rr.Body.Bytes(), &result)
	if _, hasVMs := result[0]["vms"]; !hasVMs {
		t.Error("expected Proxmox host to include 'vms' field")
	}
}

func TestWake_HostNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: other
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/missing/wake")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestWake_InvalidMAC(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: bad
    ip: 1.2.3.4
    mac: "invalid-mac"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/bad/wake")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestShutdown_HostNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: other
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/ghost/shutdown")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetLogs_HostNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: x
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "GET", "/api/hosts/ghost/services/svc/logs")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetLogs_ServiceNotConfigured(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: myhost
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
    services: [foo]
`)
	rr := doRequest(t, "GET", "/api/hosts/myhost/services/bar/logs")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestStreamLogs_ServiceNotConfigured(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: myhost
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
    services: [foo]
`)
	rr := doRequest(t, "GET", "/api/hosts/myhost/services/bar/logs/stream")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestServiceControl_InvalidAction(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: myhost
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
    services: [foo]
`)
	rr := doRequest(t, "POST", "/api/hosts/myhost/services/foo/kill")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestServiceControl_HostNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: x
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/ghost/services/foo/start")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestServiceControl_ServiceNotConfigured(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: myhost
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
    services: [allowed]
`)
	rr := doRequest(t, "POST", "/api/hosts/myhost/services/notallowed/start")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestVPNRepair_HostNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: x
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/ghost/vpn-repair")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestVPNRepair_LocalHost(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: local
    ip: 127.0.0.1
    mac: ""
    local: true
`)
	rr := doRequest(t, "POST", "/api/hosts/local/vpn-repair")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestVPNRepair_NoToken(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: remote
    ip: 192.168.1.1
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/remote/vpn-repair")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestVMShutdown_NotProxmox(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: standard
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/standard/vms/vm1/shutdown")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestVMShutdown_VMNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 1.2.3.5
        ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/ghost/shutdown")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestGetHosts_VMNordVPNFields(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 1.2.3.5
        ssh_user: user
        nordvpn_hostname: vm1.nord
      - name: vm2
        ip: 1.2.3.6
        ssh_user: user
`)
	rr := doRequest(t, "GET", "/api/hosts")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var result []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	vms, ok := result[0]["vms"].([]any)
	if !ok || len(vms) != 2 {
		t.Fatalf("expected 2 VMs, got %v", result[0]["vms"])
	}
	vm1 := vms[0].(map[string]any)
	if vm1["vpn_hostname"] != "vm1.nord" {
		t.Errorf("expected vpn_hostname 'vm1.nord', got %v", vm1["vpn_hostname"])
	}
	if reachable, ok := vm1["vpn_reachable"].(bool); !ok || reachable {
		t.Errorf("expected vpn_reachable=false for unreachable host, got %v", vm1["vpn_reachable"])
	}
	vm2 := vms[1].(map[string]any)
	if vm2["vpn_hostname"] != nil {
		t.Errorf("expected vpn_hostname null for VM without nordvpn_hostname, got %v", vm2["vpn_hostname"])
	}
	if vm2["vpn_reachable"] != nil {
		t.Errorf("expected vpn_reachable null for VM without nordvpn_hostname, got %v", vm2["vpn_reachable"])
	}
}

func TestVMVPNRepair_HostNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: x
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/ghost/vms/vm1/vpn-repair")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestVMVPNRepair_NotProxmox(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: standard
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/standard/vms/vm1/vpn-repair")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestVMVPNRepair_VMNotFound(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 1.2.3.5
        ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/ghost/vpn-repair")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestVMVPNRepair_LocalVM(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        local: true
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/vpn-repair")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestVMVPNRepair_NoToken(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 1.2.3.5
        ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/vpn-repair")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestVMVPNRepair_SSEHeaders(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
nordvpn_token: "testtoken"
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/vpn-repair")
	// SSE headers are written before SSH attempt, so status is 200
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (SSE), got %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", rr.Header().Get("Content-Type"))
	}
}

func TestVMGetLogs_ServiceNotConfigured(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 1.2.3.5
        ssh_user: user
        services: [allowed]
`)
	rr := doRequest(t, "GET", "/api/hosts/pve/vms/vm1/services/notallowed/logs")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestVMServiceControl_InvalidAction(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 1.2.3.5
        ssh_user: user
        services: [foo]
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/services/foo/kill")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// ── Local host tests (exercises localops branches) ────────────────────────────

const localHostConfig = `
hosts:
  - name: local
    ip: 127.0.0.1
    mac: ""
    local: true
    services: [definitely-nonexistent-lanctl-test-svc]
`

func TestGetHosts_LocalHost(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, localHostConfig)
	rr := doRequest(t, "GET", "/api/hosts")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var result []map[string]any
	json.Unmarshal(rr.Body.Bytes(), &result)
	if len(result) == 0 {
		t.Fatal("expected at least 1 host")
	}
	if result[0]["local"] != true {
		t.Errorf("expected local=true, got %v", result[0]["local"])
	}
	if result[0]["online"] != true {
		t.Errorf("expected local host online=true, got %v", result[0]["online"])
	}
}

func TestGetLogs_LocalHost(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, localHostConfig)
	rr := doRequest(t, "GET", "/api/hosts/local/services/definitely-nonexistent-lanctl-test-svc/logs")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestServiceControl_LocalHost(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, localHostConfig)
	rr := doRequest(t, "POST", "/api/hosts/local/services/definitely-nonexistent-lanctl-test-svc/stop")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVMShutdown_LocalVM(t *testing.T) {
	f := useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        local: true
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/shutdown")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if f.shutdownCalls != 1 {
		t.Fatalf("expected local shutdown to be called once, got %d", f.shutdownCalls)
	}
}

func TestVMGetLogs_LocalVM(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        local: true
        services: [definitely-nonexistent-lanctl-test-svc]
`)
	rr := doRequest(t, "GET", "/api/hosts/pve/vms/vm1/services/definitely-nonexistent-lanctl-test-svc/logs")
	if rr.Code == http.StatusNotFound {
		t.Fatalf("got 404, service should be found")
	}
}

func TestVMServiceControl_LocalVM(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        local: true
        services: [definitely-nonexistent-lanctl-test-svc]
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/services/definitely-nonexistent-lanctl-test-svc/restart")
	if rr.Code == http.StatusNotFound {
		t.Fatalf("got 404, service should be found")
	}
}

// ── SSH-host error-path tests (fake returns SSH errors) ──────────────────────

// sshHostConfig is a config with a non-local SSH host. The fake operations
// return injected errors, so no real connection is attempted.
const sshHostConfig = `
hosts:
  - name: remote
    ip: 127.0.0.1
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: testuser
    services: [foo]
`

func TestShutdown_SSHError(t *testing.T) {
	f := useFakeOps(t)
	f.sshShutdownErr = errors.New("ssh: connection refused")
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "POST", "/api/hosts/remote/shutdown")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetLogs_SSHError(t *testing.T) {
	f := useFakeOps(t)
	f.sshJournalErr = errors.New("ssh: connection refused")
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "GET", "/api/hosts/remote/services/foo/logs")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestServiceControl_SSHError(t *testing.T) {
	f := useFakeOps(t)
	f.sshServiceCtlErr = errors.New("ssh: connection refused")
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "POST", "/api/hosts/remote/services/foo/start")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVMShutdown_SSHError(t *testing.T) {
	f := useFakeOps(t)
	f.sshShutdownErr = errors.New("ssh: connection refused")
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/shutdown")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVMGetLogs_SSHError(t *testing.T) {
	f := useFakeOps(t)
	f.sshJournalErr = errors.New("ssh: connection refused")
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        ssh_user: user
        services: [foo]
`)
	rr := doRequest(t, "GET", "/api/hosts/pve/vms/vm1/services/foo/logs")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVMServiceControl_SSHError(t *testing.T) {
	f := useFakeOps(t)
	f.sshServiceCtlErr = errors.New("ssh: connection refused")
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        ssh_user: user
        services: [foo]
`)
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/services/foo/restart")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVPNRepair_SSEHeaders(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
nordvpn_token: "testtoken"
hosts:
  - name: remote
    ip: 127.0.0.1
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: user
`)
	rr := doRequest(t, "POST", "/api/hosts/remote/vpn-repair")
	// SSE headers are written before SSH attempt, so status is 200
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (SSE), got %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", rr.Header().Get("Content-Type"))
	}
}

func TestStreamLogs_SSEHeaders(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "GET", "/api/hosts/remote/services/foo/logs/stream")
	// SSE headers are written before SSH attempt
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (SSE), got %d", rr.Code)
	}
}

func TestVMStreamLogs_SSEHeaders(t *testing.T) {
	useFakeOps(t)
	writeConfig(t, `
hosts:
  - name: pve
    type: proxmox
    ip: 1.2.3.4
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: root
    vms:
      - name: vm1
        ip: 127.0.0.1
        ssh_user: user
        services: [foo]
`)
	rr := doRequest(t, "GET", "/api/hosts/pve/vms/vm1/services/foo/logs/stream")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (SSE), got %d", rr.Code)
	}
}

func TestParseLines_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/logs", nil)
	if parseLines(req, 100) != 100 {
		t.Error("expected default 100")
	}
}

func TestParseLines_Custom(t *testing.T) {
	req := httptest.NewRequest("GET", "/logs?lines=50", nil)
	if parseLines(req, 100) != 50 {
		t.Error("expected 50")
	}
}

func TestParseLines_Clamps(t *testing.T) {
	req := httptest.NewRequest("GET", "/logs?lines=9999", nil)
	if parseLines(req, 100) != 5000 {
		t.Error("expected 5000 (clamped)")
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"line1\n", 1},
		{"line1\nline2\n", 2},
		{"line1\nline2", 2},
	}
	for _, tt := range tests {
		got := splitLines(tt.input)
		if len(got) != tt.expected {
			t.Errorf("splitLines(%q): want %d lines, got %d", tt.input, tt.expected, len(got))
		}
	}
}
