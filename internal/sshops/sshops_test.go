package sshops

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const badKey = "/nonexistent/lanctl-test-key"

func TestDial_MissingKey(t *testing.T) {
	if _, err := dial("192.0.2.1", "user", badKey); err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestDial_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/key"
	if err := os.WriteFile(path, []byte("not a valid key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := dial("192.0.2.1", "user", path); err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestServiceStatuses_MissingKey(t *testing.T) {
	if _, err := ServiceStatuses("192.0.2.1", "user", badKey, []string{"nginx"}, false); err == nil {
		t.Fatal("expected error")
	}
}

func TestServiceControl_MissingKey(t *testing.T) {
	if err := ServiceControl("192.0.2.1", "user", badKey, "nginx", "restart", false); err == nil {
		t.Fatal("expected error")
	}
}

func TestJournalLines_MissingKey(t *testing.T) {
	if _, err := JournalLines("192.0.2.1", "user", badKey, "nginx", 10, false); err == nil {
		t.Fatal("expected error")
	}
}

func TestShutdown_MissingKey(t *testing.T) {
	if err := Shutdown("192.0.2.1", "user", badKey); err == nil {
		t.Fatal("expected error")
	}
}

func TestStreamJournal_MissingKey(t *testing.T) {
	rr := httptest.NewRecorder()
	StreamJournal("192.0.2.1", "user", badKey, "nginx", false, rr, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rr.Body.String(), "SSH error") {
		t.Errorf("expected SSH error in stream, got %q", rr.Body.String())
	}
}

func TestStreamJournal_NoFlusher(t *testing.T) {
	w := &plainWriter{}
	StreamJournal("192.0.2.1", "user", badKey, "nginx", false, w, httptest.NewRequest("GET", "/", nil))
	if w.written {
		t.Error("no output should be written when the writer cannot flush")
	}
}

func TestSystemctlCommand(t *testing.T) {
	tests := []struct {
		name      string
		userScope bool
		services  []string
		want      string
	}{
		{"system", false, []string{"nginx", "docker"}, "systemctl is-active nginx docker"},
		{"user", true, []string{"myapp-backend"}, "XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user is-active myapp-backend"},
	}
	for _, tt := range tests {
		if got := systemctlCommand(tt.userScope, "is-active", tt.services...); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestServiceControlCommand(t *testing.T) {
	if got := serviceControlCommand(false, "nginx", "restart"); got != "sudo systemctl restart nginx" {
		t.Errorf("system: got %q", got)
	}
	want := "XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user restart myapp-backend"
	if got := serviceControlCommand(true, "myapp-backend", "restart"); got != want {
		t.Errorf("user: got %q, want %q", got, want)
	}
}

func TestJournalctlCommand(t *testing.T) {
	if got := journalctlCommand(false, "nginx", 10, false); got != "journalctl -u nginx -n 10 --no-pager --output=short-iso" {
		t.Errorf("system: got %q", got)
	}
	want := "XDG_RUNTIME_DIR=/run/user/$(id -u) journalctl --user -u myapp-backend -f -n 50 --no-pager --output=short-iso"
	if got := journalctlCommand(true, "myapp-backend", 50, true); got != want {
		t.Errorf("user follow: got %q, want %q", got, want)
	}
}

func TestStreamVPNRepair_MissingKey(t *testing.T) {
	rr := httptest.NewRecorder()
	StreamVPNRepair("192.0.2.1", "user", badKey, "token", rr, httptest.NewRequest("POST", "/", nil))
	if !strings.Contains(rr.Body.String(), "SSH error") {
		t.Errorf("expected SSH error in stream, got %q", rr.Body.String())
	}
}

func TestExpandTilde(t *testing.T) {
	t.Setenv("HOME", "/home/tester")
	if got := expandTilde("~/.ssh/id_ed25519"); got != "/home/tester/.ssh/id_ed25519" {
		t.Errorf("got %q", got)
	}
	if got := expandTilde("/abs/key"); got != "/abs/key" {
		t.Errorf("got %q", got)
	}
}

func TestExpandTilde_NoHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := expandTilde("~/key"); got != "~/key" {
		t.Errorf("got %q, want unchanged path", got)
	}
}

// plainWriter implements http.ResponseWriter but not http.Flusher.
type plainWriter struct{ written bool }

func (w *plainWriter) Header() http.Header { return http.Header{} }
func (w *plainWriter) Write(b []byte) (int, error) {
	w.written = true
	return len(b), nil
}
func (w *plainWriter) WriteHeader(int) {}
