// Command lanctl-cli is a shell client for a running lanctl server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tkdlabs/lanctl/internal/client"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// env carries the process I/O and terminal facts so execute is testable.
type env struct {
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	stdinTTY  bool
	stdoutTTY bool
}

// options holds resolved global settings passed to commands.
type options struct {
	server  string
	timeout time.Duration
	output  string
	quiet   bool
	noColor bool
	yes     bool

	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	stdinTTY  bool
	stdoutTTY bool
}

func (o *options) json() bool  { return o.output == "json" }
func (o *options) color() bool { return o.output == "table" && !o.noColor && o.stdoutTTY }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := execute(ctx, env{
		stdin:     os.Stdin,
		stdout:    os.Stdout,
		stderr:    os.Stderr,
		stdinTTY:  isTerminal(os.Stdin),
		stdoutTTY: isTerminal(os.Stdout),
	}, os.Args[1:])
	os.Exit(code)
}

func execute(ctx context.Context, e env, args []string) int {
	o := &options{
		stdin:     e.stdin,
		stdout:    e.stdout,
		stderr:    e.stderr,
		stdinTTY:  e.stdinTTY,
		stdoutTTY: e.stdoutTTY,
	}

	fs := flag.NewFlagSet("lanctl-cli", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() { usage(e.stderr) }

	var sShort, server, oShort, output string
	fs.StringVar(&sShort, "s", "", "server base URL")
	fs.StringVar(&server, "server", "", "server base URL")
	fs.DurationVar(&o.timeout, "timeout", client.DefaultTimeout, "non-streaming request timeout")
	fs.StringVar(&oShort, "o", "", "output format: table, json, plain")
	fs.StringVar(&output, "output", "", "output format: table, json, plain")
	fs.BoolVar(&o.quiet, "q", false, "suppress status messages")
	fs.BoolVar(&o.quiet, "quiet", false, "suppress status messages")
	fs.BoolVar(&o.noColor, "no-color", false, "disable ANSI color")
	fs.BoolVar(&o.yes, "y", false, "assume yes for confirmations")
	fs.BoolVar(&o.yes, "yes", false, "assume yes for confirmations")

	if err := fs.Parse(args); err != nil {
		return usageError(o, err)
	}

	o.server = firstNonEmpty(sShort, server, os.Getenv("LANCTL_URL"), client.DefaultBaseURL)
	o.output = firstNonEmpty(oShort, output, "table")
	switch o.output {
	case "table", "json", "plain":
	default:
		fmt.Fprintf(e.stderr, "lanctl-cli: invalid output format %q (want table, json, or plain)\n", o.output)
		return exitUsage
	}

	rest := fs.Args()
	if len(rest) == 0 {
		usage(e.stderr)
		return exitUsage
	}
	cmd, cargs := rest[0], rest[1:]

	c := client.New(o.server, client.WithTimeout(o.timeout))

	switch cmd {
	case "hosts", "ls":
		return cmdHosts(ctx, o, c, cargs)
	case "wake":
		return cmdWake(ctx, o, c, cargs)
	case "shutdown":
		return cmdShutdown(ctx, o, c, cargs)
	case "service":
		return cmdService(ctx, o, c, cargs)
	case "logs":
		return cmdLogs(ctx, o, c, cargs)
	case "vpn-repair":
		return cmdVPNRepair(ctx, o, c, cargs)
	case "version":
		return cmdVersion(ctx, o, c, cargs)
	case "help":
		usage(o.stdout)
		return exitOK
	default:
		fmt.Fprintf(e.stderr, "lanctl-cli: unknown command %q\n", cmd)
		usage(e.stderr)
		return exitUsage
	}
}

// fail reports err to stderr and maps it to an exit code. A user-initiated
// cancellation (Ctrl-C) is treated as success.
func fail(o *options, err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, context.Canceled) {
		return exitOK
	}
	fmt.Fprintln(o.stderr, "lanctl-cli: "+err.Error())
	return exitError
}

func usageError(o *options, err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	fmt.Fprintln(o.stderr, "lanctl-cli: "+err.Error())
	return exitUsage
}

func usage(w io.Writer) {
	fmt.Fprint(w, `lanctl-cli — shell client for a lanctl server

Usage:
  lanctl-cli [global flags] <command> [args]

Commands:
  hosts [--online]                     list hosts and VMs
  wake HOST                            send a Wake-on-LAN packet
  shutdown HOST[/VM]                   shut down a host or VM
  service HOST[/VM] SERVICE ACTION     start|stop|restart a service
  logs HOST[/VM] SERVICE [-n N] [-f]   show or follow journal lines
  vpn-repair HOST[/VM]                 run NordVPN repair
  version                              print the server version
  help                                 show this help

Global flags:
  -s, --server URL   server base URL (env LANCTL_URL, default `+client.DefaultBaseURL+`)
      --timeout DUR  non-streaming request timeout (default `+client.DefaultTimeout.String()+`)
  -o, --output FMT   table, json, or plain (default table)
  -q, --quiet        suppress status messages
      --no-color     disable ANSI color
  -y, --yes          skip shutdown confirmation
`)
}

// isTerminal reports whether v is an *os.File attached to a character device.
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// parseFlags parses flags allowing them to appear before or after positional
// arguments (the stdlib flag package stops at the first positional).
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	rest := args
	for len(rest) > 0 {
		if rest[0] == "-" || rest[0] == "" || !strings.HasPrefix(rest[0], "-") {
			positionals = append(positionals, rest[0])
			rest = rest[1:]
			continue
		}
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
	}
	return positionals, nil
}

// parseTarget splits "host" or "host/vm" into its parts.
func parseTarget(s string) (host, vm string) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}
