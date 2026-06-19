package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
	t.Setenv("DEPLOY_DIR", t.TempDir()) // no hosts.yaml → error
	rr := doRequest(t, "GET", "/api/hosts")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestGetHosts_ProxmoxIncludesVMs(t *testing.T) {
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

func TestVMGetLogs_ServiceNotConfigured(t *testing.T) {
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
	writeConfig(t, localHostConfig)
	// journalctl with unknown service still returns 200 with empty or error lines
	rr := doRequest(t, "GET", "/api/hosts/local/services/definitely-nonexistent-lanctl-test-svc/logs")
	// success or 500 depending on journalctl availability — just verify it's not 404
	if rr.Code == http.StatusNotFound {
		t.Fatalf("got 404, expected 200 or 500")
	}
}

func TestServiceControl_LocalHost(t *testing.T) {
	writeConfig(t, localHostConfig)
	rr := doRequest(t, "POST", "/api/hosts/local/services/definitely-nonexistent-lanctl-test-svc/stop")
	// Will fail with systemctl error → 500, but not 404/400
	if rr.Code == http.StatusNotFound || rr.Code == http.StatusBadRequest {
		t.Fatalf("unexpected status %d", rr.Code)
	}
}

func TestVMShutdown_LocalVM(t *testing.T) {
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
	// Will attempt local shutdown — may fail (permission denied) but exercises branch
	rr := doRequest(t, "POST", "/api/hosts/pve/vms/vm1/shutdown")
	if rr.Code == http.StatusNotFound {
		t.Fatalf("got 404, VM should be found")
	}
}

func TestVMGetLogs_LocalVM(t *testing.T) {
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

// ── SSH-host error-path tests (SSH will fail → exercises non-local branches) ──

// sshHostConfig is a config with a non-local SSH host pointing at 127.0.0.1:22
// which will be unreachable (SSH key does not exist) causing predictable errors.
const sshHostConfig = `
hosts:
  - name: remote
    ip: 127.0.0.1
    mac: "aa:bb:cc:dd:ee:ff"
    ssh_user: testuser
    services: [foo]
`

func TestShutdown_SSHError(t *testing.T) {
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "POST", "/api/hosts/remote/shutdown")
	// SSH key doesn't exist → 500
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetLogs_SSHError(t *testing.T) {
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "GET", "/api/hosts/remote/services/foo/logs")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestServiceControl_SSHError(t *testing.T) {
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "POST", "/api/hosts/remote/services/foo/start")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestVMShutdown_SSHError(t *testing.T) {
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
	writeConfig(t, sshHostConfig)
	rr := doRequest(t, "GET", "/api/hosts/remote/services/foo/logs/stream")
	// SSE headers are written before SSH attempt
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (SSE), got %d", rr.Code)
	}
}

func TestVMStreamLogs_SSEHeaders(t *testing.T) {
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
