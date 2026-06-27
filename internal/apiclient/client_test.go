package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"minimax-monitor/internal/model"
)

func newOKServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.APIResponse{
			ModelRemains: []model.ModelRemains{{ModelName: "general"}},
			BaseResp:     model.BaseResp{StatusCode: 0, StatusMsg: "success"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestFetch_Success(t *testing.T) {
	srv, calls := newOKServer(t)
	c := New(srv.URL)
	resp, err := c.Fetch(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(resp.ModelRemains) != 1 || resp.ModelRemains[0].ModelName != "general" {
		t.Errorf("unexpected body: %+v", resp)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("calls = %d, want 1", *calls)
	}
}

func TestFetch_NonZeroStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.APIResponse{
			BaseResp: model.BaseResp{StatusCode: 1001, StatusMsg: "bad key"},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), "k")
	if err == nil || err.Error() == "" {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestFetch_RetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(model.APIResponse{
			BaseResp: model.BaseResp{StatusCode: 0, StatusMsg: "success"},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	// speed up backoff for test
	c.backoff = func(attempt int) time.Duration { return time.Millisecond }
	_, err := c.Fetch(context.Background(), "k")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}
