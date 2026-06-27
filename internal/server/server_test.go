package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthz(t *testing.T) {
	s := New(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	w := httptest.NewRecorder()
	s.Engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); body != "ok" {
		t.Fatalf("body = %q, want %q", body, "ok")
	}
}

func TestStatus_NoKeyring(t *testing.T) {
	s := New(nil, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/status", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !contains(w.Body.String(), `"keyring_configured":false`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestHistory_RejectsBadModel(t *testing.T) {
	s := New(nil, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/history?model=&range=24h", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}