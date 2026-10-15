// Package deploy tests the deployment shell scripts. It lives next to them and
// drives install.sh's argument handling with bash; every case here exits
// before the installer touches the system.
package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runInstallScript runs install.sh with args and returns combined output and
// the exit code. It never runs as root, so the installer stops at its root
// gate before changing anything.
func runInstallScript(t *testing.T, args ...string) (string, int) {
	t.Helper()
	script := filepath.Join("..", "install.sh")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("running install.sh: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return string(out), code
}

func TestInstallScriptHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		out, code := runInstallScript(t, flag)
		if code != 0 {
			t.Fatalf("%s: exit %d, want 0\n%s", flag, code, out)
		}
		if !strings.Contains(out, "--dir") {
			t.Errorf("%s: usage does not mention --dir:\n%s", flag, out)
		}
	}
}

func TestInstallScriptRejectsUnknownFlag(t *testing.T) {
	out, code := runInstallScript(t, "--deploy-dir", "/srv/app")
	if code == 0 {
		t.Fatalf("unknown flag accepted (exit 0):\n%s", out)
	}
	if !strings.Contains(out, "unknown option") {
		t.Errorf("expected an 'unknown option' error, got:\n%s", out)
	}
}

func TestInstallScriptDirRequiresArgument(t *testing.T) {
	out, code := runInstallScript(t, "--dir")
	if code == 0 {
		t.Fatalf("--dir without a value accepted:\n%s", out)
	}
	if !strings.Contains(out, "requires a directory argument") {
		t.Errorf("expected a missing-argument error, got:\n%s", out)
	}
}

// TestInstallScriptDirAccepted checks that --dir NAME parses (reaching the
// root gate) instead of being rejected as an unknown option. Skipped as root,
// where the installer would proceed past the gate and touch the system.
func TestInstallScriptDirAccepted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("refusing to run install.sh as root; it would reconfigure this machine")
	}
	out, code := runInstallScript(t, "--dir", "/srv/app")
	if code == 0 {
		t.Fatalf("expected to stop at the root gate, exited 0:\n%s", out)
	}
	if !strings.Contains(out, "must be run as root") {
		t.Errorf("--dir NAME did not reach the root gate:\n%s", out)
	}
}
