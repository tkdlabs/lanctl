package localops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	osuser "os/user"
	"strconv"
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

// runUser executes a command against a user's systemd manager. An empty user,
// or the process user, runs directly with XDG_RUNTIME_DIR defaulted; a
// different user is reached through sudo (the process must be root). It is a
// package variable so tests can substitute a fake.
var runUser = func(user, name string, args ...string) ([]byte, error) {
	cmdName, cmdArgs, env, err := userCommand(user, name, args...)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(cmdName, cmdArgs...)
	if env != nil {
		cmd.Env = env
	}
	return cmd.Output()
}

// lookupUserID resolves a user name (or numeric UID) to its numeric UID.
var lookupUserID = func(name string) (int, error) {
	if uid, err := strconv.Atoi(name); err == nil {
		return uid, nil
	}
	u, err := osuser.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

// userCommand builds the command that runs name/args in user's systemd
// instance. An empty user, or the process user, runs directly with
// XDG_RUNTIME_DIR defaulted. A different user requires root and is reached
// through sudo with that user's runtime directory.
func userCommand(user, name string, args ...string) (string, []string, []string, error) {
	return userCommandFor(user, name, args, os.Getuid(), os.Geteuid(), lookupUserID)
}

func userCommandFor(user, name string, args []string, uid, euid int, lookup func(string) (int, error)) (string, []string, []string, error) {
	if user == "" {
		return name, args, runtimeDirEnv(), nil
	}
	targetUID, err := lookup(user)
	if err != nil {
		return "", nil, nil, fmt.Errorf("resolve user %q: %w", user, err)
	}
	if targetUID == uid {
		return name, args, runtimeDirEnv(), nil
	}
	if euid != 0 {
		return "", nil, nil, fmt.Errorf("cannot run user-scoped command as %q: lanctl runs as uid %d and is not root", user, uid)
	}
	sudoArgs := []string{"-n", "-u", user, "env", fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", targetUID), name}
	sudoArgs = append(sudoArgs, args...)
	return "sudo", sudoArgs, nil, nil
}

// runtimeDirEnv returns the current environment with XDG_RUNTIME_DIR defaulted
// to the current user's runtime directory when it is not already set.
func runtimeDirEnv() []string {
	env := os.Environ()
	for _, e := range env {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			return env
		}
	}
	return append(env, fmt.Sprintf("XDG_RUNTIME_DIR=/run/user/%d", os.Getuid()))
}

// systemctlArgs builds `systemctl [--user] <verb> <services...>`.
func systemctlArgs(userScope bool, verb string, services ...string) []string {
	args := make([]string, 0, len(services)+2)
	if userScope {
		args = append(args, "--user")
	}
	args = append(args, verb)
	return append(args, services...)
}

// journalctlArgs builds `journalctl [--user] -u <service> [-f] -n N ...`.
func journalctlArgs(userScope bool, service string, n int, follow bool) []string {
	args := make([]string, 0, 8)
	if userScope {
		args = append(args, "--user")
	}
	args = append(args, "-u", service)
	if follow {
		args = append(args, "-f")
	}
	return append(args, "-n", fmt.Sprintf("%d", n), "--no-pager", "--output=short-iso")
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

// ServiceStatuses runs `systemctl [--user] is-active` for each service and
// returns a map. systemctl exits non-zero when any service is inactive, so we
// ignore exit errors; setup errors (unknown user, privilege switching) are
// returned.
func ServiceStatuses(services []string, userScope bool, user string) (map[string]string, error) {
	args := systemctlArgs(userScope, "is-active", services...)
	var out []byte
	var err error
	if userScope {
		out, err = runUser(user, "systemctl", args...)
	} else {
		out, err = run("systemctl", args...)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, err
		}
	}
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

// ServiceControl runs `sudo systemctl <action> <service>` for system services
// and `systemctl --user <action> <service>` for user services, switching to
// the configured user when lanctl itself runs as root.
func ServiceControl(service, action string, userScope bool, user string) error {
	args := systemctlArgs(userScope, action, service)
	var out []byte
	var err error
	if userScope {
		out, err = runUser(user, "systemctl", args...)
	} else {
		out, err = runSudo("systemctl", args...)
	}
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
func JournalLines(service string, n int, userScope bool, user string) (string, error) {
	args := journalctlArgs(userScope, service, n, false)
	var out []byte
	var err error
	if userScope {
		out, err = runUser(user, "journalctl", args...)
	} else {
		out, err = run("journalctl", args...)
	}
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

// streamUserCmd builds the stream command for a user-scoped journal read.
func streamUserCmd(ctx context.Context, user, name string, args ...string) (*exec.Cmd, error) {
	cmdName, cmdArgs, env, err := userCommand(user, name, args...)
	if err != nil {
		return nil, err
	}
	cmd := streamCmd(ctx, cmdName, cmdArgs...)
	if env != nil {
		cmd.Env = env
	}
	return cmd, nil
}

// StreamJournal streams journalctl -f output to w via SSE until r.Context() is done.
func StreamJournal(service string, userScope bool, user string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	args := journalctlArgs(userScope, service, 50, true)
	var cmd *exec.Cmd
	if userScope {
		var err error
		cmd, err = streamUserCmd(r.Context(), user, "journalctl", args...)
		if err != nil {
			fmt.Fprintf(w, "data: [error: %v]\n\n", err)
			flusher.Flush()
			return
		}
	} else {
		cmd = streamCmd(r.Context(), "journalctl", args...)
	}
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
