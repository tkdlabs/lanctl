package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkdlabs/lanctl/internal/client"
)

const hostsJSON = `[
  {"name":"desktop","ip":"192.168.0.100","mac":"AA:BB:CC:DD:EE:FF","online":true,
   "local":false,"services":["nginx"],"service_statuses":{"nginx":"active"},
   "vpn_hostname":null,"vpn_reachable":null,
   "plugs":[{"name":"rack","provider":"hubspace","device_name":"rack-plug","power_off_seconds":30,"affects":["nas","desktop"]}]},
  {"name":"nas","type":"proxmox","ip":"192.168.0.10","mac":"11:22:33:44:55:66","online":false,
   "local":false,"services":[],"service_statuses":{},
   "vpn_hostname":"vpn.example","vpn_reachable":false,
   "plugs":[{"name":"rack","provider":"hubspace","device_name":"rack-plug","power_off_seconds":30,"affects":["nas","desktop"]}],
   "vms":[{"name":"main","vmid":100,"ip":"192.168.0.200","online":true,
           "services":["docker"],"service_statuses":{"docker":"active"},
           "vpn_hostname":null,"vpn_reachable":null}]}
]`

func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	jsonResp := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
	detail := func(w http.ResponseWriter, status int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, `{"detail":"`+msg+`"}`)
	}

	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, `{"version":"v9.9.9"}`)
	})
	mux.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, hostsJSON)
	})
	mux.HandleFunc("POST /api/hosts/{name}/wake", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "ghost" {
			detail(w, http.StatusNotFound, "Host 'ghost' not found")
			return
		}
		jsonResp(w, `{"status":"magic packet sent","mac":"AA:BB:CC:DD:EE:FF"}`)
	})
	shutdown := func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, `{"status":"shutdown initiated"}`)
	}
	mux.HandleFunc("POST /api/hosts/{name}/shutdown", shutdown)
	mux.HandleFunc("POST /api/hosts/{name}/vms/{vm}/shutdown", shutdown)
	service := func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, `{"status":"`+r.PathValue("action")+` initiated"}`)
	}
	mux.HandleFunc("POST /api/hosts/{name}/services/{service}/{action}", service)
	mux.HandleFunc("POST /api/hosts/{name}/vms/{vm}/services/{service}/{action}", service)
	mux.HandleFunc("GET /api/hosts/{name}/services/{service}/logs", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, `{"host":"`+r.PathValue("name")+`","service":"`+r.PathValue("service")+`","lines":["line1","line2"]}`)
	})
	mux.HandleFunc("GET /api/hosts/{name}/services/{service}/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: streamed-1\n\n")
		w.(http.Flusher).Flush()
		io.WriteString(w, "data: streamed-2\n\n")
	})
	vpn := func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") == "bad" {
			detail(w, http.StatusNotFound, "Host 'bad' not found")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: [1/3] checking\n\n")
		w.(http.Flusher).Flush()
		io.WriteString(w, "data: [FAIL] tunnel down\n\n")
	}
	mux.HandleFunc("POST /api/hosts/{name}/vpn-repair", vpn)
	mux.HandleFunc("POST /api/hosts/{name}/vms/{vm}/vpn-repair", vpn)

	plugsBody := `[
	  {"name":"rack","provider":"hubspace","device_name":"rack-plug","power_off_seconds":30,"affects":["nas","desktop"],"on":true},
	  {"name":"tv","provider":"hubspace","device_name":"tv-plug","power_off_seconds":5,"affects":[],"on":false}
	]`
	mux.HandleFunc("GET /api/plugs", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, plugsBody)
	})
	mux.HandleFunc("GET /api/plugs/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		switch name {
		case "rack":
			jsonResp(w, `{"name":"rack","provider":"hubspace","device_name":"rack-plug","power_off_seconds":30,"affects":["nas","desktop"],"on":true}`)
		case "tv":
			jsonResp(w, `{"name":"tv","provider":"hubspace","device_name":"tv-plug","power_off_seconds":5,"affects":[],"on":false}`)
		default:
			detail(w, http.StatusNotFound, "Plug '"+name+"' not found")
		}
	})
	mux.HandleFunc("POST /api/plugs/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		affects := `[]`
		if name == "rack" {
			affects = `["nas","desktop"]`
		}
		jsonResp(w, `{"status":"`+r.PathValue("action")+` complete","plug":"`+name+`","affects":`+affects+`,"on":true}`)
	})
	mux.HandleFunc("GET /api/hubspace/devices", func(w http.ResponseWriter, r *http.Request) {
		jsonResp(w, `[{"id":"dev-plug","name":"Plug One","class":"power-outlet"}]`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type cliResult struct {
	code   int
	stdout string
	stderr string
}

func runCLI(t *testing.T, server, stdin string, interactive bool, args ...string) cliResult {
	t.Helper()
	var out, errBuf bytes.Buffer
	e := env{
		stdin:     strings.NewReader(stdin),
		stdout:    &out,
		stderr:    &errBuf,
		stdinTTY:  interactive,
		stdoutTTY: false,
	}
	full := append([]string{"-s", server}, args...)
	code := execute(context.Background(), e, full)
	return cliResult{code: code, stdout: out.String(), stderr: errBuf.String()}
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in, host, vm string
	}{
		{"desktop", "desktop", ""},
		{"nas/main", "nas", "main"},
		{"nas/a/b", "nas", "a/b"},
	}
	for _, tt := range tests {
		host, vm := parseTarget(tt.in)
		if host != tt.host || vm != tt.vm {
			t.Errorf("parseTarget(%q) = (%q,%q), want (%q,%q)", tt.in, host, vm, tt.host, tt.vm)
		}
	}
}

func TestParseFlags_Interleaved(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	n := fs.Int("n", 0, "")
	f := fs.Bool("f", false, "")

	rest, err := parseFlags(fs, []string{"host", "svc", "-n", "5", "-f"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if *n != 5 || !*f {
		t.Errorf("n=%d f=%v, want 5 true", *n, *f)
	}
	if strings.Join(rest, ",") != "host,svc" {
		t.Errorf("rest = %v", rest)
	}

	fs2 := flag.NewFlagSet("test", flag.ContinueOnError)
	fs2.SetOutput(io.Discard)
	n2 := fs2.Int("n", 0, "")
	rest2, err := parseFlags(fs2, []string{"-n", "7", "host"})
	if err != nil || *n2 != 7 || len(rest2) != 1 || rest2[0] != "host" {
		t.Errorf("leading flags: n=%d rest=%v err=%v", *n2, rest2, err)
	}
}

func TestExecute_Version(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "version")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "v9.9.9" {
		t.Errorf("stdout = %q", got.stdout)
	}
}

func TestExecute_VersionJSON(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "version")
	var v map[string]string
	if err := json.Unmarshal([]byte(got.stdout), &v); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, got.stdout)
	}
	if v["version"] != "v9.9.9" {
		t.Errorf("version = %q", v["version"])
	}
}

func TestExecute_HostsTable(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "hosts")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	for _, want := range []string{"NAME", "TYPE", "SERVICES", "VPN", "PLUGS", "desktop", "proxmox", "  main", "nginx=active", "vpn.example (unreachable)", "rack"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, got.stdout)
		}
	}
}

func TestExecute_HostsPlain(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "plain", "hosts")
	if got.code != 0 {
		t.Fatalf("code = %d", got.code)
	}
	if !strings.Contains(got.stdout, "desktop\tstandard\t192.168.0.100\tonline") {
		t.Errorf("plain output = %q", got.stdout)
	}
	if !strings.Contains(got.stdout, "nas/main\tvm\t192.168.0.200\tonline") {
		t.Errorf("plain VM line missing: %q", got.stdout)
	}
}

func TestExecute_HostsJSON(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "hosts")
	var hosts []map[string]any
	if err := json.Unmarshal([]byte(got.stdout), &hosts); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if len(hosts) != 2 {
		t.Errorf("len = %d, want 2", len(hosts))
	}
}

func TestExecute_HostsOnlineFilter(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "hosts", "--online")
	var hosts []map[string]any
	if err := json.Unmarshal([]byte(got.stdout), &hosts); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if len(hosts) != 1 || hosts[0]["name"] != "desktop" {
		t.Errorf("filtered hosts = %v", hosts)
	}
}

func TestExecute_Wake(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "wake", "desktop")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "magic packet sent") {
		t.Errorf("stderr = %q", got.stderr)
	}
	if got.stdout != "" {
		t.Errorf("stdout should be empty, got %q", got.stdout)
	}
}

func TestExecute_Wake_VMTargetIsUsageError(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "wake", "nas/main")
	if got.code != exitUsage {
		t.Errorf("code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_Wake_APIError(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "wake", "ghost")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "Host 'ghost' not found") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_ShutdownWithYes(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-y", "shutdown", "nas/main")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "shutdown initiated for nas/main") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_Shutdown_NonInteractiveRefuses(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "shutdown", "desktop")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "refusing") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_Shutdown_PromptAccept(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "y\n", true, "shutdown", "desktop")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "Shut down desktop?") {
		t.Errorf("prompt missing: %q", got.stderr)
	}
}

func TestExecute_Shutdown_PromptDecline(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "n\n", true, "shutdown", "desktop")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "aborted") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_Service(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "service", "nas/main", "docker", "restart")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "restart initiated: docker") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_Service_InvalidAction(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "service", "desktop", "nginx", "explode")
	if got.code != exitUsage {
		t.Errorf("code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_Logs(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "logs", "desktop", "nginx")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if got.stdout != "line1\nline2\n" {
		t.Errorf("stdout = %q", got.stdout)
	}
}

func TestExecute_Logs_FlagsAfterArgs(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "logs", "desktop", "nginx", "-n", "5")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
}

func TestExecute_LogsJSON(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "logs", "desktop", "nginx")
	var lines []string
	if err := json.Unmarshal([]byte(got.stdout), &lines); err != nil {
		t.Fatalf("stdout not JSON: %v", err)
	}
	if len(lines) != 2 {
		t.Errorf("lines = %v", lines)
	}
}

func TestExecute_LogsFollow(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "logs", "-f", "desktop", "nginx")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if got.stdout != "streamed-1\nstreamed-2\n" {
		t.Errorf("stdout = %q", got.stdout)
	}
}

func TestExecute_VPNRepair_FailureExitsOne(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "vpn-repair", "desktop")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "vpn repair reported a failure") {
		t.Errorf("stderr = %q", got.stderr)
	}
	if !strings.Contains(got.stdout, "[FAIL] tunnel down") {
		t.Errorf("stdout = %q", got.stdout)
	}
}

func TestExecute_VPNRepair_APIError(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "vpn-repair", "bad")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "Host 'bad' not found") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_UnknownCommand(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "frobnicate")
	if got.code != exitUsage {
		t.Errorf("code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_InvalidOutput(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "yaml", "version")
	if got.code != exitUsage {
		t.Errorf("code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_NoCommand(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false)
	if got.code != exitUsage {
		t.Errorf("code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_Help(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "help")
	if got.code != 0 {
		t.Errorf("code = %d, want 0", got.code)
	}
	if !strings.Contains(got.stdout, "Usage:") {
		t.Errorf("stdout = %q", got.stdout)
	}
}

func TestExecute_ConnectionError(t *testing.T) {
	srv := fakeServer(t)
	url := srv.URL
	srv.Close()
	got := runCLI(t, url, "", false, "version")
	if got.code != exitError {
		t.Errorf("code = %d, want %d", got.code, exitError)
	}
}

func TestExecute_QuietSuppressesStatus(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-q", "wake", "desktop")
	if got.code != 0 {
		t.Fatalf("code = %d", got.code)
	}
	if got.stderr != "" {
		t.Errorf("stderr = %q, want empty", got.stderr)
	}
}

func TestExecute_AliasLS(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "ls")
	if got.code != 0 {
		t.Fatalf("code = %d", got.code)
	}
	if !strings.Contains(got.stdout, "desktop") {
		t.Errorf("stdout = %q", got.stdout)
	}
}

func TestExecute_PlugsTable(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plugs")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	for _, want := range []string{"NAME", "DEVICE", "AFFECTS", "STATE", "rack", "rack-plug", "nas,desktop", "tv", "tv-plug"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, got.stdout)
		}
	}
}

func TestExecute_PlugsJSON(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "plugs")
	var plugs []client.Plug
	if err := json.Unmarshal([]byte(got.stdout), &plugs); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, got.stdout)
	}
	if len(plugs) != 2 || plugs[0].Name != "rack" || !plugs[0].On || plugs[1].Name != "tv" || plugs[1].On {
		t.Errorf("plugs = %+v", plugs)
	}
	if len(plugs[0].Affects) != 2 || plugs[0].Affects[0] != "nas" || plugs[0].Affects[1] != "desktop" {
		t.Errorf("rack affects = %v", plugs[0].Affects)
	}
	if len(plugs[1].Affects) != 0 {
		t.Errorf("tv affects = %v, want empty", plugs[1].Affects)
	}
}

func TestExecute_PlugsHostFilter(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "plugs", "--host", "nas")
	var plugs []client.Plug
	if err := json.Unmarshal([]byte(got.stdout), &plugs); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, got.stdout)
	}
	if len(plugs) != 1 || plugs[0].Name != "rack" {
		t.Errorf("plugs = %+v, want rack only", plugs)
	}
}

func TestExecute_PlugStatus(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plug", "status", "rack")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stdout, "rack: rack-plug is on (affects: nas, desktop)") {
		t.Errorf("stdout = %q", got.stdout)
	}
}

func TestExecute_PlugStatusJSON(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "plug", "status", "tv")
	var plug client.Plug
	if err := json.Unmarshal([]byte(got.stdout), &plug); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, got.stdout)
	}
	if plug.Name != "tv" || plug.On || len(plug.Affects) != 0 {
		t.Errorf("plug = %+v", plug)
	}
}

func TestExecute_PlugOffRequiresConfirmation(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plug", "off", "rack")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "refusing to proceed") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_PlugOffConfirmationNamesAffects(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "n\n", true, "plug", "off", "rack")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "affect hosts: nas, desktop") {
		t.Errorf("stderr = %q", got.stderr)
	}
	if !strings.Contains(got.stderr, "aborted") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_PlugOnRequiresConfirmationWhenAffected(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plug", "on", "rack")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "refusing to proceed") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_PlugOnStandaloneNoPrompt(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plug", "on", "tv")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "tv: on complete") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_PlugOffWithYes(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-y", "plug", "off", "rack")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	if !strings.Contains(got.stderr, "rack: off complete") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_PlugCycleJSON(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "plug", "cycle", "rack")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	var res actionResult
	if err := json.Unmarshal([]byte(got.stdout), &res); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, got.stdout)
	}
	if res.Action != "cycle" || res.Plug != "rack" || len(res.Affects) != 2 || res.On == nil || !*res.On {
		t.Errorf("result = %+v", res)
	}
}

func TestExecute_PlugInvalidAction(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plug", "bogus", "nas")
	if got.code != exitUsage {
		t.Errorf("code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_HubspaceDevices(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "hubspace", "devices")
	if got.code != 0 {
		t.Fatalf("code = %d, stderr = %q", got.code, got.stderr)
	}
	for _, want := range []string{"NAME", "ID", "CLASS", "Plug One", "dev-plug", "power-outlet"} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, got.stdout)
		}
	}
}

func TestExecute_PlugsPlain(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "plain", "plugs")
	if got.code != 0 {
		t.Fatalf("code = %d", got.code)
	}
	if !strings.Contains(got.stdout, "rack\track-plug\tnas,desktop\ton") {
		t.Errorf("plain output = %q", got.stdout)
	}
}

func TestExecute_HubspaceDevicesJSON(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "json", "hubspace", "devices")
	var devices []client.HubspaceDevice
	if err := json.Unmarshal([]byte(got.stdout), &devices); err != nil {
		t.Fatalf("stdout not JSON: %v (%q)", err, got.stdout)
	}
	if len(devices) != 1 || devices[0].ID != "dev-plug" {
		t.Errorf("devices = %+v", devices)
	}
}

func TestExecute_PlugAndHubspaceErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `{"detail":"hubspace down"}`)
	})
	mux.HandleFunc("GET /api/hubspace/devices", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `{"detail":"hubspace down"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, args := range [][]string{{"plugs"}, {"hubspace", "devices"}} {
		got := runCLI(t, srv.URL, "", false, args...)
		if got.code != exitError {
			t.Errorf("%v: code = %d, want %d", args, got.code, exitError)
		}
		if !strings.Contains(got.stderr, "hubspace down") {
			t.Errorf("%v: stderr = %q", args, got.stderr)
		}
	}
}

func TestPlugLabel(t *testing.T) {
	if got := plugLabel("name", "id"); got != "name" {
		t.Errorf("plugLabel(name,id) = %q", got)
	}
	if got := plugLabel("", "id"); got != "id" {
		t.Errorf("plugLabel(,id) = %q", got)
	}
	if got := plugLabel("", ""); got != "-" {
		t.Errorf("plugLabel(,) = %q", got)
	}
}

func TestExecute_PlugsExtraArgs(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plugs", "extra")
	if got.code != exitUsage {
		t.Errorf("code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_HubspaceDevicesPlainAndUsage(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "-o", "plain", "hubspace", "devices")
	if got.code != 0 || !strings.Contains(got.stdout, "Plug One\tdev-plug\tpower-outlet") {
		t.Errorf("plain output = %q (code %d)", got.stdout, got.code)
	}
	got = runCLI(t, srv.URL, "", false, "hubspace")
	if got.code != exitUsage {
		t.Errorf("missing subcommand code = %d, want %d", got.code, exitUsage)
	}
	got = runCLI(t, srv.URL, "", false, "hubspace", "bogus")
	if got.code != exitUsage {
		t.Errorf("bad subcommand code = %d, want %d", got.code, exitUsage)
	}
}

func TestExecute_PlugUsage(t *testing.T) {
	srv := fakeServer(t)
	for _, args := range [][]string{{"plug"}, {"plug", "status"}, {"plug", "status", "nas", "extra"}} {
		got := runCLI(t, srv.URL, "", false, args...)
		if got.code != exitUsage {
			t.Errorf("%v: code = %d, want %d", args, got.code, exitUsage)
		}
	}
}

func TestExecute_PlugStatusAPIError(t *testing.T) {
	srv := fakeServer(t)
	got := runCLI(t, srv.URL, "", false, "plug", "status", "ghost")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "Plug 'ghost' not found") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestExecute_PlugActionAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugs/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"name":"tv","provider":"hubspace","device_name":"tv-plug","power_off_seconds":5,"affects":[],"on":false}`)
	})
	mux.HandleFunc("POST /api/plugs/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `{"detail":"hubspace down"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got := runCLI(t, srv.URL, "", false, "plug", "on", "tv")
	if got.code != exitError {
		t.Fatalf("code = %d, want %d", got.code, exitError)
	}
	if !strings.Contains(got.stderr, "hubspace down") {
		t.Errorf("stderr = %q", got.stderr)
	}
}

func TestCycleTimeout(t *testing.T) {
	if got := cycleTimeout(10 * time.Second); got != 2*time.Minute {
		t.Errorf("cycleTimeout(10s) = %v, want 2m", got)
	}
	if got := cycleTimeout(5 * time.Minute); got != 5*time.Minute {
		t.Errorf("cycleTimeout(5m) = %v, want 5m", got)
	}
}

func TestFormatPlugHelpers(t *testing.T) {
	if got := formatAffects(nil); got != "-" {
		t.Errorf("formatAffects(nil) = %q", got)
	}
	if got := formatAffects([]string{"nas", "desktop"}); got != "nas,desktop" {
		t.Errorf("formatAffects = %q", got)
	}
	if got := formatPlugNames(nil); got != "-" {
		t.Errorf("formatPlugNames(nil) = %q", got)
	}
	plugs := []client.PlugInfo{{Name: "rack"}, {Name: "nas"}}
	if got := formatPlugNames(plugs); got != "rack,nas" {
		t.Errorf("formatPlugNames = %q", got)
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := typeLabel(""); got != "standard" {
		t.Errorf("typeLabel(\"\") = %q", got)
	}
	if got := formatServices(nil, nil, nil); got != "-" {
		t.Errorf("formatServices(nil) = %q", got)
	}
	if got := formatServices([]string{"a", "b"}, nil, map[string]string{"a": "active"}); got != "a=active,b" {
		t.Errorf("formatServices = %q", got)
	}
	userSvcs := []string{"myapp"}
	if got := formatServices([]string{"a"}, userSvcs, map[string]string{"myapp": "active"}); got != "a,myapp(user)=active" {
		t.Errorf("formatServices with user service = %q", got)
	}
	if got := formatServices(nil, []string{"myapp"}, nil); got != "myapp(user)" {
		t.Errorf("formatServices user only = %q", got)
	}
	name := "vpn.example"
	yes := true
	if got := formatVPN(&name, &yes); got != "vpn.example (reachable)" {
		t.Errorf("formatVPN = %q", got)
	}
	if got := formatVPN(nil, nil); got != "-" {
		t.Errorf("formatVPN(nil) = %q", got)
	}
}

func TestIsTerminal_NonFile(t *testing.T) {
	if isTerminal(strings.NewReader("")) {
		t.Error("non-file should not be a terminal")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "third"); got != "third" {
		t.Errorf("got %q", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Errorf("got %q", got)
	}
}
