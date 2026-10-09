package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tkdlabs/lanctl/internal/notices"
)

func TestNoticesEndpointOK(t *testing.T) {
	useFakeOps(t)
	t.Setenv("LANCTL_LOCAL_HOST", "")
	t.Setenv("CONFIG_GIT_REMOTE", "")
	t.Setenv("SYNC_DISABLED_FILE", "")
	writeConfig(t, "hosts:\n  - name: box\n    ip: 192.0.2.10\n    mac: \"aa:bb:cc:dd:ee:01\"\n    local: true\n")

	rr := doRequest(t, http.MethodGet, "/api/notices")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var rep notices.Report
	if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.ConfigOK {
		t.Errorf("ConfigOK = false: %s", rep.ConfigError)
	}
	if rep.Status != "ok" {
		t.Errorf("status = %q, notices %+v", rep.Status, rep.Notices)
	}
}

func TestNoticesEndpointInvalidConfig(t *testing.T) {
	useFakeOps(t)
	t.Setenv("CONFIG_GIT_REMOTE", "")
	t.Setenv("SYNC_DISABLED_FILE", "")
	writeConfig(t, "hosts:\n  - name: box\n    nope: 1\n")

	rr := doRequest(t, http.MethodGet, "/api/notices")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var rep notices.Report
	if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.ConfigOK {
		t.Error("ConfigOK = true, want false")
	}
	if rep.Status != "error" {
		t.Errorf("status = %q, want error", rep.Status)
	}
}
