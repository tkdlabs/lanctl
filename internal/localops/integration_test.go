//go:build integration

package localops

import "testing"

// These tests exercise real systemctl/journalctl on the host running the suite.
// Run them explicitly with:
//
//	go test -tags=integration ./internal/localops/

func TestServiceStatuses_EmptySlice(t *testing.T) {
	statuses, err := ServiceStatuses([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("expected empty map, got %v", statuses)
	}
}

func TestServiceStatuses_NonexistentService(t *testing.T) {
	statuses, err := ServiceStatuses([]string{"definitely-nonexistent-lanctl-test-svc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	status := statuses["definitely-nonexistent-lanctl-test-svc"]
	if status != "inactive" && status != "unknown" {
		t.Errorf("unexpected status %q for nonexistent service", status)
	}
}

func TestJournalLines_NonexistentService(t *testing.T) {
	_, _ = JournalLines("definitely-nonexistent-lanctl-test-svc", 10)
}

func TestServiceControl_NonexistentService(t *testing.T) {
	_ = ServiceControl("definitely-nonexistent-lanctl-test-svc", "stop")
}
