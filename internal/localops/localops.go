package localops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// run executes a command without elevation and returns its stdout.
// It is a package variable so tests can substitute a fake.
var run = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// runSudo executes a command via sudo and returns its combined output.
// It is a package variable so tests can substitute a fake.
var runSudo = func(name string, args ...string) ([]byte, error) {
	return exec.Command("sudo", append([]string{name}, args...)...).CombinedOutput()
}

// Shutdown runs `sudo shutdown -h now` locally.
//
// It refuses to run unless LANCTL_ALLOW_SHUTDOWN=1 is set. This prevents
// accidental power-offs from tests, dev runs, or shell mistakes.
func Shutdown() error {
	if os.Getenv("LANCTL_ALLOW_SHUTDOWN") != "1" {
		return errors.New("local shutdown disabled (set LANCTL_ALLOW_SHUTDOWN=1 to enable)")
	}
	_, err := runSudo("shutdown", "-h", "now")
	return err
}

// ServiceStatuses runs `systemctl is-active` for each service and returns a map.
// systemctl exits non-zero when any service is inactive, so we ignore the error.
func ServiceStatuses(services []string) (map[string]string, error) {
	args := append([]string{"is-active"}, services...)
	out, _ := run("systemctl", args...)
	return parseStatusOutput(string(out), services), nil
}

// parseStatusOutput maps newline-separated `systemctl is-active` output onto
// services by position. Missing or blank lines become "unknown".
func parseStatusOutput(out string, services []string) map[string]string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	statuses := make(map[string]string, len(services))
	for i, svc := range services {
		if i < len(lines) && lines[i] != "" {
			statuses[svc] = strings.TrimSpace(lines[i])
		} else {
			statuses[svc] = "unknown"
		}
	}
	return statuses
}

// ServiceControl runs `sudo systemctl <action> <service>` locally.
func ServiceControl(service, action string) error {
	out, err := runSudo("systemctl", action, service)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = fmt.Sprintf("systemctl %s %s failed", action, service)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// JournalLines returns the last n lines of a service's journal.
func JournalLines(service string, n int) (string, error) {
	out, err := run("journalctl", "-u", service, "-n", fmt.Sprintf("%d", n), "--no-pager", "--output=short-iso")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// streamCmd builds the command used by StreamJournal. It is a package
// variable so tests can substitute a fake process.
var streamCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// StreamJournal streams journalctl -f output to w via SSE until r.Context() is done.
func StreamJournal(service string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	cmd := streamCmd(r.Context(), "journalctl", "-u", service, "-f", "-n", "50", "--no-pager", "--output=short-iso")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(w, "data: [error: %v]\n\n", err)
		flusher.Flush()
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(w, "data: [error: %v]\n\n", err)
		flusher.Flush()
		return
	}

	lines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
