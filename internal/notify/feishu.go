package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FeishuClient posts interactive-card payloads to a Feishu/Lark custom-bot
// webhook. It auto-detects a ?secret= query parameter and applies the
// HMAC-SHA256 signing scheme documented at:
// https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot
type FeishuClient struct {
	httpClient *http.Client
}

// NewFeishuClient returns a FeishuClient with default 5s per-request timeout.
func NewFeishuClient() *FeishuClient {
	return &FeishuClient{httpClient: &http.Client{Timeout: 5 * time.Second}}
}

// NewFeishuClientWithHTTP lets callers (tests) inject a custom http.Client.
func NewFeishuClientWithHTTP(c *http.Client) *FeishuClient {
	return &FeishuClient{httpClient: c}
}

// Send posts a notification to rawURL. Retries on HTTP non-2xx or Feishu
// StatusCode != 0. Backoff: 1s, 3s, 10s (3 retries after the initial attempt).
func (c *FeishuClient) Send(ctx context.Context, rawURL string, n Notification) error {
	cleanURL, secret, err := parseURLAndSecret(rawURL)
	if err != nil {
		return fmt.Errorf("parse webhook url: %w", err)
	}
	payload := buildCardPayload(n)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}
	if secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		sig := hmacSign(secret, ts, string(body))
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return fmt.Errorf("re-decode for sign: %w", err)
		}
		m["timestamp"] = ts
		m["sign"] = sig
		body, err = json.Marshal(m)
		if err != nil {
			return fmt.Errorf("re-marshal with sign: %w", err)
		}
	}

	backoffs := []time.Duration{0, 1 * time.Second, 3 * time.Second, 10 * time.Second}
	var lastErr error
	for _, d := range backoffs {
		if d > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
		}
		err := c.post(ctx, cleanURL, body)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("feishu send failed after retries: %w", lastErr)
}

func (c *FeishuClient) post(ctx context.Context, target string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var ack struct {
		StatusCode int    `json:"StatusCode"`
		Code       int    `json:"code"`
		Msg        string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return fmt.Errorf("decode feishu ack: %w (body=%s)", err, string(raw))
	}
	if ack.StatusCode != 0 || ack.Code != 0 {
		return fmt.Errorf("feishu error StatusCode=%d code=%d msg=%s", ack.StatusCode, ack.Code, ack.Msg)
	}
	return nil
}

// parseURLAndSecret splits the user-provided URL into a clean URL (no
// ?secret=) and the extracted secret (empty if not present).
func parseURLAndSecret(rawURL string) (string, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", err
	}
	secret := u.Query().Get("secret")
	if secret != "" {
		q := u.Query()
		q.Del("secret")
		u.RawQuery = q.Encode()
	}
	return u.String(), secret, nil
}

// hmacSign computes Feishu's signature:
//   stringToSign = timestamp + "\n" + body
//   sign         = base64(hmac-sha256(secret, stringToSign))
func hmacSign(secret, ts, body string) string {
	stringToSign := ts + "\n" + body
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// buildCardPayload dispatches to the alert or reset card variant based on n.Kind.
func buildCardPayload(n Notification) map[string]any {
	if n.Kind == KindReset {
		return buildResetCard(n)
	}
	return buildAlertCard(n)
}

// buildAlertCard produces the standard threshold-crossing card. The
// consumed (Used) value is the prominent field; remaining is secondary;
// threshold label uses '≥X%' to reflect the consumption-forward semantic.
func buildAlertCard(n Notification) map[string]any {
	titlePrefix := "⚠️ 配额告警 · "
	if n.IsTest {
		titlePrefix = "[测试] "
	}
	title := titlePrefix + n.Model

	fields := []map[string]any{
		{"text": map[string]any{"tag": "lark_md", "content": "**模型**\n`" + n.Model + "`"}},
		{"text": map[string]any{"tag": "lark_md", "content": "**触发时间**\n" + time.UnixMilli(n.FetchedAt).Format("2006-01-02 15:04:05")}},
		// Consumption is the prominent value (bold); remaining is secondary.
		{"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**消耗**\n**%d%%**", n.Used)}},
		{"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**剩余**\n%d%%", n.Remaining)}},
		{"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**阈值**\n≥%d%%", n.Threshold)}},
	}
	if n.PrevNotifiedPct != nil {
		// PrevNotifiedPct is stored as the remaining value at the
		// previous alert; show the corresponding consumption figure.
		consumedAtPrev := 100 - *n.PrevNotifiedPct
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**上次告警 (消耗)**\n%d%%", consumedAtPrev)},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**上次告警 (消耗)**\n—"},
		})
	}
	if n.IntervalResetAt != nil {
		ts := FormatResetTime(*n.IntervalResetAt)
		var remain string
		if n.IntervalResetRemainMs != nil {
			remain = " (" + FormatResetRemain(*n.IntervalResetRemainMs) + ")"
		}
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**区间重置**\n" + ts + remain},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**区间重置**\n—"},
		})
	}
	if n.WeeklyResetAt != nil {
		ts := FormatResetTime(*n.WeeklyResetAt)
		var remain string
		if n.WeeklyResetRemainMs != nil {
			remain = " (" + FormatResetRemain(*n.WeeklyResetRemainMs) + ")"
		}
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**周重置**\n" + ts + remain},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**周重置**\n—"},
		})
	}
	if n.WeeklyRemainingPct != nil {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**本周剩余**\n%d%%", *n.WeeklyRemainingPct)},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**本周剩余**\n—"},
		})
	}

	elements := []any{
		map[string]any{"tag": "div", "fields": fields},
		map[string]any{"tag": "hr"},
	}
	noteText := buildTrendNoteText(n)
	elements = append(elements, map[string]any{
		"tag": "note",
		"elements": []map[string]any{
			{"tag": "plain_text", "content": noteText},
		},
	})

	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title":    map[string]any{"tag": "plain_text", "content": title},
				"template": n.Severity.Template(),
			},
			"elements": elements,
		},
	}
}

func buildTrendNoteText(n Notification) string {
	if n.IsTest {
		return "这是测试消息,不影响告警状态。"
	}
	if len(n.RecentTrend) == 0 {
		return "无近期趋势数据"
	}
	parts := make([]string, 0, len(n.RecentTrend))
	for _, p := range n.RecentTrend {
		parts = append(parts, fmt.Sprintf("%d", p.Remaining))
	}
	return "最近10分钟趋势(剩余%): " + strings.Join(parts, " → ")
}

// buildResetCard produces the interval-window-rolled-over card.
func buildResetCard(n Notification) map[string]any {
	title := "🔄 配额重置 · " + n.Model

	fields := []map[string]any{
		{"text": map[string]any{"tag": "lark_md", "content": "**模型**\n`" + n.Model + "`"}},
		{"text": map[string]any{"tag": "lark_md", "content": "**触发时间**\n" + time.UnixMilli(n.FetchedAt).Format("2006-01-02 15:04:05")}},
		{"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**消耗**\n%d%%", n.Used)}},
		{"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**剩余**\n%d%%", n.Remaining)}},
	}
	if n.WindowMaxConsumed != nil {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**本周期最高消耗**\n%d%%", *n.WindowMaxConsumed)},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**本周期最高消耗**\n—"},
		})
	}
	if n.IntervalResetAt != nil {
		ts := FormatResetTime(*n.IntervalResetAt)
		var remain string
		if n.IntervalResetRemainMs != nil {
			remain = " (" + FormatResetRemain(*n.IntervalResetRemainMs) + ")"
		}
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**区间重置**\n" + ts + remain},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**区间重置**\n—"},
		})
	}
	if n.WeeklyResetAt != nil {
		ts := FormatResetTime(*n.WeeklyResetAt)
		var remain string
		if n.WeeklyResetRemainMs != nil {
			remain = " (" + FormatResetRemain(*n.WeeklyResetRemainMs) + ")"
		}
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**周重置**\n" + ts + remain},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**周重置**\n—"},
		})
	}
	if n.WeeklyRemainingPct != nil {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**本周剩余**\n%d%%", *n.WeeklyRemainingPct)},
		})
	} else {
		fields = append(fields, map[string]any{
			"text": map[string]any{"tag": "lark_md", "content": "**本周剩余**\n—"},
		})
	}

	elements := []any{
		map[string]any{"tag": "div", "fields": fields},
		map[string]any{"tag": "hr"},
	}
	noteText := fmt.Sprintf("区间已重置,下次告警阈值 ≥ %d%% 消耗时触发。", n.Threshold)
	elements = append(elements, map[string]any{
		"tag": "note",
		"elements": []map[string]any{
			{"tag": "plain_text", "content": noteText},
		},
	})

	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title":    map[string]any{"tag": "plain_text", "content": title},
				"template": n.Severity.Template(), // SevInfo.Template() == "blue"
			},
			"elements": elements,
		},
	}
}
