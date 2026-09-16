package httphandler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestJSON_WritesBodyAndContentType(t *testing.T) {
	rr := httptest.NewRecorder()
	JSON(rr, http.StatusCreated, map[string]string{"status": "ok"})

	if rr.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("got content-type %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["status"] != "ok" {
		t.Errorf("unexpected body %v", got)
	}
}

func TestJSON_MarshalError(t *testing.T) {
	rr := httptest.NewRecorder()
	// A channel cannot be marshalled to JSON.
	JSON(rr, http.StatusOK, make(chan int))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("got status %d, want 500", rr.Code)
	}
}

func TestErrorJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	ErrorJSON(rr, http.StatusNotFound, "missing")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", rr.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["detail"] != "missing" {
		t.Errorf("got detail %q, want 'missing'", got["detail"])
	}
}
