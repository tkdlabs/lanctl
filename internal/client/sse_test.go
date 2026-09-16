package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sseHandler(chunks ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range chunks {
			io.WriteString(w, c)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

func TestScanSSE(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single event", "data: a\n\n", []string{"a"}},
		{"heartbeat only", ": heartbeat\n\n", nil},
		{"heartbeat between events", "data: a\n\n: heartbeat\n\ndata: b\n\n", []string{"a", "b"}},
		{"multi-line event", "data: a\ndata: b\n\n", []string{"a\nb"}},
		{"ignores other fields", "event: x\nid: 1\ndata: a\n\n", []string{"a"}},
		{"no space after colon", "data:a\n\n", []string{"a"}},
		{"no trailing blank line", "data: a", []string{"a"}},
		{"empty input", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			err := scanSSE(strings.NewReader(tt.input), func(s string) error {
				got = append(got, s)
				return nil
			})
			if err != nil {
				t.Fatalf("scanSSE: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("events = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestScanSSE_PropagatesHandlerError(t *testing.T) {
	sentinel := errors.New("stop")
	err := scanSSE(strings.NewReader("data: a\n\n"), func(string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
}

func TestStreamLogs(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		sseHandler("data: one\n\n", ": heartbeat\n\n", "data: two\n\n")(w, r)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	err := New(srv.URL).StreamLogs(context.Background(), "desktop", "", "nginx", &buf)
	if err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/api/hosts/desktop/services/nginx/logs/stream" {
		t.Errorf("path = %q", gotPath)
	}
	if buf.String() != "one\ntwo\n" {
		t.Errorf("output = %q, want %q", buf.String(), "one\ntwo\n")
	}
}

func TestStreamLogs_VMPathPost(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		sseHandler("data: hi\n\n")(w, r)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := New(srv.URL).StreamLogs(context.Background(), "nas", "main", "docker", &buf); err != nil {
		t.Fatalf("StreamLogs: %v", err)
	}
	if gotPath != "/api/hosts/nas/vms/main/services/docker/logs/stream" {
		t.Errorf("path = %q", gotPath)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
}

func TestStreamLogs_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"detail":"Service 'x' not configured"}`)
	}))
	defer srv.Close()

	err := New(srv.URL).StreamLogs(context.Background(), "desktop", "", "x", io.Discard)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusNotFound || apiErr.Detail != "Service 'x' not configured" {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestStreamLogs_WriteError(t *testing.T) {
	srv := httptest.NewServer(sseHandler("data: a\n\n"))
	defer srv.Close()

	sentinel := errors.New("disk full")
	err := New(srv.URL).StreamLogs(context.Background(), "h", "", "s", errWriter{sentinel})
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want sentinel", err)
	}
}

func TestStreamingIgnoresClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		io.WriteString(w, "data: late\n\n")
	}))
	defer srv.Close()

	c := New(srv.URL, WithTimeout(time.Millisecond))
	var buf bytes.Buffer
	if err := c.StreamLogs(context.Background(), "h", "", "s", &buf); err != nil {
		t.Fatalf("stream should ignore Client timeout: %v", err)
	}
	if buf.String() != "late\n" {
		t.Errorf("output = %q", buf.String())
	}
}

func TestStreamLogs_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if err := New(srv.URL).StreamLogs(ctx, "h", "", "s", io.Discard); err == nil {
		t.Fatal("expected error after context cancellation")
	}
}

func TestVPNRepair(t *testing.T) {
	tests := []struct {
		name    string
		chunks  []string
		wantErr error
		wantOut string
	}{
		{
			name:    "success",
			chunks:  []string{"data: [1/3] checking\n\n", "data: [OK] connected\n\n"},
			wantOut: "[1/3] checking\n[OK] connected\n",
		},
		{
			name:    "fail marker",
			chunks:  []string{"data: [OK] start\n\n", "data: [FAIL] tunnel down\n\n"},
			wantErr: ErrVPNRepairFailed,
			wantOut: "[OK] start\n[FAIL] tunnel down\n",
		},
		{
			name:    "ssh error marker",
			chunks:  []string{"data: [SSH error] timeout\n\n"},
			wantErr: ErrVPNRepairFailed,
			wantOut: "[SSH error] timeout\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				sseHandler(tt.chunks...)(w, r)
			}))
			defer srv.Close()

			var buf bytes.Buffer
			err := New(srv.URL).VPNRepair(context.Background(), "desktop", "", &buf)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if gotPath != "/api/hosts/desktop/vpn-repair" {
				t.Errorf("path = %q", gotPath)
			}
			if buf.String() != tt.wantOut {
				t.Errorf("output = %q, want %q", buf.String(), tt.wantOut)
			}
		})
	}
}

func TestVPNRepair_VMPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		sseHandler("data: [OK] done\n\n")(w, r)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := New(srv.URL).VPNRepair(context.Background(), "nas", "main", &buf); err != nil {
		t.Fatalf("VPNRepair: %v", err)
	}
	if gotPath != "/api/hosts/nas/vms/main/vpn-repair" {
		t.Errorf("path = %q", gotPath)
	}
}

type errWriter struct{ err error }

func (e errWriter) Write([]byte) (int, error) { return 0, e.err }
