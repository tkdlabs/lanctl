package localops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// stubRun replaces the package command runners for the duration of a test.
func stubRun(t *testing.T, out []byte, err error) *[][]string {
	t.Helper()
	var calls [][]string
	prevRun, prevSudo := run, runSudo
	run = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return out, err
	}
	runSudo = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{"sudo", name}, args...))
		return out, err
	}
	t.Cleanup(func() { run, runSudo = prevRun, prevSudo })
	return &calls
}

func TestParseStatusOutput_Empty(t *testing.T) {
	out := ""
	services := []string{"svc1", "svc2"}
	statuses := parseStatusOutput(out, services)
	if statuses["svc1"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["svc1"])
	}
	if statuses["svc2"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["svc2"])
	}
}

func TestParseStatusOutput_ActiveInactive(t *testing.T) {
	out := "active\ninactive\nfailed\n"
	services := []string{"s1", "s2", "s3"}
	expected := map[string]string{"s1": "active", "s2": "inactive", "s3": "failed"}
	statuses := parseStatusOutput(out, services)
	for svc, want := range expected {
		if statuses[svc] != want {
			t.Errorf("service %q: want %q, got %q", svc, want, statuses[svc])
		}
	}
}

func TestParseStatusOutput_FewerLinesThanServices(t *testing.T) {
	out := "active\n"
	services := []string{"s1", "s2", "s3"}
	statuses := parseStatusOutput(out, services)
	if statuses["s1"] != "active" {
		t.Errorf("expected 'active', got %q", statuses["s1"])
	}
	if statuses["s2"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["s2"])
	}
	if statuses["s3"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["s3"])
	}
}

func TestParseStatusOutput_EmptyServices(t *testing.T) {
	statuses := parseStatusOutput("active\ninactive\n", []string{})
	if len(statuses) != 0 {
		t.Errorf("expected empty map, got %v", statuses)
	}
}

func TestShutdown_DisabledByDefault(t *testing.T) {
	t.Setenv("LANCTL_ALLOW_SHUTDOWN", "")
	if err := Shutdown(); err == nil {
		t.Fatal("expected Shutdown to be disabled without LANCTL_ALLOW_SHUTDOWN=1")
	}
}

func TestShutdown_Enabled(t *testing.T) {
	t.Setenv("LANCTL_ALLOW_SHUTDOWN", "1")
	calls := stubRun(t, nil, nil)
	if err := Shutdown(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.Join((*calls)[0], " ")
	if got != "sudo shutdown -h now" {
		t.Errorf("got command %q, want %q", got, "sudo shutdown -h now")
	}
}

func TestShutdown_EnabledPropagatesError(t *testing.T) {
	t.Setenv("LANCTL_ALLOW_SHUTDOWN", "1")
	stubRun(t, nil, errors.New("boom"))
	if err := Shutdown(); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestServiceStatuses(t *testing.T) {
	calls := stubRun(t, []byte("active\ninactive\n"), nil)
	statuses, err := ServiceStatuses([]string{"nginx", "docker"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statuses["nginx"] != "active" || statuses["docker"] != "inactive" {
		t.Errorf("unexpected statuses: %v", statuses)
	}
	if got := strings.Join((*calls)[0], " "); got != "systemctl is-active nginx docker" {
		t.Errorf("got command %q", got)
	}
}

func TestServiceControl_Success(t *testing.T) {
	calls := stubRun(t, nil, nil)
	if err := ServiceControl("nginx", "restart"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join((*calls)[0], " "); got != "sudo systemctl restart nginx" {
		t.Errorf("got command %q", got)
	}
}

func TestServiceControl_ErrorUsesOutput(t *testing.T) {
	stubRun(t, []byte("Job failed. See logs.\n"), errors.New("exit 1"))
	err := ServiceControl("nginx", "restart")
	if err == nil || err.Error() != "Job failed. See logs." {
		t.Fatalf("got %v, want trimmed command output", err)
	}
}

func TestServiceControl_ErrorWithoutOutput(t *testing.T) {
	stubRun(t, []byte("   "), errors.New("exit 1"))
	err := ServiceControl("nginx", "stop")
	want := "systemctl stop nginx failed"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestJournalLines_Success(t *testing.T) {
	stubRun(t, []byte("line one\nline two\n"), nil)
	out, err := JournalLines("nginx", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "line one\nline two\n" {
		t.Errorf("unexpected output %q", out)
	}
}

func TestJournalLines_Error(t *testing.T) {
	stubRun(t, nil, errors.New("no journal"))
	if _, err := JournalLines("nginx", 10); err == nil {
		t.Fatal("expected error")
	}
}

func TestStreamJournal_StreamsOutput(t *testing.T) {
	prev := streamCmd
	streamCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "printf 'line one\\nline two\\n'")
	}
	t.Cleanup(func() { streamCmd = prev })

	rr := httptest.NewRecorder()
	StreamJournal("svc", rr, httptest.NewRequest("GET", "/", nil))

	body := rr.Body.String()
	if !strings.Contains(body, "data: line one") || !strings.Contains(body, "data: line two") {
		t.Errorf("unexpected stream body: %q", body)
	}
}

func TestStreamJournal_NoFlusher(t *testing.T) {
	w := &noFlushWriter{}
	StreamJournal("svc", w, httptest.NewRequest("GET", "/", nil))
	if w.written {
		t.Error("nothing should be written when the writer cannot flush")
	}
}

// noFlushWriter implements http.ResponseWriter but not http.Flusher.
type noFlushWriter struct{ written bool }

func (w *noFlushWriter) Header() http.Header { return http.Header{} }
func (w *noFlushWriter) Write(b []byte) (int, error) {
	w.written = true
	return len(b), nil
}
func (w *noFlushWriter) WriteHeader(int) {}
