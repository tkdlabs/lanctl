package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkdlabs/lanctl/internal/config"
	lhttphandler "github.com/tkdlabs/lanctl/internal/httphandler"
	"github.com/tkdlabs/lanctl/internal/version"
)

const sshTimeout = 1500 * time.Millisecond

var validActions = map[string]bool{"start": true, "stop": true, "restart": true}

// RegisterRoutes registers all /api/* routes on mux.
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/version", Version)
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

// ── GET /api/version ──────────────────────────────────────────────────────────

// Version returns the running build version.
func Version(w http.ResponseWriter, r *http.Request) {
	lhttphandler.JSON(w, http.StatusOK, map[string]string{"version": version.Version})
}

// ── Response types ────────────────────────────────────────────────────────────

type vmStatus struct {
	Name            string            `json:"name"`
	VMID            int               `json:"vmid,omitempty"`
	IP              string            `json:"ip"`
	Online          bool              `json:"online"`
	Services        []string          `json:"services"`
	UserServices    []string          `json:"user_services"`
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
	UserServices    []string          `json:"user_services"`
	ServiceStatuses map[string]string `json:"service_statuses"`
	VPNHostname     *string           `json:"vpn_hostname"`
	VPNReachable    *bool             `json:"vpn_reachable"`
	VMs             *[]vmStatus       `json:"vms,omitempty"`
}

// ── Concurrent host/VM checkers ───────────────────────────────────────────────

// serviceStatuses queries the systemd status of system and user services and
// merges the results. A failed scope is logged under label and skipped, so one
// unreachable user manager does not hide system service statuses.
func serviceStatuses(label string, local bool, ip, sshUser, keyPath string, services, userServices []string) map[string]string {
	statuses := map[string]string{}
	for _, group := range []struct {
		names     []string
		userScope bool
	}{
		{services, false},
		{userServices, true},
	} {
		if len(group.names) == 0 {
			continue
		}
		var s map[string]string
		var err error
		if local {
			s, err = ops.ServiceStatuses(group.names, group.userScope)
		} else {
			s, err = ops.SSHServiceStatuses(ip, sshUser, keyPath, group.names, group.userScope)
		}
		if err != nil {
			log.Printf("service status check failed for %s: %v", label, err)
			continue
		}
		for name, status := range s {
			statuses[name] = status
		}
	}
	return statuses
}

func checkVM(cfg config.Config, host config.Host, vm config.VM) vmStatus {
	online := vm.Local || ops.CheckSSHPort(vm.IP, sshTimeout)

	statuses := map[string]string{}
	if online && (len(vm.Services) > 0 || len(vm.UserServices) > 0) {
		keyPath := ""
		if !vm.Local {
			keyPath = config.ResolveVMSSHKey(cfg, host, vm)
		}
		statuses = serviceStatuses(vm.Name, vm.Local, vm.IP, vm.SSHUser, keyPath, vm.Services, vm.UserServices)
	}

	svcs := vm.Services
	if svcs == nil {
		svcs = []string{}
	}
	userSvcs := vm.UserServices
	if userSvcs == nil {
		userSvcs = []string{}
	}

	var vpnHost *string
	var vpnReachable *bool
	if vm.NordVPNHost != "" {
		v := vm.NordVPNHost
		vpnHost = &v
		b := ops.CheckSSHPort(vm.NordVPNHost, sshTimeout)
		vpnReachable = &b
	}

	return vmStatus{
		Name:            vm.Name,
		VMID:            vm.VMID,
		IP:              vm.IP,
		Online:          online,
		Services:        svcs,
		UserServices:    userSvcs,
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
	online := host.Local || ops.CheckSSHPort(host.IP, sshTimeout)

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

	if online && (len(host.Services) > 0 || len(host.UserServices) > 0) {
		keyPath := ""
		if !host.Local {
			keyPath = config.ResolveSSHKey(cfg, host)
		}
		statuses = serviceStatuses(host.Name, host.Local, host.IP, host.SSHUser, keyPath, host.Services, host.UserServices)
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
				vpnOnline[i] = ops.CheckSSHPort(h.NordVPNHost, sshTimeout)
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
		userSvcs := h.UserServices
		if userSvcs == nil {
			userSvcs = []string{}
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
			UserServices:    userSvcs,
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
	if err := ops.SendMagicPacket(host.MAC, broadcast, 9); err != nil {
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
		if err := ops.Shutdown(); err != nil {
			lhttphandler.ErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		if err := ops.SSHShutdown(host.IP, host.SSHUser, keyPath); err != nil {
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
	scope, ok := config.FindService(host, service)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for host '%s'", service, name))
		return
	}
	userScope := scope == config.ScopeUser

	var out string
	if host.Local {
		out, err = ops.JournalLines(service, lines, userScope)
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		out, err = ops.SSHJournalLines(host.IP, host.SSHUser, keyPath, service, lines, userScope)
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
	scope, ok := config.FindService(host, service)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for host '%s'", service, name))
		return
	}

	setSSEHeaders(w)
	if host.Local {
		ops.StreamJournal(service, scope == config.ScopeUser, w, r)
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		ops.SSHStreamJournal(host.IP, host.SSHUser, keyPath, service, scope == config.ScopeUser, w, r)
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
	scope, ok := config.FindService(host, service)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for host '%s'", service, name))
		return
	}

	if host.Local {
		err = ops.ServiceControl(service, action, scope == config.ScopeUser)
	} else {
		keyPath := config.ResolveSSHKey(cfg, host)
		err = ops.SSHServiceControl(host.IP, host.SSHUser, keyPath, service, action, scope == config.ScopeUser)
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
	ops.StreamVPNRepair(host.IP, host.SSHUser, keyPath, cfg.NordVPNToken, w, r)
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
		err = ops.Shutdown()
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		err = ops.SSHShutdown(vm.IP, vm.SSHUser, keyPath)
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
	ops.StreamVPNRepair(vm.IP, vm.SSHUser, keyPath, cfg.NordVPNToken, w, r)
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
	scope, ok := config.FindVMService(vm, service)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for VM '%s'", service, vmName))
		return
	}

	var out string
	if vm.Local {
		out, err = ops.JournalLines(service, lines, scope == config.ScopeUser)
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		out, err = ops.SSHJournalLines(vm.IP, vm.SSHUser, keyPath, service, lines, scope == config.ScopeUser)
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
	scope, ok := config.FindVMService(vm, service)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for VM '%s'", service, vmName))
		return
	}

	setSSEHeaders(w)
	if vm.Local {
		ops.StreamJournal(service, scope == config.ScopeUser, w, r)
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		ops.SSHStreamJournal(vm.IP, vm.SSHUser, keyPath, service, scope == config.ScopeUser, w, r)
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
	scope, ok := config.FindVMService(vm, service)
	if !ok {
		lhttphandler.ErrorJSON(w, http.StatusNotFound, fmt.Sprintf("Service '%s' not configured for VM '%s'", service, vmName))
		return
	}

	var err2 error
	if vm.Local {
		err2 = ops.ServiceControl(service, action, scope == config.ScopeUser)
	} else {
		keyPath := config.ResolveVMSSHKey(cfg, host, vm)
		err2 = ops.SSHServiceControl(vm.IP, vm.SSHUser, keyPath, service, action, scope == config.ScopeUser)
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
