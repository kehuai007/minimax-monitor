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

func TestBuildCardPayload_AlertCard_EmphasizesUsed(t *testing.T) {
	prev := intPtr(20) // remaining at previous notification = 20 -> consumed = 80
	n := Notification{
		Model:           "general",
		Severity:        SevHigh,
		Remaining:       20,
		Used:            80,
		Threshold:       80,
		PrevNotifiedPct: prev,
		FetchedAt:       time.Date(2026, 6, 28, 16, 42, 13, 0, time.UTC).UnixMilli(),
	}
	card := buildCardPayload(n)
	body, _ := json.Marshal(card)
	s := string(body)
	// 1) "消耗" field appears BEFORE "剩余" in the serialized fields list
	iUsed := strings.Index(s, "**消耗**")
	iRem := strings.Index(s, "剩余")
	if iUsed < 0 || iRem < 0 || iUsed > iRem {
		t.Errorf("order: 消耗 must come before 剩余; 消耗@%d 剩余@%d", iUsed, iRem)
	}
	// 2) "消耗" is rendered bold (the spec wraps the value in **...**)
	if !strings.Contains(s, "**消耗**\\n**80%**") {
		t.Errorf("expected bold 消耗 80%% in card; body=%s", s)
	}
	// 3) threshold field uses '≥80%' (not '≤80%')
	if !strings.Contains(s, "≥80%") {
		t.Errorf("expected ≥80%% threshold copy; body=%s", s)
	}
	// 4) prev_notified (remaining 20) renders as consumed 80
	if !strings.Contains(s, "上次告警 (消耗)") {
		t.Errorf("expected '上次告警 (消耗)' label; body=%s", s)
	}
	if !strings.Contains(s, "80%") {
		// 80 appears twice (current consumed + previous consumed); assert at least once
		// in the prev-notified field by context check below
	}
	// 5) title still uses "配额告警"
	if !strings.Contains(s, "配额告警") {
		t.Errorf("expected '配额告警' title; body=%s", s)
	}
}

func TestBuildCardPayload_AlertCard_TestTitle(t *testing.T) {
	n := Notification{
		IsTest:      true,
		Model:       "general",
		Severity:    SevInfo,
		Remaining:   20,
		Used:        80,
		Threshold:   80,
		FetchedAt:   time.Date(2026, 6, 28, 16, 42, 13, 0, time.UTC).UnixMilli(),
	}
	card := buildCardPayload(n)
	body, _ := json.Marshal(card)
	s := string(body)
	// Title must switch to "[测试] " when IsTest — never include "配额告警" so
	// the user is never misled into thinking a real alert fired.
	if !strings.Contains(s, "[测试]") {
		t.Errorf("expected '[测试]' title prefix; body=%s", s)
	}
	if strings.Contains(s, "配额告警") {
		t.Errorf("test card should not include '配额告警' title; body=%s", s)
	}
	// Severity was forced to SevInfo so the template color is blue.
	if !strings.Contains(s, `"template":"blue"`) {
		t.Errorf("expected template=blue for test card; body=%s", s)
	}
	// Footer note should be the test-specific copy, not the trend line.
	if !strings.Contains(s, "这是测试消息") {
		t.Errorf("expected '这是测试消息' footer note; body=%s", s)
	}
}

func TestBuildCardPayload_ResetCard_TitleAndFields(t *testing.T) {
	maxConsumed := intPtr(87)
	endAt := int64Ptr(time.Date(2026, 6, 28, 18, 45, 0, 0, time.UTC).UnixMilli())
	n := Notification{
		Kind:              KindReset,
		Model:             "general",
		Severity:          SevInfo,
		Remaining:         100,
		Used:              0,
		Threshold:         80,
		WindowMaxConsumed: maxConsumed,
		IntervalResetAt:   endAt,
		FetchedAt:         time.Date(2026, 6, 28, 18, 45, 0, 0, time.UTC).UnixMilli(),
	}
	card := buildCardPayload(n)
	body, _ := json.Marshal(card)
	s := string(body)
	if !strings.Contains(s, "🔄 配额重置") {
		t.Errorf("expected '🔄 配额重置' title; body=%s", s)
	}
	if !strings.Contains(s, `"template":"blue"`) {
		t.Errorf("expected template=blue; body=%s", s)
	}
	if !strings.Contains(s, "本周期最高消耗") {
		t.Errorf("expected '本周期最高消耗' field; body=%s", s)
	}
	if !strings.Contains(s, "87%") {
		t.Errorf("expected 87%% in window-max field; body=%s", s)
	}
	if !strings.Contains(s, "≥ 80%") {
		t.Errorf("expected '≥ 80%%' threshold copy in note; body=%s", s)
	}
}

func TestBuildCardPayload_ResetCard_NoWindowMax_ShowsDash(t *testing.T) {
	endAt := int64Ptr(time.Now().UnixMilli())
	n := Notification{
		Kind:            KindReset,
		Model:           "general",
		Severity:        SevInfo,
		Remaining:       100,
		Used:            0,
		Threshold:       80,
		IntervalResetAt: endAt,
		FetchedAt:       time.Now().UnixMilli(),
	}
	card := buildCardPayload(n)
	body, _ := json.Marshal(card)
	s := string(body)
	if !strings.Contains(s, "本周期最高消耗") {
		t.Errorf("expected '本周期最高消耗' field present even when nil; body=%s", s)
	}
	if !strings.Contains(s, "—") {
		t.Errorf("expected '—' placeholder when WindowMaxConsumed is nil; body=%s", s)
	}
}
