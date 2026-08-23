package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tom/ai-dev/frontends/lanctl-go/internal/config"
	lhttphandler "github.com/tom/ai-dev/frontends/lanctl-go/internal/httphandler"
	"github.com/tom/ai-dev/frontends/lanctl-go/internal/localops"
	"github.com/tom/ai-dev/frontends/lanctl-go/internal/network"
	"github.com/tom/ai-dev/frontends/lanctl-go/internal/sshops"
)

const sshTimeout = 1500 * time.Millisecond

var validActions = map[string]bool{"start": true, "stop": true, "restart": true}

// RegisterRoutes registers all /api/* routes on mux.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hosts", GetHosts)
	mux.HandleFunc("POST /api/hosts/{name}/wake", Wake)
	mux.HandleFunc("POST /api/hosts/{name}/shutdown", Shutdown)
	mux.HandleFunc("GET /api/hosts/{name}/services/{service}/logs/stream", StreamLogs)
	mux.HandleFunc("GET /api/hosts/{name}/services/{service}/logs", GetLogs)
	mux.HandleFunc("POST /api/hosts/{name}/services/{service}/{action}", ServiceControl)
	mux.HandleFunc("POST /api/hosts/{name}/vpn-repair", VPNRepair)
	mux.HandleFunc("POST /api/hosts/{name}/vms/{vm}/shutdown", VMShutdown)
	mux.HandleFunc("POST /api/hosts/{name}/vms/{vm}/vpn-repair", VMVPNRepair)
	mux.HandleFunc("GET /api/hosts/{name}/vms/{vm}/services/{service}/logs/stream", VMStreamLogs)
	mux.HandleFunc("GET /api/hosts/{name}/vms/{vm}/services/{service}/logs", VMGetLogs)
	mux.HandleFunc("POST /api/hosts/{name}/vms/{vm}/services/{service}/{action}", VMServiceControl)
}

// ── Response types ────────────────────────────────────────────────────────────

type vmStatus struct {
	Name            string            `json:"name"`
	VMID            int               `json:"vmid,omitempty"`
	IP              string            `json:"ip"`
	Online          bool              `json:"online"`
	Services        []string          `json:"services"`
	ServiceStatuses map[string]string `json:"service_statuses"`
	VPNHostname     *string           `json:"vpn_hostname"`
	VPNReachable    *bool             `json:"vpn_reachable"`
}

type hostStatus struct {
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	IP              string            `json:"ip"`
	MAC             string            `json:"mac"`
	Online          bool              `json:"online"`
	Local           bool              `json:"local"`
	Services        []string          `json:"services"`
	ServiceStatuses map[string]string `json:"service_statuses"`
	VPNHostname     *string           `json:"vpn_hostname"`
	VPNReachable    *bool             `json:"vpn_reachable"`
	VMs             *[]vmStatus       `json:"vms,omitempty"`
}

// ── Concurrent host/VM checkers ───────────────────────────────────────────────

func checkVM(cfg config.Config, host config.Host, vm config.VM) vmStatus {
	online := vm.Local || network.CheckSSHPort(vm.IP, sshTimeout)

	statuses := map[string]string{}
	if online && len(vm.Services) > 0 {
		var err error
		var s map[string]string
		if vm.Local {
			s, err = localops.ServiceStatuses(vm.Services)
		} else {
			keyPath := config.ResolveVMSSHKey(cfg, host, vm)
			s, err = sshops.ServiceStatuses(vm.IP, vm.SSHUser, keyPath, vm.Services)
		}
		if err != nil {
			log.Printf("service status check failed for VM %s: %v", vm.Name, err)
		} else {
			statuses = s
		}
	}

	svcs := vm.Services
	if svcs == nil {
		svcs = []string{}
	}

	var vpnHost *string
	var vpnReachable *bool
	if vm.NordVPNHost != "" {
		v := vm.NordVPNHost
		vpnHost = &v
		b := network.CheckSSHPort(vm.NordVPNHost, sshTimeout)
		vpnReachable = &b
	}

	return vmStatus{
		Name:            vm.Name,
		VMID:            vm.VMID,
		IP:              vm.IP,
		Online:          online,
		Services:        svcs,
		ServiceStatuses: statuses,
		VPNHostname:     vpnHost,
		VPNReachable:    vpnReachable,
	}
}

type hostCheckResult struct {
	online   bool
	statuses map[string]string
	vms      []vmStatus
}

func checkHost(cfg config.Config, host config.Host) hostCheckResult {
	online := host.Local || network.CheckSSHPort(host.IP, sshTimeout)

	statuses := map[string]string{}

	if config.IsProxmox(host) {
		vms := host.VMs
		results := make([]vmStatus, len(vms))
		var wg sync.WaitGroup
		for i, vm := range vms {
			wg.Add(1)
			go func(i int, vm config.VM) {
				defer wg.Done()
				results[i] = checkVM(cfg, host, vm)
			}(i, vm)
		}
		wg.Wait()
		return hostCheckResult{online: online, statuses: statuses, vms: results}
	}

	if online && len(host.Services) > 0 {
		var err error
		var s map[string]string
		if host.Local {
			s, err = localops.ServiceStatuses(host.Services)
		} else {
			keyPath := config.ResolveSSHKey(cfg, host)
			s, err = sshops.ServiceStatuses(host.IP, host.SSHUser, keyPath, host.Services)
		}
		if err != nil {
			log.Printf("service status check failed for %s: %v", host.Name, err)
		} else {
			statuses = s
		}
	}

	return hostCheckResult{online: online, statuses: statuses}
}

// ── GET /api/hosts ────────────────────────────────────────────────────────────

func GetHosts(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	hosts := cfg.Hosts
	hostResults := make([]hostCheckResult, len(hosts))
	vpnOnline := make([]bool, len(hosts))

	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func(i int, h config.Host) {
			defer wg.Done()
			hostResults[i] = checkHost(cfg, h)
			if h.NordVPNHost != "" {
				vpnOnline[i] = network.CheckSSHPort(h.NordVPNHost, sshTimeout)
			} else {
				vpnOnline[i] = true // sentinel; vpn_reachable will be null for these
			}
		}(i, h)
	}
	wg.Wait()

	result := make([]hostStatus, len(hosts))
	for i, h := range hosts {
		res := hostResults[i]

		svcs := h.Services
		if svcs == nil {
			svcs = []string{}
		}

		var vpnHost *string
		var vpnReachable *bool
		if h.NordVPNHost != "" {
			v := h.NordVPNHost
			vpnHost = &v
			b := vpnOnline[i]
			vpnReachable = &b
		}

		hostType := h.Type
		if hostType == "" {
			hostType = "standard"
		}

		hs := hostStatus{
			Name:            h.Name,
			Type:            hostType,
			IP:              h.IP,
			MAC:             h.MAC,
			Online:          res.online,
			Local:           h.Local,
			Services:        svcs,
			ServiceStatuses: res.statuses,
			VPNHostname:     vpnHost,
			VPNReachable:    vpnReachable,
		}

		if config.IsProxmox(h) {
			vms := res.vms
			if vms == nil {
				vms = []vmStatus{}
			}
			hs.VMs = &vms
		}

		result[i] = hs
	}

	lhttphandler.JSON(w, http.StatusOK, result)
}

// ── POST /api/hosts/{name}/wake ───────────────────────────────────────────────

func Wake(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, ok := config.GetHost(cfg, name)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Host '%s' not found", name))
		return
	}
	broadcast := host.Broadcast
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	if err := network.SendMagicPacket(host.MAC, broadcast, 9); err != nil {
		lhttphandler.ErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	lhttphandler.JSON(w, http.StatusOK, map[string]string{"status": "magic packet sent", "mac": host.MAC})
}

// ── POST /api/hosts/{name}/shutdown ──────────────────────────────────────────

func Shutdown(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, ok := config.GetHost(cfg, name)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Host '%s' not found", name))
		return
	}
	if host.Local {
		if err := localops.Shutdown(); err != nil {
			lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		if err := sshops.Shutdown(host.IP, host.SSHUser, keyPath); err != nil {
			lhttphandler.ErrorJSON(w, http.StatusInternalServerError, fmt.Sprintf("SSH error: %v", err))
			return
		}
	}
	lhttphandler.JSON(w, http.StatusOK, map[string]string{"status": "shutdown initiated", "host": name})
}

// ── GET /api/hosts/{name}/services/{service}/logs ─────────────────────────────

func GetLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	service := r.PathValue("service")
	lines := parseLines(r, 100)

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, ok := config.GetHost(cfg, name)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Host '%s' not found", name))
		return
	}
	if !config.ValidateService(host, service) {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for host '%s'", service, name))
		return
	}

	var out string
	if host.Local {
		out, err = localops.JournalLines(service, lines)
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		out, err = sshops.JournalLines(host.IP, host.SSHUser, keyPath, service, lines)
	}
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, fmt.Sprintf("SSH error: %v", err))
		return
	}

	logLines := splitLines(out)
	lhttphandler.JSON(w, http.StatusOK, map[string]any{"host": name, "service": service, "lines": logLines})
}

// ── GET /api/hosts/{name}/services/{service}/logs/stream ─────────────────────

func StreamLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	service := r.PathValue("service")

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, ok := config.GetHost(cfg, name)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Host '%s' not found", name))
		return
	}
	if !config.ValidateService(host, service) {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for host '%s'", service, name))
		return
	}

	setSSEHeaders(w)
	if host.Local {
		localops.StreamJournal(service, w, r)
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		sshops.StreamJournal(host.IP, host.SSHUser, keyPath, service, w, r)
	}
}

// ── POST /api/hosts/{name}/services/{service}/{action} ────────────────────────

func ServiceControl(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	service := r.PathValue("service")
	action := r.PathValue("action")

	if !validActions[action] {
		lhttphandler.ErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("Invalid action '%s'. Must be one of: restart, start, stop", action))
		return
	}

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, ok := config.GetHost(cfg, name)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Host '%s' not found", name))
		return
	}
	if !config.ValidateService(host, service) {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for host '%s'", service, name))
		return
	}

	if host.Local {
		err = localops.ServiceControl(service, action)
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		err = sshops.ServiceControl(host.IP, host.SSHUser, keyPath, service, action)
	}
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	lhttphandler.JSON(w, http.StatusOK, map[string]string{"status": action + " initiated", "host": name, "service": service})
}

// ── POST /api/hosts/{name}/vpn-repair ────────────────────────────────────────

func VPNRepair(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, ok := config.GetHost(cfg, name)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Host '%s' not found", name))
		return
	}
	if host.Local {
		lhttphandler.ErrorJSON(w, http.StatusBadRequest, "Cannot repair VPN on the local host")
		return
	}
	if cfg.NordVPNToken == "" {
		lhttphandler.ErrorJSON(w, http.StatusBadRequest, "nordvpn_token not set in hosts.yaml")
		return
	}

	setSSEHeaders(w)
	keyPath := config.ResolveSSHKey(cfg, host)
	sshops.StreamVPNRepair(host.IP, host.SSHUser, keyPath, cfg.NordVPNToken, w, r)
}

// ── VM routes ─────────────────────────────────────────────────────────────────

func getProxmoxAndVM(cfg config.Config, name, vmName string) (config.Host, config.VM, string) {
	host, ok := config.GetHost(cfg, name)
	if !ok {
		return config.Host{}, config.VM{}, fmt.Sprintf("Host '%s' not found", name)
	}
	if !config.IsProxmox(host) {
		return config.Host{}, config.VM{}, fmt.Sprintf("Host '%s' is not a Proxmox host", name)
	}
	vm, ok := config.GetVM(host, vmName)
	if !ok {
		return config.Host{}, config.VM{}, fmt.Sprintf("VM '%s' not found on host '%s'", vmName, name)
	}
	return host, vm, ""
}

// POST /api/hosts/{name}/vms/{vm}/shutdown
func VMShutdown(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmName := r.PathValue("vm")

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, vm, errMsg := getProxmoxAndVM(cfg, name, vmName)
	if errMsg != "" {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, errMsg)
		return
	}

	if vm.Local {
		err = localops.Shutdown()
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		err = sshops.Shutdown(vm.IP, vm.SSHUser, keyPath)
	}
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, fmt.Sprintf("SSH error: %v", err))
		return
	}

	lhttphandler.JSON(w, http.StatusOK, map[string]string{"status": "shutdown initiated", "host": name, "vm": vmName})
}

// POST /api/hosts/{name}/vms/{vm}/vpn-repair
func VMVPNRepair(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmName := r.PathValue("vm")

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, vm, errMsg := getProxmoxAndVM(cfg, name, vmName)
	if errMsg != "" {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, errMsg)
		return
	}
	if vm.Local {
		lhttphandler.ErrorJSON(w, http.StatusBadRequest, "Cannot repair VPN on the local host")
		return
	}
	if cfg.NordVPNToken == "" {
		lhttphandler.ErrorJSON(w, http.StatusBadRequest, "nordvpn_token not set in hosts.yaml")
		return
	}

	setSSEHeaders(w)
	keyPath := config.ResolveVMSSHKey(cfg, host, vm)
	sshops.StreamVPNRepair(vm.IP, vm.SSHUser, keyPath, cfg.NordVPNToken, w, r)
}

// GET /api/hosts/{name}/vms/{vm}/services/{service}/logs
func VMGetLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmName := r.PathValue("vm")
	service := r.PathValue("service")
	lines := parseLines(r, 100)

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, vm, errMsg := getProxmoxAndVM(cfg, name, vmName)
	if errMsg != "" {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, errMsg)
		return
	}
	if !config.ValidateVMService(vm, service) {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for VM '%s'", service, vmName))
		return
	}

	var out string
	if vm.Local {
		out, err = localops.JournalLines(service, lines)
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		out, err = sshops.JournalLines(vm.IP, vm.SSHUser, keyPath, service, lines)
	}
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, fmt.Sprintf("SSH error: %v", err))
		return
	}

	logLines := splitLines(out)
	lhttphandler.JSON(w, http.StatusOK, map[string]any{"host": name, "vm": vmName, "service": service, "lines": logLines})
}

// GET /api/hosts/{name}/vms/{vm}/services/{service}/logs/stream
func VMStreamLogs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmName := r.PathValue("vm")
	service := r.PathValue("service")

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, vm, errMsg := getProxmoxAndVM(cfg, name, vmName)
	if errMsg != "" {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, errMsg)
		return
	}
	if !config.ValidateVMService(vm, service) {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for VM '%s'", service, vmName))
		return
	}

	setSSEHeaders(w)
	if vm.Local {
		localops.StreamJournal(service, w, r)
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		sshops.StreamJournal(vm.IP, vm.SSHUser, keyPath, service, w, r)
	}
}

// POST /api/hosts/{name}/vms/{vm}/services/{service}/{action}
func VMServiceControl(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmName := r.PathValue("vm")
	service := r.PathValue("service")
	action := r.PathValue("action")

	if !validActions[action] {
		lhttphandler.ErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("Invalid action '%s'. Must be one of: restart, start, stop", action))
		return
	}

	cfg, err := config.Load()
	if err != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	host, vm, errMsg := getProxmoxAndVM(cfg, name, vmName)
	if errMsg != "" {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, errMsg)
		return
	}
	if !config.ValidateVMService(vm, service) {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for VM '%s'", service, vmName))
		return
	}

	var err2 error
	if vm.Local {
		err2 = localops.ServiceControl(service, action)
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		err2 = sshops.ServiceControl(vm.IP, vm.SSHUser, keyPath, service, action)
	}
	if err2 != nil {
		lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err2.Error())
		return
	}

	lhttphandler.JSON(w, http.StatusOK, map[string]string{"status": action + " initiated", "host": name, "vm": vmName, "service": service})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func parseLines(r *http.Request, defaultVal int) int {
	q := r.URL.Query().Get("lines")
	if q == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 {
		return defaultVal
	}
	if n > 5000 {
		return 5000
	}
	return n
}

func splitLines(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	return lines
}
