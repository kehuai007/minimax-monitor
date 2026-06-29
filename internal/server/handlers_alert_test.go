package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"minimax-monitor/internal/notify"
	"minimax-monitor/internal/storage"
)

func newTestServerWithDB(t *testing.T) (*Server, *storage.DB) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(nil, nil)
	s.DB = db
	s.AlertConfig = func() storage.AlertConfig {
		c, _ := db.GetAlertConfig(context.Background())
		return c
	}
	s.AlertTest = func(ctx context.Context) (int64, error) { return 0, nil }
	return s, db
}

func TestAlertGet_Default(t *testing.T) {
	s, _ := newTestServerWithDB(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/settings/alert", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got["enabled"] != false {
		t.Errorf("enabled = %v, want false", got["enabled"])
	}
	if got["url"] != "" {
		t.Errorf("url = %v, want empty", got["url"])
	}
	if got["threshold"].(float64) != 80 {
		t.Errorf("threshold = %v, want 80", got["threshold"])
	}
}

func TestAlertGet_URLMasked(t *testing.T) {
	s, db := newTestServerWithDB(t)
	ctx := context.Background()
	longURL := "https://open.feishu.cn/open-apis/bot/v2/hook/abcdef1234567890XYZ"
	_ = db.SetAlertConfig(ctx, storage.AlertConfig{Enabled: true, URL: longURL, Threshold: 70})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/settings/alert", nil)
	s.Engine.ServeHTTP(w, req)
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	masked := got["url"].(string)
	if strings.Contains(masked, "abcdef") {
		t.Errorf("url not masked: %q", masked)
	}
	if !strings.HasSuffix(masked, "XYZ") {
		t.Errorf("masked url should end with last 4 chars, got %q", masked)
	}
}

func TestAlertPut_Valid(t *testing.T) {
	s, db := newTestServerWithDB(t)
	gin.SetMode(gin.TestMode)
	body := `{"enabled":true,"url":"https://open.feishu.cn/open-apis/bot/v2/hook/abc","threshold":75}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/api/settings/alert",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	cfg, _ := db.GetAlertConfig(context.Background())
	if !cfg.Enabled || cfg.URL == "" || cfg.Threshold != 75 {
		t.Errorf("after PUT = %+v", cfg)
	}
}

func TestAlertPut_RejectBadURL(t *testing.T) {
	s, _ := newTestServerWithDB(t)
	gin.SetMode(gin.TestMode)
	body := `{"enabled":true,"url":"https://evil.com/hook","threshold":80}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/api/settings/alert",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAlertPut_EnabledRequiresURL(t *testing.T) {
	s, _ := newTestServerWithDB(t)
	gin.SetMode(gin.TestMode)
	body := `{"enabled":true,"url":"","threshold":80}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/api/settings/alert",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAlertPut_EmptyURLPreservesPrevious(t *testing.T) {
	s, db := newTestServerWithDB(t)
	ctx := context.Background()
	prevURL := "https://open.feishu.cn/open-apis/bot/v2/hook/abc"
	if err := db.SetAlertConfig(ctx, storage.AlertConfig{
		Enabled: false, URL: prevURL, Threshold: 80,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Simulates the user toggling enabled without re-pasting the masked URL.
	body := `{"enabled":true,"url":"","threshold":85}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/api/settings/alert",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	cfg, _ := db.GetAlertConfig(ctx)
	if cfg.URL != prevURL {
		t.Errorf("url = %q, want preserved %q", cfg.URL, prevURL)
	}
	if !cfg.Enabled {
		t.Errorf("enabled = false, want true")
	}
	if cfg.Threshold != 85 {
		t.Errorf("threshold = %d, want 85", cfg.Threshold)
	}
}

func TestAlertPut_DisableClearsState(t *testing.T) {
	s, db := newTestServerWithDB(t)
	ctx := context.Background()
	_ = db.SetAlertConfig(ctx, storage.AlertConfig{Enabled: true, URL: "https://open.feishu.cn/x", Threshold: 80})
	_ = db.SetAlertState(ctx, "general", storage.AlertState{NotifiedPcts: []int{80, 79}})
	gin.SetMode(gin.TestMode)
	body := `{"enabled":false,"url":"https://open.feishu.cn/x","threshold":80}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPut, "/api/settings/alert",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	st, _ := db.GetAlertState(ctx, "general")
	if len(st.NotifiedPcts) != 0 {
		t.Errorf("state not cleared on disable; got %v", st.NotifiedPcts)
	}
}

func TestAlertTest_ConfigMissing(t *testing.T) {
	s, _ := newTestServerWithDB(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/settings/alert/test", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when disabled", w.Code)
	}
}

func TestAlertTest_DelegatesToAlertTest(t *testing.T) {
	s, db := newTestServerWithDB(t)
	ctx := context.Background()
	_ = db.SetAlertConfig(ctx, storage.AlertConfig{Enabled: true, URL: "https://open.feishu.cn/x", Threshold: 80})
	s.AlertTest = func(ctx context.Context) (int64, error) {
		return 1782561600000, nil
	}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/settings/alert/test", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["sent_at"].(float64) != 1782561600000 {
		t.Errorf("sent_at = %v, want 1782561600000", got["sent_at"])
	}
}

func TestAlertTest_UpstreamError(t *testing.T) {
	s, db := newTestServerWithDB(t)
	ctx := context.Background()
	_ = db.SetAlertConfig(ctx, storage.AlertConfig{Enabled: true, URL: "https://open.feishu.cn/x", Threshold: 80})
	s.AlertTest = func(ctx context.Context) (int64, error) {
		return 0, errors.New("connection refused")
	}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/settings/alert/test", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

// Ensure notify package reference stays used in case future tests need it
var _ = notify.ErrConfigMissing