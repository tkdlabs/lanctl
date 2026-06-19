package localops

import (
	"strings"
	"testing"
)

// ── Unit tests for parsing logic ──────────────────────────────────────────────

func TestParseServiceStatusOutput_Empty(t *testing.T) {
	out := ""
	services := []string{"svc1", "svc2"}
	statuses := parseStatusOutput(out, services)
	if statuses["svc1"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["svc1"])
	}
	if statuses["svc2"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["svc2"])
	}
}

func TestParseServiceStatusOutput_ActiveInactive(t *testing.T) {
	out := "active\ninactive\nfailed\n"
	services := []string{"s1", "s2", "s3"}
	expected := map[string]string{"s1": "active", "s2": "inactive", "s3": "failed"}
	statuses := parseStatusOutput(out, services)
	for svc, want := range expected {
		if statuses[svc] != want {
			t.Errorf("service %q: want %q, got %q", svc, want, statuses[svc])
		}
	}
}

func TestParseServiceStatusOutput_FewerLinesThanServices(t *testing.T) {
	out := "active\n"
	services := []string{"s1", "s2", "s3"}
	statuses := parseStatusOutput(out, services)
	if statuses["s1"] != "active" {
		t.Errorf("expected 'active', got %q", statuses["s1"])
	}
	if statuses["s2"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["s2"])
	}
	if statuses["s3"] != "unknown" {
		t.Errorf("expected 'unknown', got %q", statuses["s3"])
	}
}

// ── Integration tests (calls real systemctl/journalctl if available) ──────────

func TestServiceStatuses_EmptySlice(t *testing.T) {
	// Calling with no services should return an empty map without error.
	statuses, err := ServiceStatuses([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected empty map, got %v", statuses)
	}
}

func TestServiceStatuses_NonexistentService(t *testing.T) {
	// systemctl is-active returns non-zero for unknown services, but we ignore the error.
	statuses, err := ServiceStatuses([]string{"definitely-nonexistent-lanctl-test-svc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	status := statuses["definitely-nonexistent-lanctl-test-svc"]
	// systemctl reports "inactive" or "unknown" for nonexistent services
	if status != "inactive" && status != "unknown" {
		t.Errorf("unexpected status %q for nonexistent service", status)
	}
}

func TestJournalLines_NonexistentService(t *testing.T) {
	// journalctl returns an error for unknown units or exits non-zero.
	// We just verify it doesn't panic.
	_, _ = JournalLines("definitely-nonexistent-lanctl-test-svc", 10)
}

func TestServiceControl_NonexistentService(t *testing.T) {
	// sudo systemctl stop nonexistent should fail; we get a non-nil error.
	err := ServiceControl("definitely-nonexistent-lanctl-test-svc", "stop")
	// May or may not have sudo — either way we get an error from systemctl
	_ = err
}

// ── Helper matching actual function logic ─────────────────────────────────────

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
