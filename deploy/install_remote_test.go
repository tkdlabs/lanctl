package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runRemoteInstaller runs deploy/install-remote.sh with args and returns
// combined output and the exit code. Tests pass --dry-run so nothing is
// installed and no SSH connection is made.
func runRemoteInstaller(t *testing.T, args ...string) (string, int) {
	t.Helper()
	script := filepath.Join("..", "deploy", "install-remote.sh")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running install-remote.sh: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return string(out), code
}

func TestDeployScriptsParse(t *testing.T) {
	for _, script := range []string{filepath.Join("..", "install.sh"), filepath.Join("..", "deploy", "install-remote.sh")} {
		if out, err := exec.Command("bash", "-n", script).CombinedOutput(); err != nil {
			t.Errorf("%s: bash -n failed: %v\n%s", script, err, out)
		}
	}
}

func TestInstallRemoteHelpMentionsEnvFile(t *testing.T) {
	out, code := runRemoteInstaller(t, "--help")
	if code != 0 {
		t.Fatalf("--help: exit %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "--env-file") {
		t.Errorf("usage does not mention --env-file:\n%s", out)
	}
}

func TestInstallRemoteEnvFileRequiresArgument(t *testing.T) {
	out, code := runRemoteInstaller(t, "--env-file")
	if code == 0 {
		t.Fatalf("--env-file without a value accepted:\n%s", out)
	}
	if !strings.Contains(out, "requires a file") {
		t.Errorf("expected a missing-argument error, got:\n%s", out)
	}
}

func TestInstallRemoteMissingEnvFile(t *testing.T) {
	out, code := runRemoteInstaller(t, "--host", "example.invalid", "--dry-run", "--env-file", "/nonexistent/lanctl.env")
	if code == 0 {
		t.Fatalf("missing env file accepted:\n%s", out)
	}
	if !strings.Contains(out, "not found") {
		t.Errorf("expected a not-found error, got:\n%s", out)
	}
}

func TestInstallRemoteDryRunHidesEnvFileContents(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "extra.env")
	const secret = "super-secret-token-value"
	if err := os.WriteFile(envPath, []byte("NORDVPN_TOKEN="+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	out, code := runRemoteInstaller(t, "--host", "example.invalid", "--dry-run", "--env-file", envPath)
	if code != 0 {
		t.Fatalf("dry-run failed (%d):\n%s", code, out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("dry-run leaked env file contents:\n%s", out)
	}
	if !strings.Contains(out, "contents not shown") {
		t.Errorf("dry-run does not describe the env file:\n%s", out)
	}
}
