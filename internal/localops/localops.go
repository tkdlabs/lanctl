package localops

import (
	"bufio"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Shutdown runs `sudo shutdown -h now` locally.
func Shutdown() error {
	return exec.Command("sudo", "shutdown", "-h", "now").Run()
}

// ServiceStatuses runs `systemctl is-active` for each service and returns a map.
// systemctl exits non-zero when any service is inactive, so we ignore the error.
func ServiceStatuses(services []string) (map[string]string, error) {
	args := append([]string{"is-active"}, services...)
	cmd := exec.Command("systemctl", args...)
	out, _ := cmd.Output()
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	statuses := make(map[string]string, len(services))
	for i, svc := range services {
		if i < len(lines) && lines[i] != "" {
			statuses[svc] = strings.TrimSpace(lines[i])
		} else {
			statuses[svc] = "unknown"
		}
	}
	return statuses, nil
}

// ServiceControl runs `sudo systemctl <action> <service>` locally.
func ServiceControl(service, action string) error {
	cmd := exec.Command("sudo", "systemctl", action, service)
	out, err := cmd.CombinedOutput()
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
	cmd := exec.Command("journalctl", "-u", service, "-n", fmt.Sprintf("%d", n), "--no-pager", "--output=short-iso")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// StreamJournal streams journalctl -f output to w via SSE until r.Context() is done.
func StreamJournal(service string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	cmd := exec.CommandContext(r.Context(), "journalctl", "-u", service, "-f", "-n", "50", "--no-pager", "--output=short-iso")
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
