package sshops

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// dial opens an SSH connection using public-key auth, ignoring host key verification.
func dial(ip, user, keyPath string) (*ssh.Client, error) {
	keyPath = expandTilde(keyPath)
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read SSH key %s: %w", keyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse SSH key: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec — matches Python known_hosts=None
		Timeout:         5 * time.Second,
	}
	return ssh.Dial("tcp", ip+":22", cfg)
}

// runCmd executes a command over an existing SSH client and returns (stdout+stderr, exit_code, error).
// A non-zero exit code from the remote command is not treated as an error.
func runCmd(client *ssh.Client, cmd string) (string, int, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", -1, err
	}
	defer session.Close()

	out, err := session.CombinedOutput(cmd)
	if err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitStatus(), nil
		}
		return string(out), -1, err
	}
	return string(out), 0, nil
}

// ServiceStatuses runs `systemctl is-active <services...>` via SSH and returns a status map.
func ServiceStatuses(ip, user, keyPath string, services []string) (map[string]string, error) {
	client, err := dial(ip, user, keyPath)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	cmd := "systemctl is-active " + strings.Join(services, " ")
	out, _, err := runCmd(client, cmd)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
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

// ServiceControl runs `sudo systemctl <action> <service>` via SSH.
func ServiceControl(ip, user, keyPath, service, action string) error {
	client, err := dial(ip, user, keyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	out, exitCode, err := runCmd(client, fmt.Sprintf("sudo systemctl %s %s", action, service))
	if err != nil {
		return err
	}
	if exitCode != 0 {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = fmt.Sprintf("systemctl %s %s failed (exit %d)", action, service, exitCode)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// Shutdown runs `sudo shutdown -h now` via SSH. Connection drops before the command returns;
// EOF and connection-reset errors are silently ignored.
func Shutdown(ip, user, keyPath string) error {
	client, err := dial(ip, user, keyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	_, _, err = runCmd(client, "sudo shutdown -h now")
	if err != nil {
		s := err.Error()
		if strings.Contains(s, "EOF") || strings.Contains(s, "connection reset") {
			return nil
		}
		return err
	}
	return nil
}

// JournalLines returns the last n lines of a service's journal via SSH.
func JournalLines(ip, user, keyPath, service string, n int) (string, error) {
	client, err := dial(ip, user, keyPath)
	if err != nil {
		return "", err
	}
	defer client.Close()

	cmd := fmt.Sprintf("journalctl -u %s -n %d --no-pager --output=short-iso", service, n)
	out, _, err := runCmd(client, cmd)
	if err != nil {
		return "", err
	}
	return out, nil
}

// StreamJournal streams journalctl -f for a service via SSE over SSH.
// Sends a SSE heartbeat comment every 15 seconds to keep the connection alive.
func StreamJournal(ip, user, keyPath, service string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	client, err := dial(ip, user, keyPath)
	if err != nil {
		fmt.Fprintf(w, "data: [SSH error] %v\n\n", err)
		flusher.Flush()
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		fmt.Fprintf(w, "data: [SSH error] %v\n\n", err)
		flusher.Flush()
		return
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		fmt.Fprintf(w, "data: [error] %v\n\n", err)
		flusher.Flush()
		return
	}

	cmd := fmt.Sprintf("journalctl -u %s -f -n 50 --no-pager --output=short-iso", service)
	if err := session.Start(cmd); err != nil {
		fmt.Fprintf(w, "data: [error] %v\n\n", err)
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
			session.Signal(ssh.SIGTERM)
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

// StreamVPNRepair runs the NordVPN repair sequence on the remote host and streams
// progress lines as SSE. The caller must set SSE response headers before calling.
func StreamVPNRepair(ip, user, keyPath, token string, w http.ResponseWriter, r *http.Request) {
	flusher, _ := w.(http.Flusher)
	sse := func(msg string) {
		fmt.Fprintf(w, "data: %s\n\n", msg)
		if flusher != nil {
			flusher.Flush()
		}
	}

	sse(fmt.Sprintf("Connecting to %s via LAN SSH...", ip))

	client, err := dial(ip, user, keyPath)
	if err != nil {
		sse(fmt.Sprintf("[SSH error] %v", err))
		return
	}
	defer client.Close()

	sse("Connected.")
	sse("")

	run := func(cmd string) (string, int) {
		out, code, err := runCmd(client, cmd)
		if err != nil {
			return err.Error(), -1
		}
		return strings.TrimRight(out, "\n"), code
	}
	emitLines := func(out string) {
		for _, line := range strings.Split(out, "\n") {
			sse("  " + line)
		}
	}

	// Step 1: login
	sse("[1/3] Checking NordVPN login status...")
	out, rc := run("nordvpn account")
	emitLines(out)
	if rc != 0 || strings.Contains(strings.ToLower(out), "not logged in") {
		sse("[1/3] Not logged in — authenticating with token...")
		out, rc = run("nordvpn login --token " + token)
		emitLines(out)
		if rc != 0 {
			sse("[FAIL] Login failed — check token in hosts.yaml")
			return
		}
		sse("[1/3] Logged in.")
	} else {
		sse("[1/3] Already logged in.")
	}
	sse("")

	// Step 2: meshnet
	sse("[2/3] Checking meshnet status...")
	out, _ = run("nordvpn meshnet")
	emitLines(out)
	if strings.Contains(strings.ToLower(out), "disabled") {
		sse("[2/3] Meshnet disabled — enabling...")
		out, _ = run("nordvpn set meshnet on")
		emitLines(out)
	} else {
		sse("[2/3] Meshnet already enabled.")
	}
	sse("")

	// Step 3: peer list
	sse("[3/3] Verifying meshnet peer list...")
	out, rc = run("nordvpn meshnet peer list")
	emitLines(out)
	if rc != 0 || strings.Contains(strings.ToLower(out), "error") {
		sse("[3/3] Peer list failed — restarting nordvpnd daemon...")
		out, _ = run("sudo systemctl restart nordvpnd")
		emitLines(out)

		sse("[3/3] Waiting for daemon to come up (4s)...")
		time.Sleep(4 * time.Second)

		sse("[3/3] Re-enabling meshnet after restart...")
		out, _ = run("nordvpn set meshnet on")
		emitLines(out)
		sse("")

		sse("[3/3] Final verification...")
		out, rc = run("nordvpn meshnet peer list")
		emitLines(out)
		if rc != 0 {
			sse("")
			sse("[FAIL] Repair unsuccessful — manual intervention needed.")
		} else {
			sse("")
			sse("[OK] Repair successful.")
		}
	} else {
		sse("[OK] Meshnet peers reachable — done.")
	}
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home := os.Getenv("HOME")
		if home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
