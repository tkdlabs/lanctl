package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
