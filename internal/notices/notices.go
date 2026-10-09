// Package notices builds a self-diagnostic report for the running box from
// local state only: the resolved config, the config-sync state files, and the
// environment. It never contacts a host, service, or network, so the /api/notices
// handler is safe to poll and cheap to test.
package notices

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tkdlabs/lanctl/internal/config"
)

// Level classifies a notice by severity. The overall report status is derived
// from the highest level present.
type Level string

const (
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

const (
	statusOK       = "ok"
	statusDegraded = "degraded"
	statusError    = "error"
)

// defaultStaleAfter is how long a successful config sync may go without
// running before it is reported as stale. Override with LANCTL_STALE_AFTER
// (seconds).
const defaultStaleAfter = 3600 * time.Second

// Notice is a single diagnostic message.
type Notice struct {
	Level   Level  `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SyncInfo mirrors $DEPLOY_DIR/.config-sync/state.json plus derived age.
type SyncInfo struct {
	Status     string `json:"status"`
	Rev        string `json:"rev,omitempty"`
	At         string `json:"at,omitempty"`
	AgeSeconds *int64 `json:"age_seconds,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Report is the payload returned by GET /api/notices.
type Report struct {
	Status           string    `json:"status"`
	Version          string    `json:"version"`
	ConfigPath       string    `json:"config_path"`
	ConfigOK         bool      `json:"config_ok"`
	ConfigError      string    `json:"config_error,omitempty"`
	HostCount        int       `json:"host_count"`
	LocalHost        string    `json:"local_host,omitempty"`
	ConfigAgeSeconds *int64    `json:"config_age_seconds,omitempty"`
	Sync             *SyncInfo `json:"sync,omitempty"`
	Notices          []Notice  `json:"notices"`
}

// Build assembles the report. now is injected so callers (and tests) control
// the clock used for freshness checks.
func Build(version string, now time.Time) Report {
	report := Report{
		Version:    version,
		ConfigPath: config.Path(),
		LocalHost:  os.Getenv("LANCTL_LOCAL_HOST"),
		Notices:    []Notice{},
	}

	cfg, err := config.Load()
	if err != nil {
		report.ConfigError = err.Error()
		report.add(LevelError, "config_invalid", "Configuration is not usable: "+err.Error())
	} else {
		report.ConfigOK = true
		report.HostCount = len(cfg.Hosts)
		checkConfig(cfg, &report)
	}

	if fi, err := os.Stat(report.ConfigPath); err == nil {
		age := int64(now.Sub(fi.ModTime()).Seconds())
		report.ConfigAgeSeconds = &age
	}

	checkSync(deployDir(), now, &report)

	report.Status = overall(report.Notices)
	return report
}

// checkConfig looks for configs that load cleanly but will misbehave at
// runtime: a required token that is unset, or SSH keys that do not exist.
func checkConfig(cfg config.Config, report *Report) {
	if cfg.NordVPNToken == "" {
		var uses []string
		for _, h := range cfg.Hosts {
			if h.NordVPNHost != "" {
				uses = append(uses, h.Name)
			}
			for _, vm := range h.VMs {
				if vm.NordVPNHost != "" {
					uses = append(uses, h.Name+"/"+vm.Name)
				}
			}
		}
		if len(uses) > 0 {
			sort.Strings(uses)
			report.add(LevelWarning, "missing_vpn_token",
				fmt.Sprintf("nordvpn_token is unset but VPN repair is configured for: %s", strings.Join(uses, ", ")))
		}
	}

	missing := map[string][]string{}
	addMissing := func(key, target string) {
		if key == "" || fileExists(key) {
			return
		}
		missing[key] = append(missing[key], target)
	}
	for _, h := range cfg.Hosts {
		if !h.Local {
			addMissing(config.ResolveSSHKey(cfg, h), h.Name)
		}
		for _, vm := range h.VMs {
			if !vm.Local {
				addMissing(config.ResolveVMSSHKey(cfg, h, vm), h.Name+"/"+vm.Name)
			}
		}
	}
	keys := make([]string, 0, len(missing))
	for key := range missing {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		targets := missing[key]
		sort.Strings(targets)
		report.add(LevelWarning, "missing_ssh_key",
			fmt.Sprintf("SSH key %s not found for: %s", key, strings.Join(targets, ", ")))
	}
}

// checkSync reads the config-sync state written by deploy/lanctl-sync.sh and
// turns it into notices.
func checkSync(deployDir string, now time.Time, report *Report) {
	workDir := filepath.Join(deployDir, ".config-sync")
	stateFile := filepath.Join(workDir, "state.json")
	failedFile := filepath.Join(workDir, "FAILED")

	disabledFile := os.Getenv("SYNC_DISABLED_FILE")
	if disabledFile == "" {
		disabledFile = filepath.Join(deployDir, "SYNC_DISABLED")
	}
	syncConfigured := os.Getenv("CONFIG_GIT_REMOTE") != ""

	if fileExists(disabledFile) {
		report.add(LevelWarning, "sync_held",
			"Config sync is held ("+disabledFile+" exists); the timer will not apply changes")
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if syncConfigured {
			report.add(LevelWarning, "never_synced",
				"Config sync is configured but has never run ("+stateFile+" is missing)")
		}
	} else {
		checkSyncState(data, failedFile, now, report)
	}

	if !hasCode(report, "sync_failed") && fileExists(failedFile) {
		if detail, err := os.ReadFile(failedFile); err == nil {
			report.add(LevelError, "sync_failed", "Last config sync failure: "+strings.TrimSpace(string(detail)))
		}
	}
}

func checkSyncState(data []byte, failedFile string, now time.Time, report *Report) {
	var st struct {
		Status string `json:"status"`
		Rev    string `json:"rev"`
		At     string `json:"at"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		report.add(LevelWarning, "sync_state_unreadable", "Cannot parse sync state: "+err.Error())
		return
	}

	info := &SyncInfo{Status: st.Status, Rev: st.Rev, At: st.At}
	if t, err := time.Parse(time.RFC3339, st.At); err == nil {
		age := int64(now.Sub(t).Seconds())
		info.AgeSeconds = &age
	}
	if detail, err := os.ReadFile(failedFile); err == nil {
		info.Detail = strings.TrimSpace(string(detail))
	}
	report.Sync = info

	switch st.Status {
	case "ok":
		if info.AgeSeconds != nil && time.Duration(*info.AgeSeconds)*time.Second > staleAfter() {
			report.add(LevelWarning, "sync_stale",
				fmt.Sprintf("Latest successful config sync was %s ago (limit %s)", humanAge(*info.AgeSeconds), staleAfter()))
		}
	case "failed":
		msg := "The last config sync failed"
		if info.Detail != "" {
			msg += ": " + info.Detail
		}
		report.add(LevelError, "sync_failed", msg)
	case "pending":
		report.add(LevelInfo, "sync_pending", "A validated config is waiting out the apply delay")
	case "held":
		report.add(LevelWarning, "sync_held_state", "The last config sync was held during the apply delay; the config was not applied")
	case "aborted":
		report.add(LevelWarning, "sync_aborted", "The last config sync was aborted during the apply delay; the config was not applied")
	default:
		report.add(LevelWarning, "sync_unknown", fmt.Sprintf("Unknown config sync state %q", st.Status))
	}
}

// staleAfter returns the configured freshness limit.
func staleAfter() time.Duration {
	if v := os.Getenv("LANCTL_STALE_AFTER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultStaleAfter
}

// deployDir resolves the directory holding hosts.yaml and the sync state.
func deployDir() string {
	if d := os.Getenv("DEPLOY_DIR"); d != "" {
		return config.ExpandTilde(d)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func (r *Report) add(level Level, code, message string) {
	r.Notices = append(r.Notices, Notice{Level: level, Code: code, Message: message})
}

func overall(notices []Notice) string {
	status := statusOK
	for _, n := range notices {
		switch n.Level {
		case LevelError:
			return statusError
		case LevelWarning:
			status = statusDegraded
		}
	}
	return status
}

func hasCode(r *Report, code string) bool {
	for _, n := range r.Notices {
		if n.Code == code {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func humanAge(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
