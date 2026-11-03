package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tkdlabs/lanctl/internal/client"
)

const (
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorReset = "\033[0m"
)

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func renderHosts(w io.Writer, hosts []client.Host, format string, color bool) error {
	switch format {
	case "json":
		return writeJSON(w, hosts)
	case "plain":
		renderHostsPlain(w, hosts)
		return nil
	default:
		renderHostsTable(w, hosts, color)
		return nil
	}
}

func renderHostsTable(w io.Writer, hosts []client.Host, color bool) {
	headers := []string{"NAME", "TYPE", "IP", "ONLINE", "SERVICES", "VPN"}
	var rows [][]string
	var online []bool
	for _, h := range hosts {
		rows = append(rows, hostRow(h))
		online = append(online, h.Online)
		if h.VMs != nil {
			for _, vm := range *h.VMs {
				rows = append(rows, vmRow(vm))
				online = append(online, vm.Online)
			}
		}
	}
	writeTable(w, headers, rows, online, color)
}

func renderHostsPlain(w io.Writer, hosts []client.Host) {
	for _, h := range hosts {
		fmt.Fprintln(w, strings.Join(hostRow(h), "\t"))
		if h.VMs != nil {
			for _, vm := range *h.VMs {
				row := []string{
					h.Name + "/" + vm.Name,
					"vm",
					vm.IP,
					onlineLabel(vm.Online),
					formatServices(vm.Services, vm.UserServices, vm.ServiceStatuses),
					formatVPN(vm.VPNHostname, vm.VPNReachable),
				}
				fmt.Fprintln(w, strings.Join(row, "\t"))
			}
		}
	}
}

func hostRow(h client.Host) []string {
	return []string{
		h.Name,
		typeLabel(h.Type),
		h.IP,
		onlineLabel(h.Online),
		formatServices(h.Services, h.UserServices, h.ServiceStatuses),
		formatVPN(h.VPNHostname, h.VPNReachable),
	}
}

func vmRow(vm client.VM) []string {
	return []string{
		"  " + vm.Name,
		"vm",
		vm.IP,
		onlineLabel(vm.Online),
		formatServices(vm.Services, vm.UserServices, vm.ServiceStatuses),
		formatVPN(vm.VPNHostname, vm.VPNReachable),
	}
}

func writeTable(w io.Writer, headers []string, rows [][]string, online []bool, color bool) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	writeRow := func(cells []string, rowOnline int) {
		for i, cell := range cells {
			last := i == len(cells)-1
			if last {
				fmt.Fprint(w, cell)
				break
			}
			padded := pad(cell, widths[i])
			if color && i == 3 && rowOnline >= 0 {
				padded = colorize(online[rowOnline], padded)
			}
			fmt.Fprint(w, padded, "  ")
		}
		fmt.Fprintln(w)
	}

	writeRow(headers, -1)
	for i, row := range rows {
		writeRow(row, i)
	}
}

func colorize(isOnline bool, s string) string {
	if isOnline {
		return colorGreen + s + colorReset
	}
	return colorRed + s + colorReset
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func filterOnline(hosts []client.Host) []client.Host {
	out := make([]client.Host, 0, len(hosts))
	for _, h := range hosts {
		if h.Online {
			out = append(out, h)
		}
	}
	return out
}

func typeLabel(t string) string {
	if t == "" {
		return "standard"
	}
	return t
}

func onlineLabel(online bool) string {
	if online {
		return "online"
	}
	return "offline"
}

// formatServices renders system and user services as a comma-separated list.
// User services are suffixed with "(user)" so the scope is visible in both the
// table and plain output.
func formatServices(services, userServices []string, statuses map[string]string) string {
	if len(services) == 0 && len(userServices) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(services)+len(userServices))
	appendSvc := func(s string, userScope bool) {
		label := s
		if userScope {
			label += "(user)"
		}
		if st := statuses[s]; st != "" {
			label += "=" + st
		}
		parts = append(parts, label)
	}
	for _, s := range services {
		appendSvc(s, false)
	}
	for _, s := range userServices {
		appendSvc(s, true)
	}
	return strings.Join(parts, ",")
}

func renderPlugs(w io.Writer, plugs []client.Plug, format string, color bool) error {
	switch format {
	case "json":
		return writeJSON(w, plugs)
	case "plain":
		for _, p := range plugs {
			fmt.Fprintf(w, "%s\t%s\t%s\n", p.Host, plugLabel(p.DeviceName, p.DeviceID), stateLabel(p.On))
		}
		return nil
	default:
		headers := []string{"HOST", "PLUG", "STATE"}
		widths := []int{len(headers[0]), len(headers[1])}
		for _, p := range plugs {
			if len(p.Host) > widths[0] {
				widths[0] = len(p.Host)
			}
			if label := plugLabel(p.DeviceName, p.DeviceID); len(label) > widths[1] {
				widths[1] = len(label)
			}
		}
		fmt.Fprintf(w, "%s  %s  %s\n", pad(headers[0], widths[0]), pad(headers[1], widths[1]), headers[2])
		for _, p := range plugs {
			label := plugLabel(p.DeviceName, p.DeviceID)
			state := stateLabel(p.On)
			if color {
				state = colorize(p.On, state)
			}
			fmt.Fprintf(w, "%s  %s  %s\n", pad(p.Host, widths[0]), pad(label, widths[1]), state)
		}
		return nil
	}
}

func renderHubspaceDevices(w io.Writer, devices []client.HubspaceDevice, format string) error {
	switch format {
	case "json":
		return writeJSON(w, devices)
	case "plain":
		for _, d := range devices {
			fmt.Fprintf(w, "%s\t%s\t%s\n", d.Name, d.ID, d.Class)
		}
		return nil
	default:
		headers := []string{"NAME", "ID", "CLASS"}
		widths := []int{len(headers[0]), len(headers[1]), len(headers[2])}
		for _, d := range devices {
			if len(d.Name) > widths[0] {
				widths[0] = len(d.Name)
			}
			if len(d.ID) > widths[1] {
				widths[1] = len(d.ID)
			}
			if len(d.Class) > widths[2] {
				widths[2] = len(d.Class)
			}
		}
		fmt.Fprintf(w, "%s  %s  %s\n", pad(headers[0], widths[0]), pad(headers[1], widths[1]), headers[2])
		for _, d := range devices {
			fmt.Fprintf(w, "%s  %s  %s\n", pad(d.Name, widths[0]), pad(d.ID, widths[1]), d.Class)
		}
		return nil
	}
}

func plugLabel(name, id string) string {
	if name != "" {
		return name
	}
	if id != "" {
		return id
	}
	return "-"
}

func stateLabel(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func formatVPN(hostname *string, reachable *bool) string {
	if hostname == nil || *hostname == "" {
		return "-"
	}
	if reachable == nil {
		return *hostname
	}
	if *reachable {
		return *hostname + " (reachable)"
	}
	return *hostname + " (unreachable)"
}
