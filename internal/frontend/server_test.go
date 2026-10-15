package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttach_ServesIndexAndLog(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.html")
	logPath := filepath.Join(dir, "log.html")
	if err := os.WriteFile(indexPath, []byte("<h1>index</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("<h1>log</h1>"), 0600); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Attach(mux, indexPath, logPath)

	tests := []struct {
		path string
		want string
	}{
		{"/", "<h1>index</h1>"},
		{"/log", "<h1>log</h1>"},
	}
	for _, tt := range tests {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", tt.path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("%s: got status %d", tt.path, rr.Code)
		}
		if rr.Body.String() != tt.want {
			t.Errorf("%s: got body %q, want %q", tt.path, rr.Body.String(), tt.want)
		}
	}
}

func TestAttach_MissingFileReturns404(t *testing.T) {
	mux := http.NewServeMux()
	Attach(mux, filepath.Join(t.TempDir(), "nope.html"), filepath.Join(t.TempDir(), "nope2.html"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// TestIndexHTML_SurfacesHostsErrors guards the dashboard against showing a
// bare "Error: HTTP 500" with no explanation when /api/hosts fails: the page
// must read the server's {"detail": ...} message and must render the notices
// banner even when the hosts request fails. The frontend is static HTML/JS, so
// this asserts on the shipped source rather than executing it.
func TestIndexHTML_SurfacesHostsErrors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)

	// A shared reader that turns a non-2xx response into the server's message
	// instead of "HTTP <status>".
	if !strings.Contains(src, "fetchJSON") {
		t.Error("index.html: no shared fetch helper; the JSON error detail is likely discarded")
	}
	// Hosts and notices are fetched independently, so a failing /api/hosts
	// does not skip renderNotices (the banner that explains the failure).
	if !strings.Contains(src, "Promise.allSettled") {
		t.Error("index.html: hosts/notices fetches are coupled; a hosts error hides the notices banner")
	}
}
