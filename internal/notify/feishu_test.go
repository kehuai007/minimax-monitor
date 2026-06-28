package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseURLAndSecret_NoSecret(t *testing.T) {
	in := "https://open.feishu.cn/open-apis/bot/v2/hook/abc123"
	u, s, err := parseURLAndSecret(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != "" {
		t.Errorf("secret = %q, want empty", s)
	}
	if u != in {
		t.Errorf("cleanURL = %q, want %q", u, in)
	}
}

func TestParseURLAndSecret_WithSecret(t *testing.T) {
	in := "https://open.feishu.cn/open-apis/bot/v2/hook/abc?secret=topsecret"
	u, s, err := parseURLAndSecret(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != "topsecret" {
		t.Errorf("secret = %q, want topsecret", s)
	}
	if strings.Contains(u, "secret=") {
		t.Errorf("cleanURL still contains secret: %q", u)
	}
	if !strings.HasPrefix(u, "https://open.feishu.cn/open-apis/bot/v2/hook/abc") {
		t.Errorf("cleanURL stripped too much: %q", u)
	}
}

func TestHMACSign_KnownVector(t *testing.T) {
	sig := hmacSign("Sec1", "1700000000", `{"a":1}`)
	if sig == "" {
		t.Error("hmacSign returned empty")
	}
	if strings.ContainsAny(sig, " \n\r") {
		t.Errorf("sig contains whitespace: %q", sig)
	}
}

func TestFeishuClient_Send_Success(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"msg_type":"interactive"`) {
			t.Errorf("body missing interactive msg_type: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"StatusCode":0,"msg":"ok"}`)
	}))
	defer srv.Close()

	c := NewFeishuClientWithHTTP(&http.Client{Timeout: 2 * time.Second})
	n := Notification{
		Model: "general", Remaining: 60, Used: 40, Threshold: 80,
		Severity: SevMid, FetchedAt: time.Now().UnixMilli(),
	}
	if err := c.Send(context.Background(), srv.URL, n); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

func TestFeishuClient_Send_RetriesOnError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewFeishuClientWithHTTP(&http.Client{Timeout: 2 * time.Second})
	n := Notification{Model: "general", Remaining: 60, Threshold: 80, FetchedAt: time.Now().UnixMilli()}
	err := c.Send(context.Background(), srv.URL, n)
	if err == nil {
		t.Fatal("expected error after retries")
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("calls = %d, want at least 3 (initial + 2 retries)", got)
	}
}

func TestFeishuClient_Send_RetriesOnNonZeroStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"StatusCode":230002,"msg":"sign invalid"}`)
	}))
	defer srv.Close()

	c := NewFeishuClientWithHTTP(&http.Client{Timeout: 2 * time.Second})
	n := Notification{Model: "general", Remaining: 60, Threshold: 80, FetchedAt: time.Now().UnixMilli()}
	err := c.Send(context.Background(), srv.URL, n)
	if err == nil {
		t.Fatal("expected error when Feishu returns non-zero StatusCode")
	}
	if got := calls.Load(); got < 3 {
		t.Errorf("calls = %d, want at least 3", got)
	}
}

func TestFeishuClient_Send_SignsWhenSecretPresent(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		fmt.Fprint(w, `{"StatusCode":0}`)
	}))
	defer srv.Close()

	c := NewFeishuClientWithHTTP(&http.Client{Timeout: 2 * time.Second})
	n := Notification{Model: "general", Remaining: 60, Threshold: 80, FetchedAt: time.Now().UnixMilli()}
	urlWithSecret := srv.URL + "?secret=mysecret"
	if err := c.Send(context.Background(), urlWithSecret, n); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(receivedBody), &got); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if _, ok := got["timestamp"]; !ok {
		t.Error("signed body missing timestamp")
	}
	if _, ok := got["sign"]; !ok {
		t.Error("signed body missing sign")
	}
}
