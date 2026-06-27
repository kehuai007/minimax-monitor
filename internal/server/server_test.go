package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"minimax-monitor/internal/model"
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

type fakeStore struct {
	val string
	err error
}

func (f *fakeStore) Get() (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.val, nil
}
func (f *fakeStore) Set(v string) error   { f.val = v; return nil }
func (f *fakeStore) Delete() error        { f.val = ""; return nil }

type fakeFetcher struct {
	called atomic.Int32
	resp   *model.APIResponse
	err    error
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) (*model.APIResponse, error) {
	f.called.Add(1)
	return f.resp, f.err
}

// "context" already imported above; ensure it's there.

func TestSettingsKey_PostSuccess(t *testing.T) {
	store := &fakeStore{}
	fetch := &fakeFetcher{resp: &model.APIResponse{BaseResp: model.BaseResp{StatusCode: 0}}}
	s := New(nil, store)
	s.Validator = func(ctx context.Context, key string) error {
		_, err := fetch.Fetch(ctx, key)
		return err
	}
	body, _ := json.Marshal(map[string]string{"api_key": "sk-x"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/settings/key", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if store.val != "sk-x" {
		t.Errorf("key not stored: %q", store.val)
	}
	if fetch.called.Load() != 1 {
		t.Errorf("validator not called")
	}
}

func TestSettingsKey_PostInvalidKey(t *testing.T) {
	store := &fakeStore{}
	fetch := &fakeFetcher{err: errBad}
	s := New(nil, store)
	s.Validator = func(ctx context.Context, key string) error {
		_, err := fetch.Fetch(ctx, key)
		return err
	}
	body, _ := json.Marshal(map[string]string{"api_key": "bad"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/settings/key", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if store.val != "" {
		t.Errorf("key should NOT be stored on validation failure")
	}
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

var errBad = stringErr("bad key")