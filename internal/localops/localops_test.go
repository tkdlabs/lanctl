package localops

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// stubRun replaces the package command runners for the duration of a test.
func stubRun(t *testing.T, out []byte, err error) *[][]string {
	t.Helper()
	var calls [][]string
	prevRun, prevSudo, prevUser := run, runSudo, runUser
	run = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return out, err
	}
	runSudo = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{"sudo", name}, args...))
		return out, err
	}
	runUser = func(user, name string, args ...string) ([]byte, error) {
		call := append([]string{name}, args...)
		if user != "" {
			call = append([]string{"user=" + user}, call...)
		}
		calls = append(calls, call)
		return out, err
	}
	t.Cleanup(func() { run, runSudo, runUser = prevRun, prevSudo, prevUser })
	return &calls
}

func TestRuntimeDirEnv_DefaultsWhenUnset(t *testing.T) {
	if _, ok := os.LookupEnv("XDG_RUNTIME_DIR"); ok {
		t.Skip("XDG_RUNTIME_DIR already set in this environment")
	}
	want := fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", os.Getuid())
	for _, e := range runtimeDirEnv() {
		if e == want {
			return
		}
	}
	t.Errorf("runtimeDirEnv() missing %q", want)
}

func TestRuntimeDirEnv_PreservesExisting(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/custom")
	env := runtimeDirEnv()
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			count++
			if e != "XDG_RUNTIME_DIR=/run/user/custom" {
				t.Errorf("got %q, want existing value", e)
			}
		}
	}
	if count != 1 {
		t.Errorf("XDG_RUNTIME_DIR appears %d times, want 1", count)
	}
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
	statuses, err := ServiceStatuses([]string{"nginx", "docker"}, false, "")
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

func TestServiceStatuses_UserScope(t *testing.T) {
	calls := stubRun(t, []byte("active\n"), nil)
	statuses, err := ServiceStatuses([]string{"myapp-backend"}, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statuses["myapp-backend"] != "active" {
		t.Errorf("unexpected statuses: %v", statuses)
	}
	if got := strings.Join((*calls)[0], " "); got != "systemctl --user is-active myapp-backend" {
		t.Errorf("got command %q", got)
	}
}

func TestServiceStatuses_UserScopePassesUser(t *testing.T) {
	calls := stubRun(t, []byte("active\n"), nil)
	if _, err := ServiceStatuses([]string{"myapp-backend"}, true, "appuser"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "user=appuser systemctl --user is-active myapp-backend"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Errorf("got command %q, want %q", got, want)
	}
}

func TestServiceStatuses_SetupErrorIsReturned(t *testing.T) {
	stubRun(t, nil, errors.New("user lookup failed"))
	if _, err := ServiceStatuses([]string{"myapp-backend"}, true, "appuser"); err == nil {
		t.Fatal("expected setup error to be returned")
	}
}

func TestServiceStatuses_ExitErrorIsIgnored(t *testing.T) {
	exitErr := exec.Command("sh", "-c", "exit 3").Run()
	if exitErr == nil {
		t.Fatal("expected a non-zero exit error")
	}
	stubRun(t, []byte("inactive\n"), exitErr)
	statuses, err := ServiceStatuses([]string{"myapp-backend"}, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statuses["myapp-backend"] != "inactive" {
		t.Errorf("status = %q, want inactive", statuses["myapp-backend"])
	}
}

func TestUserCommandFor(t *testing.T) {
	lookup := func(name string) (int, error) {
		switch name {
		case "same":
			return 1000, nil
		case "other":
			return 2000, nil
		default:
			return 0, fmt.Errorf("unknown user %q", name)
		}
	}
	args := []string{"--user", "is-active", "svc"}
	tests := []struct {
		name      string
		user      string
		uid, euid int
		wantName  string
		wantArgs  []string
		wantEnv   bool
		wantErr   string
	}{
		{
			name: "empty uses process user", user: "", uid: 1000, euid: 1000,
			wantName: "systemctl", wantArgs: args, wantEnv: true,
		},
		{
			name: "same user runs directly", user: "same", uid: 1000, euid: 1000,
			wantName: "systemctl", wantArgs: args, wantEnv: true,
		},
		{
			name: "other user as root uses sudo", user: "other", uid: 1000, euid: 0,
			wantName: "sudo",
			wantArgs: []string{"-n", "-u", "other", "env", "XDG_RUNTIME_DIR=/run/user/2000", "systemctl", "--user", "is-active", "svc"},
		},
		{
			name: "other user as non-root fails", user: "other", uid: 1000, euid: 1000,
			wantErr: "not root",
		},
		{
			name: "unknown user fails", user: "missing", uid: 1000, euid: 0,
			wantErr: "unknown user",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs, env, err := userCommandFor(tt.user, "systemctl", args, tt.uid, tt.euid, lookup)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got error %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotName != tt.wantName {
				t.Errorf("name = %q, want %q", gotName, tt.wantName)
			}
			if strings.Join(gotArgs, " ") != strings.Join(tt.wantArgs, " ") {
				t.Errorf("args = %q, want %q", gotArgs, tt.wantArgs)
			}
			if (env != nil) != tt.wantEnv {
				t.Errorf("env = %v, want non-nil=%v", env, tt.wantEnv)
			}
		})
	}
}

func TestLookupUserID_Numeric(t *testing.T) {
	uid, err := lookupUserID("1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != 1234 {
		t.Errorf("uid = %d, want 1234", uid)
	}
}

func TestServiceControl_Success(t *testing.T) {
	calls := stubRun(t, nil, nil)
	if err := ServiceControl("nginx", "restart", false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join((*calls)[0], " "); got != "sudo systemctl restart nginx" {
		t.Errorf("got command %q", got)
	}
}

func TestServiceControl_UserScope(t *testing.T) {
	calls := stubRun(t, nil, nil)
	if err := ServiceControl("myapp-backend", "restart", true, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join((*calls)[0], " "); got != "systemctl --user restart myapp-backend" {
		t.Errorf("got command %q (user services must not use sudo)", got)
	}
}

func TestServiceControl_UserScopePassesUser(t *testing.T) {
	calls := stubRun(t, nil, nil)
	if err := ServiceControl("myapp-backend", "restart", true, "appuser"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "user=appuser systemctl --user restart myapp-backend"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Errorf("got command %q, want %q", got, want)
	}
}

func TestServiceControl_ErrorUsesOutput(t *testing.T) {
	stubRun(t, []byte("Job failed. See logs.\n"), errors.New("exit 1"))
	err := ServiceControl("nginx", "restart", false, "")
	if err == nil || err.Error() != "Job failed. See logs." {
		t.Fatalf("got %v, want trimmed command output", err)
	}
}

func TestServiceControl_ErrorWithoutOutput(t *testing.T) {
	stubRun(t, []byte("   "), errors.New("exit 1"))
	err := ServiceControl("nginx", "stop", false, "")
	want := "systemctl stop nginx failed"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestJournalLines_Success(t *testing.T) {
	calls := stubRun(t, []byte("line one\nline two\n"), nil)
	out, err := JournalLines("nginx", 10, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "line one\nline two\n" {
		t.Errorf("unexpected output %q", out)
	}
	want := "journalctl -u nginx -n 10 --no-pager --output=short-iso"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Errorf("got command %q, want %q", got, want)
	}
}

func TestJournalLines_UserScope(t *testing.T) {
	calls := stubRun(t, []byte("log line\n"), nil)
	if _, err := JournalLines("myapp-backend", 25, true, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "journalctl --user -u myapp-backend -n 25 --no-pager --output=short-iso"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Errorf("got command %q, want %q", got, want)
	}
}

func TestJournalLines_UserScopePassesUser(t *testing.T) {
	calls := stubRun(t, []byte("log line\n"), nil)
	if _, err := JournalLines("myapp-backend", 25, true, "appuser"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "user=appuser journalctl --user -u myapp-backend -n 25 --no-pager --output=short-iso"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Errorf("got command %q, want %q", got, want)
	}
}

func TestJournalLines_Error(t *testing.T) {
	stubRun(t, nil, errors.New("no journal"))
	if _, err := JournalLines("nginx", 10, false, ""); err == nil {
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
	StreamJournal("svc", false, "", rr, httptest.NewRequest("GET", "/", nil))

	body := rr.Body.String()
	if !strings.Contains(body, "data: line one") || !strings.Contains(body, "data: line two") {
		t.Errorf("unexpected stream body: %q", body)
	}
}

func TestStreamJournal_UserScopeArgs(t *testing.T) {
	prev := streamCmd
	var gotArgs []string
	streamCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		gotArgs = args
		return exec.Command("sh", "-c", "true")
	}
	t.Cleanup(func() { streamCmd = prev })

	StreamJournal("myapp-backend", true, "", httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	want := "--user -u myapp-backend -f -n 50 --no-pager --output=short-iso"
	if got := strings.Join(gotArgs, " "); got != want {
		t.Errorf("got args %q, want %q", got, want)
	}
}

func TestStreamJournal_UserSwitchError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("switch error only applies to a non-root process")
	}
	rr := httptest.NewRecorder()
	StreamJournal("myapp-backend", true, "definitely-not-current-lanctl-user", rr, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rr.Body.String(), "data: [error:") {
		t.Errorf("expected an error event, got %q", rr.Body.String())
	}
}

func TestStreamJournal_NoFlusher(t *testing.T) {
	w := &noFlushWriter{}
	StreamJournal("svc", false, "", w, httptest.NewRequest("GET", "/", nil))
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
