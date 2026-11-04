package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/tkdlabs/lanctl/internal/client"
)

var validActions = map[string]bool{"start": true, "stop": true, "restart": true}
var validPlugActions = map[string]bool{"status": true, "on": true, "off": true, "cycle": true}

// actionResult is the JSON shape printed by mutating commands with -o json.
type actionResult struct {
	Action  string   `json:"action"`
	Host    string   `json:"host,omitempty"`
	VM      string   `json:"vm,omitempty"`
	Service string   `json:"service,omitempty"`
	Plug    string   `json:"plug,omitempty"`
	Affects []string `json:"affects,omitempty"`
	On      *bool    `json:"on,omitempty"`
	Status  string   `json:"status,omitempty"`
}

func newFlagSet(name string, o *options) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(o.stderr)
	return fs
}

// report emits a command result: JSON to stdout, or a status line to stderr.
func report(o *options, res actionResult, msg string) int {
	if o.json() {
		if err := writeJSON(o.stdout, res); err != nil {
			return fail(o, err)
		}
		return exitOK
	}
	if !o.quiet {
		fmt.Fprintln(o.stderr, msg)
	}
	return exitOK
}

func targetLabel(host, vm string) string {
	if vm != "" {
		return host + "/" + vm
	}
	return host
}

func cmdHosts(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("hosts", o)
	online := fs.Bool("online", false, "only list online hosts")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 0 {
		fmt.Fprintln(o.stderr, "lanctl-cli: hosts takes no arguments")
		return exitUsage
	}

	hosts, err := c.Hosts(ctx)
	if err != nil {
		return fail(o, err)
	}
	if *online {
		hosts = filterOnline(hosts)
	}
	if err := renderHosts(o.stdout, hosts, o.output, o.color()); err != nil {
		return fail(o, err)
	}
	return exitOK
}

func cmdWake(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("wake", o)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 1 {
		fmt.Fprintln(o.stderr, "lanctl-cli: usage: wake HOST")
		return exitUsage
	}
	host, vm := parseTarget(rest[0])
	if vm != "" {
		fmt.Fprintln(o.stderr, "lanctl-cli: wake does not support a VM target")
		return exitUsage
	}

	status, err := c.Wake(ctx, host)
	if err != nil {
		return fail(o, err)
	}
	return report(o, actionResult{Action: "wake", Host: host, Status: status}, fmt.Sprintf("%s: %s", host, status))
}

func cmdShutdown(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("shutdown", o)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 1 {
		fmt.Fprintln(o.stderr, "lanctl-cli: usage: shutdown HOST[/VM]")
		return exitUsage
	}
	host, vm := parseTarget(rest[0])

	ok, err := confirm(o, "Shut down "+targetLabel(host, vm)+"?")
	if err != nil {
		return fail(o, err)
	}
	if !ok {
		if !o.quiet {
			fmt.Fprintln(o.stderr, "aborted")
		}
		return exitOK
	}

	if err := c.Shutdown(ctx, host, vm); err != nil {
		return fail(o, err)
	}
	return report(o, actionResult{Action: "shutdown", Host: host, VM: vm, Status: "shutdown initiated"},
		"shutdown initiated for "+targetLabel(host, vm))
}

func cmdService(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("service", o)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 3 {
		fmt.Fprintln(o.stderr, "lanctl-cli: usage: service HOST[/VM] SERVICE start|stop|restart")
		return exitUsage
	}
	host, vm := parseTarget(rest[0])
	service, action := rest[1], rest[2]
	if !validActions[action] {
		fmt.Fprintf(o.stderr, "lanctl-cli: invalid action %q (want start, stop, or restart)\n", action)
		return exitUsage
	}

	if err := c.ServiceControl(ctx, host, vm, service, action); err != nil {
		return fail(o, err)
	}
	status := action + " initiated"
	return report(o, actionResult{Action: action, Host: host, VM: vm, Service: service, Status: status},
		fmt.Sprintf("%s: %s", status, service))
}

func cmdLogs(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("logs", o)
	n := fs.Int("n", 100, "number of lines to fetch")
	follow := fs.Bool("f", false, "follow the log stream")
	fs.BoolVar(follow, "follow", false, "follow the log stream")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 2 {
		fmt.Fprintln(o.stderr, "lanctl-cli: usage: logs HOST[/VM] SERVICE [-n N] [-f]")
		return exitUsage
	}
	host, vm := parseTarget(rest[0])
	service := rest[1]

	if *follow {
		return fail(o, c.StreamLogs(ctx, host, vm, service, o.stdout))
	}

	lines, err := c.Logs(ctx, host, vm, service, *n)
	if err != nil {
		return fail(o, err)
	}
	if o.json() {
		if err := writeJSON(o.stdout, lines); err != nil {
			return fail(o, err)
		}
		return exitOK
	}
	for _, line := range lines {
		fmt.Fprintln(o.stdout, line)
	}
	return exitOK
}

func cmdVPNRepair(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("vpn-repair", o)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 1 {
		fmt.Fprintln(o.stderr, "lanctl-cli: usage: vpn-repair HOST[/VM]")
		return exitUsage
	}
	host, vm := parseTarget(rest[0])
	return fail(o, c.VPNRepair(ctx, host, vm, o.stdout))
}

func cmdPlugs(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("plugs", o)
	host := fs.String("host", "", "only plugs that affect this host")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 0 {
		fmt.Fprintln(o.stderr, "lanctl-cli: plugs takes no arguments")
		return exitUsage
	}
	plugs, err := c.Plugs(ctx)
	if err != nil {
		return fail(o, err)
	}
	if *host != "" {
		filtered := make([]client.Plug, 0, len(plugs))
		for _, p := range plugs {
			for _, name := range p.Affects {
				if name == *host {
					filtered = append(filtered, p)
					break
				}
			}
		}
		plugs = filtered
	}
	if err := renderPlugs(o.stdout, plugs, o.output, o.color()); err != nil {
		return fail(o, err)
	}
	return exitOK
}

func cmdPlug(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("plug", o)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 2 {
		fmt.Fprintln(o.stderr, "lanctl-cli: usage: plug status|on|off|cycle NAME")
		return exitUsage
	}
	action, name := rest[0], rest[1]
	if !validPlugActions[action] {
		fmt.Fprintf(o.stderr, "lanctl-cli: invalid plug action %q (want status, on, off, or cycle)\n", action)
		return exitUsage
	}

	if action == "status" {
		plug, err := c.PlugStatus(ctx, name)
		if err != nil {
			return fail(o, err)
		}
		if o.json() {
			if err := writeJSON(o.stdout, plug); err != nil {
				return fail(o, err)
			}
			return exitOK
		}
		line := fmt.Sprintf("%s: %s is %s", plug.Name, plugLabel(plug.DeviceName, plug.DeviceID), stateLabel(plug.On))
		if len(plug.Affects) > 0 {
			line += " (affects: " + strings.Join(plug.Affects, ", ") + ")"
		}
		fmt.Fprintln(o.stdout, line)
		return exitOK
	}

	// Fetch status first so confirmations can name the affected hosts.
	plug, err := c.PlugStatus(ctx, name)
	if err != nil {
		return fail(o, err)
	}
	affectsWarning := ""
	if len(plug.Affects) > 0 {
		affectsWarning = " This will affect hosts: " + strings.Join(plug.Affects, ", ") + ", which depend on this plug."
	}
	switch action {
	case "on":
		if len(plug.Affects) > 0 {
			ok, err := confirm(o, "Turn on plug "+name+"?"+affectsWarning)
			if err != nil {
				return fail(o, err)
			}
			if !ok {
				if !o.quiet {
					fmt.Fprintln(o.stderr, "aborted")
				}
				return exitOK
			}
		}
	case "off":
		ok, err := confirm(o, "Turn off plug "+name+"? This cuts power immediately."+affectsWarning)
		if err != nil {
			return fail(o, err)
		}
		if !ok {
			if !o.quiet {
				fmt.Fprintln(o.stderr, "aborted")
			}
			return exitOK
		}
	case "cycle":
		ok, err := confirm(o, "Power-cycle plug "+name+"? This cuts power immediately and turns it back on."+affectsWarning)
		if err != nil {
			return fail(o, err)
		}
		if !ok {
			if !o.quiet {
				fmt.Fprintln(o.stderr, "aborted")
			}
			return exitOK
		}
	}

	if action == "cycle" {
		// The server waits out the off delay, so the normal 15s client
		// timeout is too short. A user-supplied longer timeout still wins.
		c = client.New(o.server, client.WithTimeout(cycleTimeout(o.timeout)))
	}
	res, err := c.PlugControl(ctx, name, action)
	if err != nil {
		return fail(o, err)
	}
	on := res.On
	return report(o, actionResult{Action: action, Plug: res.Plug, Affects: res.Affects, On: &on, Status: res.Status},
		fmt.Sprintf("%s: %s", name, res.Status))
}

func cmdHubspace(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("hubspace", o)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 1 || rest[0] != "devices" {
		fmt.Fprintln(o.stderr, "lanctl-cli: usage: hubspace devices")
		return exitUsage
	}
	devices, err := c.HubspaceDevices(ctx)
	if err != nil {
		return fail(o, err)
	}
	if err := renderHubspaceDevices(o.stdout, devices, o.output); err != nil {
		return fail(o, err)
	}
	return exitOK
}

// cycleTimeout gives a power cycle enough time for the configured off delay.
func cycleTimeout(timeout time.Duration) time.Duration {
	const min = 2 * time.Minute
	if timeout < min {
		return min
	}
	return timeout
}

func cmdVersion(ctx context.Context, o *options, c *client.Client, args []string) int {
	fs := newFlagSet("version", o)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return usageError(o, err)
	}
	if len(rest) != 0 {
		fmt.Fprintln(o.stderr, "lanctl-cli: version takes no arguments")
		return exitUsage
	}
	v, err := c.Version(ctx)
	if err != nil {
		return fail(o, err)
	}
	if o.json() {
		if err := writeJSON(o.stdout, map[string]string{"version": v}); err != nil {
			return fail(o, err)
		}
		return exitOK
	}
	fmt.Fprintln(o.stdout, v)
	return exitOK
}

// confirm asks for confirmation unless --yes or JSON output is set. When stdin
// is not a terminal, it refuses rather than blocking or assuming yes.
func confirm(o *options, prompt string) (bool, error) {
	if o.yes || o.json() {
		return true, nil
	}
	if !o.stdinTTY {
		return false, errors.New("refusing to proceed non-interactively; pass --yes")
	}
	fmt.Fprintf(o.stderr, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(o.stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
