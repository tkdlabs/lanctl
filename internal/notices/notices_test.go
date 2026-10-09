package notices

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cleanEnv clears the environment Build reads so tests are hermetic even if
// the developer's shell exports these.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DEPLOY_DIR", "LANCTL_LOCAL_HOST", "CONFIG_GIT_REMOTE", "LANCTL_STALE_AFTER", "SYNC_DISABLED_FILE"} {
		t.Setenv(k, "")
	}
}

// setup creates a DEPLOY_DIR with the given hosts.yaml and returns its path.
func setup(t *testing.T, hostsYAML string) string {
	t.Helper()
	cleanEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"), hostsYAML)
	t.Setenv("DEPLOY_DIR", dir)
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeState(t *testing.T, dir, content string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, ".config-sync", "state.json"), content)
}

const localHostYAML = `hosts:
  - name: box
    ip: 192.0.2.10
    mac: "aa:bb:cc:dd:ee:01"
    local: true
`

func noticeByCode(r Report, code string) (Notice, bool) {
	for _, n := range r.Notices {
		if n.Code == code {
			return n, true
		}
	}
	return Notice{}, false
}

func TestBuildOK(t *testing.T) {
	setup(t, localHostYAML)
	rep := Build("dev", time.Now())

	if rep.Status != statusOK {
		t.Errorf("status = %q, want ok", rep.Status)
	}
	if !rep.ConfigOK || rep.HostCount != 1 {
		t.Errorf("ConfigOK=%v HostCount=%d, want true/1", rep.ConfigOK, rep.HostCount)
	}
	if filepath.Base(rep.ConfigPath) != "hosts.yaml" {
		t.Errorf("ConfigPath = %q", rep.ConfigPath)
	}
	if rep.ConfigAgeSeconds == nil {
		t.Error("ConfigAgeSeconds is nil, want set")
	}
	if len(rep.Notices) != 0 {
		t.Errorf("notices = %+v, want none", rep.Notices)
	}
	if rep.Sync != nil {
		t.Errorf("Sync = %+v, want nil", rep.Sync)
	}
}

func TestBuildConfigInvalid(t *testing.T) {
	setup(t, "hosts:\n  - name: box\n    bogus_key: 1\n")
	rep := Build("dev", time.Now())

	if rep.Status != statusError {
		t.Errorf("status = %q, want error", rep.Status)
	}
	if rep.ConfigOK {
		t.Error("ConfigOK = true, want false")
	}
	if rep.ConfigError == "" {
		t.Error("ConfigError empty, want message")
	}
	if _, ok := noticeByCode(rep, "config_invalid"); !ok {
		t.Errorf("missing config_invalid notice: %+v", rep.Notices)
	}
}

func TestBuildConfigMissing(t *testing.T) {
	cleanEnv(t)
	t.Setenv("DEPLOY_DIR", t.TempDir())
	rep := Build("dev", time.Now())
	if rep.ConfigOK {
		t.Error("ConfigOK = true, want false")
	}
	if _, ok := noticeByCode(rep, "config_invalid"); !ok {
		t.Errorf("missing config_invalid notice: %+v", rep.Notices)
	}
}

func TestBuildLocalHostReported(t *testing.T) {
	setup(t, localHostYAML)
	t.Setenv("LANCTL_LOCAL_HOST", "box")
	rep := Build("v1.2.3", time.Now())
	if rep.LocalHost != "box" {
		t.Errorf("LocalHost = %q, want box", rep.LocalHost)
	}
	if rep.Version != "v1.2.3" {
		t.Errorf("Version = %q", rep.Version)
	}
}

func TestMissingVPNToken(t *testing.T) {
	setup(t, `hosts:
  - name: box
    ip: 192.0.2.10
    mac: "aa:bb:cc:dd:ee:01"
    local: true
  - name: nas
    ip: 192.0.2.20
    mac: "aa:bb:cc:dd:ee:02"
    nordvpn_hostname: vpn.example.com
    ssh_key: /tmp/definitely-missing-key
`)
	rep := Build("dev", time.Now())
	if n, ok := noticeByCode(rep, "missing_vpn_token"); !ok {
		t.Errorf("missing missing_vpn_token notice: %+v", rep.Notices)
	} else if n.Level != LevelWarning {
		t.Errorf("level = %q, want warning", n.Level)
	}
	if rep.Status != statusDegraded {
		t.Errorf("status = %q, want degraded", rep.Status)
	}
}

func TestMissingSSHKey(t *testing.T) {
	setup(t, `hosts:
  - name: box
    ip: 192.0.2.10
    mac: "aa:bb:cc:dd:ee:01"
    local: true
  - name: nas
    ip: 192.0.2.20
    mac: "aa:bb:cc:dd:ee:02"
    ssh_key: /tmp/lanctl-missing-key-xyz
`)
	rep := Build("dev", time.Now())
	n, ok := noticeByCode(rep, "missing_ssh_key")
	if !ok {
		t.Fatalf("missing missing_ssh_key notice: %+v", rep.Notices)
	}
	if n.Message == "" {
		t.Error("empty message")
	}
}

func TestMissingVMSSHKey(t *testing.T) {
	setup(t, `hosts:
  - name: pve
    type: proxmox
    ip: 192.0.2.30
    mac: "aa:bb:cc:dd:ee:03"
    local: true
    vms:
      - name: guest
        ip: 192.0.2.31
        ssh_key: /tmp/lanctl-missing-vm-key
`)
	rep := Build("dev", time.Now())
	if _, ok := noticeByCode(rep, "missing_ssh_key"); !ok {
		t.Errorf("missing missing_ssh_key notice: %+v", rep.Notices)
	}
}

func TestSSHKeyPresent(t *testing.T) {
	dir := setup(t, "")
	key := filepath.Join(dir, "id_key")
	writeFile(t, key, "key")
	writeFile(t, filepath.Join(dir, "hosts.yaml"), `hosts:
  - name: box
    ip: 192.0.2.10
    mac: "aa:bb:cc:dd:ee:01"
    local: true
  - name: nas
    ip: 192.0.2.20
    mac: "aa:bb:cc:dd:ee:02"
    ssh_key: `+key+`
`)
	rep := Build("dev", time.Now())
	if _, ok := noticeByCode(rep, "missing_ssh_key"); ok {
		t.Errorf("unexpected missing_ssh_key: %+v", rep.Notices)
	}
}

func TestSyncOKFresh(t *testing.T) {
	dir := setup(t, localHostYAML)
	t.Setenv("CONFIG_GIT_REMOTE", "git@example.com:cfg.git")
	now := time.Now()
	writeState(t, dir, `{"status":"ok","rev":"abc123","at":"`+now.UTC().Format(time.RFC3339)+`"}`)

	rep := Build("dev", now)
	if rep.Sync == nil || rep.Sync.Status != "ok" || rep.Sync.Rev != "abc123" {
		t.Fatalf("Sync = %+v", rep.Sync)
	}
	if rep.Sync.AgeSeconds == nil {
		t.Error("AgeSeconds nil")
	}
	if rep.Status != statusOK {
		t.Errorf("status = %q, want ok; notices %+v", rep.Status, rep.Notices)
	}
}

func TestSyncStale(t *testing.T) {
	dir := setup(t, localHostYAML)
	now := time.Now()
	old := now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	writeState(t, dir, `{"status":"ok","rev":"abc","at":"`+old+`"}`)

	rep := Build("dev", now)
	if _, ok := noticeByCode(rep, "sync_stale"); !ok {
		t.Fatalf("missing sync_stale notice: %+v", rep.Notices)
	}
	if rep.Status != statusDegraded {
		t.Errorf("status = %q, want degraded", rep.Status)
	}
}

func TestStaleAfterEnv(t *testing.T) {
	dir := setup(t, localHostYAML)
	t.Setenv("LANCTL_STALE_AFTER", "60")
	now := time.Now()
	recent := now.Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	writeState(t, dir, `{"status":"ok","rev":"abc","at":"`+recent+`"}`)

	rep := Build("dev", now)
	if _, ok := noticeByCode(rep, "sync_stale"); !ok {
		t.Fatalf("missing sync_stale notice with LANCTL_STALE_AFTER=60: %+v", rep.Notices)
	}
}

func TestSyncFailed(t *testing.T) {
	dir := setup(t, localHostYAML)
	writeState(t, dir, `{"status":"failed","rev":"bad","at":"2026-01-01T00:00:00Z"}`)
	writeFile(t, filepath.Join(dir, ".config-sync", "FAILED"), "staged config rejected: boom\n")

	rep := Build("dev", time.Now())
	n, ok := noticeByCode(rep, "sync_failed")
	if !ok {
		t.Fatalf("missing sync_failed notice: %+v", rep.Notices)
	}
	if n.Level != LevelError {
		t.Errorf("level = %q, want error", n.Level)
	}
	if rep.Sync == nil || rep.Sync.Detail != "staged config rejected: boom" {
		t.Errorf("Sync.Detail = %+v", rep.Sync)
	}
	if rep.Status != statusError {
		t.Errorf("status = %q, want error", rep.Status)
	}
}

func TestSyncStates(t *testing.T) {
	cases := []struct {
		state     string
		wantCode  string
		wantLevel Level
	}{
		{"pending", "sync_pending", LevelInfo},
		{"held", "sync_held_state", LevelWarning},
		{"aborted", "sync_aborted", LevelWarning},
		{"else", "sync_unknown", LevelWarning},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			dir := setup(t, localHostYAML)
			writeState(t, dir, `{"status":"`+tc.state+`","rev":"r","at":"2026-01-01T00:00:00Z"}`)
			rep := Build("dev", time.Now())
			n, ok := noticeByCode(rep, tc.wantCode)
			if !ok {
				t.Fatalf("missing %s notice: %+v", tc.wantCode, rep.Notices)
			}
			if n.Level != tc.wantLevel {
				t.Errorf("level = %q, want %q", n.Level, tc.wantLevel)
			}
		})
	}
}

func TestSyncStateUnreadable(t *testing.T) {
	dir := setup(t, localHostYAML)
	writeState(t, dir, "not json")
	rep := Build("dev", time.Now())
	if _, ok := noticeByCode(rep, "sync_state_unreadable"); !ok {
		t.Fatalf("missing sync_state_unreadable: %+v", rep.Notices)
	}
	if rep.Sync != nil {
		t.Errorf("Sync = %+v, want nil", rep.Sync)
	}
}

func TestNeverSynced(t *testing.T) {
	setup(t, localHostYAML)
	t.Setenv("CONFIG_GIT_REMOTE", "git@example.com:cfg.git")
	rep := Build("dev", time.Now())
	if _, ok := noticeByCode(rep, "never_synced"); !ok {
		t.Fatalf("missing never_synced: %+v", rep.Notices)
	}
}

func TestNoSyncConfiguredIsQuiet(t *testing.T) {
	setup(t, localHostYAML)
	rep := Build("dev", time.Now())
	if _, ok := noticeByCode(rep, "never_synced"); ok {
		t.Errorf("unexpected never_synced without CONFIG_GIT_REMOTE: %+v", rep.Notices)
	}
}

func TestSyncHeldSentinel(t *testing.T) {
	dir := setup(t, localHostYAML)
	writeFile(t, filepath.Join(dir, "SYNC_DISABLED"), "")
	rep := Build("dev", time.Now())
	if _, ok := noticeByCode(rep, "sync_held"); !ok {
		t.Fatalf("missing sync_held: %+v", rep.Notices)
	}
}

func TestSyncHeldSentinelEnv(t *testing.T) {
	setup(t, localHostYAML)
	custom := filepath.Join(t.TempDir(), "HOLD")
	writeFile(t, custom, "")
	t.Setenv("SYNC_DISABLED_FILE", custom)
	rep := Build("dev", time.Now())
	if _, ok := noticeByCode(rep, "sync_held"); !ok {
		t.Fatalf("missing sync_held via SYNC_DISABLED_FILE: %+v", rep.Notices)
	}
}

func TestFailedFileWithoutState(t *testing.T) {
	dir := setup(t, localHostYAML)
	writeFile(t, filepath.Join(dir, ".config-sync", "FAILED"), "clone failed\n")
	rep := Build("dev", time.Now())
	n, ok := noticeByCode(rep, "sync_failed")
	if !ok {
		t.Fatalf("missing sync_failed: %+v", rep.Notices)
	}
	if n.Level != LevelError {
		t.Errorf("level = %q, want error", n.Level)
	}
}

func TestDeployDirFallsBackToCwd(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hosts.yaml"), localHostYAML)
	t.Chdir(dir)
	rep := Build("dev", time.Now())
	if !rep.ConfigOK {
		t.Fatalf("ConfigOK = false: %s", rep.ConfigError)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		secs int64
		want string
	}{
		{30, "30s"},
		{90, "1m"},
		{3700, "1.0h"},
		{90000, "1.0d"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.secs); got != tc.want {
			t.Errorf("humanAge(%d) = %q, want %q", tc.secs, got, tc.want)
		}
	}
}

func TestOverall(t *testing.T) {
	if got := overall(nil); got != statusOK {
		t.Errorf("overall(nil) = %q", got)
	}
	if got := overall([]Notice{{Level: LevelInfo}}); got != statusOK {
		t.Errorf("info => %q, want ok", got)
	}
	if got := overall([]Notice{{Level: LevelInfo}, {Level: LevelWarning}}); got != statusDegraded {
		t.Errorf("warning => %q, want degraded", got)
	}
	if got := overall([]Notice{{Level: LevelWarning}, {Level: LevelError}}); got != statusError {
		t.Errorf("error => %q, want error", got)
	}
}

func TestStaleAfterDefault(t *testing.T) {
	cleanEnv(t)
	if got := staleAfter(); got != defaultStaleAfter {
		t.Errorf("staleAfter() = %v, want %v", got, defaultStaleAfter)
	}
	t.Setenv("LANCTL_STALE_AFTER", "120")
	if got := staleAfter(); got != 120*time.Second {
		t.Errorf("staleAfter() = %v, want 120s", got)
	}
	t.Setenv("LANCTL_STALE_AFTER", "bogus")
	if got := staleAfter(); got != defaultStaleAfter {
		t.Errorf("staleAfter() = %v, want default", got)
	}
	t.Setenv("LANCTL_STALE_AFTER", "-5")
	if got := staleAfter(); got != defaultStaleAfter {
		t.Errorf("staleAfter() = %v, want default", got)
	}
}

func TestDeployDirTilde(t *testing.T) {
	cleanEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEPLOY_DIR", "~/deploy")
	if got := deployDir(); got != filepath.Join(home, "deploy") {
		t.Errorf("deployDir() = %q, want %q", got, filepath.Join(home, "deploy"))
	}
}
