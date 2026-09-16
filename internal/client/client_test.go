package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew_Defaults(t *testing.T) {
	c := New("")
	if c.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, want %q", c.BaseURL, DefaultBaseURL)
	}
	if c.HTTP.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.HTTP.Timeout, DefaultTimeout)
	}
}

func TestNew_TrimsTrailingSlashAndAppliesOptions(t *testing.T) {
	custom := &http.Client{Timeout: time.Second}
	c := New("http://example:8003/", WithHTTPClient(custom), WithTimeout(2*time.Second))
	if c.BaseURL != "http://example:8003" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.HTTP != custom {
		t.Error("WithHTTPClient did not replace the client")
	}
	if c.HTTP.Timeout != 2*time.Second {
		t.Errorf("Timeout = %v, want 2s", c.HTTP.Timeout)
	}
}

func TestNew_NormalizesBaseURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", DefaultBaseURL},
		{"   ", DefaultBaseURL},
		{"rpi.local:8004", "http://rpi.local:8004"},
		{"  rpi.local:8004  ", "http://rpi.local:8004"},
		{"localhost:8003/", "http://localhost:8003"},
		{"http://example:8003/", "http://example:8003"},
		{"https://example:8004/", "https://example:8004"},
		{"[::1]:8004", "http://[::1]:8004"},
	}
	for _, tt := range tests {
		if got := New(tt.in).BaseURL; got != tt.want {
			t.Errorf("New(%q).BaseURL = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRequestPathsAndMethods(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		call   func(*Client) error
		method string
		path   string
		query  string
	}{
		{"version", func(c *Client) error { _, err := c.Version(ctx); return err }, "GET", "/api/version", ""},
		{"wake", func(c *Client) error { _, err := c.Wake(ctx, "nas box"); return err }, "POST", "/api/hosts/nas%20box/wake", ""},
		{"shutdown host", func(c *Client) error { return c.Shutdown(ctx, "desktop", "") }, "POST", "/api/hosts/desktop/shutdown", ""},
		{"shutdown vm", func(c *Client) error { return c.Shutdown(ctx, "nas", "vm/1") }, "POST", "/api/hosts/nas/vms/vm%2F1/shutdown", ""},
		{"service host", func(c *Client) error { return c.ServiceControl(ctx, "desktop", "", "nginx", "restart") }, "POST", "/api/hosts/desktop/services/nginx/restart", ""},
		{"service vm", func(c *Client) error { return c.ServiceControl(ctx, "nas", "main", "docker", "stop") }, "POST", "/api/hosts/nas/vms/main/services/docker/stop", ""},
		{"logs", func(c *Client) error { _, err := c.Logs(ctx, "desktop", "", "nginx", 50); return err }, "GET", "/api/hosts/desktop/services/nginx/logs", "lines=50"},
		{"logs no lines", func(c *Client) error { _, err := c.Logs(ctx, "desktop", "", "nginx", 0); return err }, "GET", "/api/hosts/desktop/services/nginx/logs", ""},
		{"logs vm", func(c *Client) error { _, err := c.Logs(ctx, "nas", "main", "lanctl.service", 5); return err }, "GET", "/api/hosts/nas/vms/main/services/lanctl.service/logs", "lines=5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath, gotQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath, gotQuery = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
				io.WriteString(w, "{}")
			}))
			defer srv.Close()

			if err := tt.call(New(srv.URL)); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotMethod != tt.method {
				t.Errorf("method = %q, want %q", gotMethod, tt.method)
			}
			if gotPath != tt.path {
				t.Errorf("path = %q, want %q", gotPath, tt.path)
			}
			if gotQuery != tt.query {
				t.Errorf("query = %q, want %q", gotQuery, tt.query)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"version":"v1.2.3"}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL).Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", got)
	}
}

func TestHosts_DecodesHostsAndVMs(t *testing.T) {
	body := `[
		{
			"name":"nas","type":"proxmox","ip":"192.168.0.10","mac":"11:22:33:44:55:66",
			"online":true,"local":false,"services":[],
			"service_statuses":{},"vpn_hostname":null,"vpn_reachable":null,
			"vms":[
				{"name":"main","vmid":100,"ip":"192.168.0.200","online":false,
				 "services":["nginx"],"service_statuses":{"nginx":"active"},
				 "vpn_hostname":"vpn.example","vpn_reachable":true}
			]
		},
		{"name":"desktop","ip":"192.168.0.100","mac":"AA:BB:CC:DD:EE:FF","online":true,
		 "local":true,"services":["nginx"],"service_statuses":{"nginx":"active"},
		 "vpn_hostname":null,"vpn_reachable":null}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	hosts, err := New(srv.URL).Hosts(context.Background())
	if err != nil {
		t.Fatalf("Hosts: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("len(hosts) = %d, want 2", len(hosts))
	}
	nas := hosts[0]
	if nas.Name != "nas" || nas.Type != "proxmox" || !nas.Online || nas.Local {
		t.Errorf("unexpected host: %+v", nas)
	}
	if nas.VMs == nil || len(*nas.VMs) != 1 {
		t.Fatalf("expected 1 VM, got %+v", nas.VMs)
	}
	vm := (*nas.VMs)[0]
	if vm.Name != "main" || vm.VMID != 100 || vm.Online {
		t.Errorf("unexpected vm: %+v", vm)
	}
	if vm.VPNReachable == nil || !*vm.VPNReachable {
		t.Errorf("vm.VPNReachable = %v, want true", vm.VPNReachable)
	}
	if hosts[1].VPNHostname != nil {
		t.Errorf("desktop.VPNHostname = %v, want nil", hosts[1].VPNHostname)
	}
}

func TestHosts_EmptyAndNullBecomeEmptySlice(t *testing.T) {
	for _, body := range []string{`[]`, `null`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, body)
		}))
		hosts, err := New(srv.URL).Hosts(context.Background())
		srv.Close()
		if err != nil {
			t.Fatalf("Hosts(%s): %v", body, err)
		}
		if hosts == nil || len(hosts) != 0 {
			t.Errorf("Hosts(%s) = %#v, want empty non-nil slice", body, hosts)
		}
	}
}

func TestWake_ReturnsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status":"magic packet sent","mac":"AA:BB:CC:DD:EE:FF"}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL).Wake(context.Background(), "desktop")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if got != "magic packet sent" {
		t.Errorf("status = %q", got)
	}
}

func TestLogs_DecodesLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"host":"desktop","service":"nginx","lines":["a","b"]}`)
	}))
	defer srv.Close()

	lines, err := New(srv.URL).Logs(context.Background(), "desktop", "", "nginx", 2)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Errorf("lines = %#v", lines)
	}
}

func TestLogs_NullLinesBecomeEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"host":"desktop","service":"nginx","lines":null}`)
	}))
	defer srv.Close()

	lines, err := New(srv.URL).Logs(context.Background(), "desktop", "", "nginx", 100)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if lines == nil || len(lines) != 0 {
		t.Errorf("lines = %#v, want empty non-nil", lines)
	}
}

func TestAPIError_CarriesStatusAndDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"detail":"Host 'ghost' not found"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).Wake(context.Background(), "ghost")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", apiErr.Status)
	}
	if apiErr.Detail != "Host 'ghost' not found" {
		t.Errorf("detail = %q", apiErr.Detail)
	}
	if apiErr.Error() != apiErr.Detail {
		t.Errorf("Error() = %q, want detail", apiErr.Error())
	}
}

func TestAPIError_NonJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()

	err := New(srv.URL).Shutdown(context.Background(), "desktop", "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", apiErr.Status)
	}
	if apiErr.Error() != "HTTP 500" {
		t.Errorf("Error() = %q, want %q", apiErr.Error(), "HTTP 500")
	}
}

func TestConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := New(url).Hosts(context.Background())
	if err == nil {
		t.Fatal("expected error for closed server")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Errorf("connection error should not be *APIError: %v", err)
	}
}

func TestContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"version":"v1"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := New(srv.URL).Version(ctx); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{not json`)
	}))
	defer srv.Close()

	if _, err := New(srv.URL).Version(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}
