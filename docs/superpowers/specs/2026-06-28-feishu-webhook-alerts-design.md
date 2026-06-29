# MiniMax Token Plan Monitor — Feishu Webhook Alerts (v2)

**Date**: 2026-06-28
**Status**: Draft (pending user review)
**Target**: Extend the existing MiniMax Token Plan Monitor with v2 alerting —
Feishu/Lark custom-bot webhook notifications driven by configurable remaining-
percent thresholds, per-interval-window deduplication, a settings UI, and a
test button. Single binary, no new dependencies beyond Go stdlib, no new env
vars, no new CLI flags.

---

## 1. Goals & Non-Goals

### 1.1 Goals
- Add a new `internal/notify` package with a Feishu webhook client and an alert
  engine that consumes each scheduler tick's `Latest` snapshot set.
- Detect when `CurrentIntervalRemainingPct` (5-minute window) crosses a
  user-configured threshold (default 80). Notify on crossing; notify again on
  every subsequent 1% drop until the interval window resets.
- Persist per-model "already-notified percent" state in SQLite; clear it when
  the interval window rolls over (remaining ≥ 95).
- Expose three HTTP endpoints for the new settings: `GET/PUT
  /api/settings/alert` and `POST /api/settings/alert/test`.
- Extend the existing settings modal with a collapsible "告警通知" section
  containing enabled flag, webhook URL, threshold input, save button, and a
  test button.
- Auto-detect `?secret=` query parameter on the webhook URL and apply Feishu's
  HMAC-SHA256 signed-request scheme.
- Send rich interactive cards (color by severity), include model name,
  remaining/used %, threshold, last-notified %, both interval & weekly reset
  times, recent 10-minute trend, server timestamp.
- Retry webhook delivery 3× with 1s/3s/10s backoff. Final failure logs warn
  and does **not** advance dedup state, so the next tick retries.
- Allow disabling alerts; on disable, clear all per-model state.
- Test button sends a real interactive card clearly tagged "测试"; does NOT
  touch any alert state.

### 1.2 Non-Goals (this v2)
- Multi-channel notifications (email, ntfy, Telegram, etc.) — only Feishu.
- Per-model thresholds — single global threshold.
- Weekly-window alerts — only interval window.
- Signature schemes other than Feishu's `?secret=` HMAC variant.
- Encrypted-at-rest storage for the webhook URL — stored plaintext in SQLite
  (the dashboard has no auth; the listener bind is the only access guard).
- Dashboard basic auth — unchanged from v1.
- Webhook delivery queue persistence — if the process dies mid-retry, the
  notification is lost.

---

## 2. Architecture & File Layout

```
internal/
├── notify/                          # NEW package
│   ├── notify.go                    # Notifier iface, Severity enum, Notification struct
│   ├── feishu.go                    # FeishuClient: URL parse, HMAC sign, POST + retry
│   ├── alert_engine.go              # Evaluate(snaps), SendTest()
│   ├── notify_test.go
│   ├── feishu_test.go
│   └── alert_engine_test.go
├── storage/
│   ├── settings.go                  # NEW: GetAlertConfig / SetAlertConfig
│   ├── alert_state.go               # NEW: per-model notified pcts
│   ├── settings_test.go             # NEW
│   ├── alert_state_test.go          # NEW
│   └── sqlite.go                    # EDITED: append 2 new CREATE TABLE statements
├── scheduler/
│   └── scheduler.go                 # EDITED: optional alerter, fire after Insert before Broadcast
└── server/
    ├── handlers_alert.go            # NEW: GET/PUT/POST handlers
    ├── server.go                    # EDITED: 3 new routes, AlertConfig/AlertTest fields
    ├── handlers_alert_test.go       # NEW
    └── web/
        ├── index.html               # EDITED: alert section appended to modal
        ├── app.js                   # EDITED: alert load/save/test handlers
        └── style.css                # EDITED: collapsible section + severity badge styles

cmd/minimax-monitor/main.go          # EDITED: construct notifier + engine, wire to scheduler & server
docs/superpowers/
├── specs/2026-06-28-feishu-webhook-alerts-design.md   # THIS FILE
└── plans/2026-06-28-feishu-webhook-alerts.md          # (created by writing-plans)
```

No new third-party dependencies — Feishu signing uses stdlib
`crypto/hmac` + `crypto/sha256`; HTTP uses `net/http`; JSON uses `encoding/json`.

---

## 3. Data Model

### 3.1 SQLite additions

Appended to `schema` in `internal/storage/sqlite.go` (auto-applied on next
startup via `CREATE TABLE IF NOT EXISTS`):

```sql
-- generic key-value settings
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- per-model alert dedup state
CREATE TABLE IF NOT EXISTS alert_state (
    model_name    TEXT PRIMARY KEY,
    notified_pcts TEXT NOT NULL,        -- JSON array of ints, e.g. "[80,79,78]"
    updated_at    INTEGER NOT NULL
);
```

### 3.2 Default values

| key | default | type |
|---|---|---|
| `alert.enabled` | `false` | string `"true"` / `"false"` |
| `alert.url` | `""` | string |
| `alert.threshold` | `80` | int (1..100) |
| `alert.last_test_at` | `0` | int64 (epoch ms) |

### 3.3 Go types

```go
// internal/storage/settings.go
type AlertConfig struct {
    Enabled   bool   `json:"enabled"`
    URL       string `json:"url"`
    Threshold int    `json:"threshold"`
}
func (db *DB) GetAlertConfig(ctx context.Context) (AlertConfig, error)
func (db *DB) SetAlertConfig(ctx context.Context, cfg AlertConfig) error

// internal/storage/alert_state.go
type AlertState struct {
    NotifiedPcts []int `json:"notified_pcts"`
    UpdatedAt    int64 `json:"updated_at"`
}
func (db *DB) GetAlertState(ctx context.Context, model string) (AlertState, error)
func (db *DB) SetAlertState(ctx context.Context, model string, st AlertState) error
func (db *DB) ClearAlertState(ctx context.Context, model string) error   // per-model
func (db *DB) ClearAllAlertStates(ctx context.Context) error             // on disable
```

Threshold validation: `0 < t ≤ 100`. URL validation: must parse as URL and
host ∈ {`open.feishu.cn`, `open.larksuite.com`}.

---

## 4. Notification Engine

### 4.1 Public types

```go
// internal/notify/notify.go
type Severity int
const (
    SevInfo     Severity = iota  // test card
    SevLow                        // remaining > 50%
    SevMid                        // 30..50%
    SevHigh                       // 10..30%
    SevCritical                   // < 10%
)

type TrendPoint struct {
    FetchedAt int64
    Remaining int
}

type Notification struct {
    IsTest                  bool
    Model                   string
    Severity                Severity
    Remaining               int
    Used                    int          // 100 - Remaining
    WeeklyRemainingPct      *int         // nullable
    Threshold               int
    PrevNotifiedPct         *int         // most recent in this model's notified list
    IntervalResetAt         *int64       // unix ms; nil if missing
    IntervalResetRemainMs   *int64       // nil if past
    WeeklyResetAt           *int64
    WeeklyResetRemainMs     *int64
    RecentTrend             []TrendPoint // last 10 minutes, 1-minute buckets
    FetchedAt               int64
}

type Notifier interface {
    Send(ctx context.Context, rawURL string, n Notification) error
}
```

### 4.2 Engine behaviour

```go
// internal/notify/alert_engine.go (pseudo)
func (e *AlertEngine) Evaluate(ctx, snaps []storage.Snapshot) error {
    cfg := e.cfg()  // live read each tick
    if !cfg.Enabled || cfg.URL == "" { return nil }

    for _, s := range snaps {
        if s.IntervalRemainingPct == nil { continue }
        remaining := *s.IntervalRemainingPct

        // 1) Interval reset detection
        if remaining >= 95 {
            _ = e.db.ClearAlertState(ctx, s.ModelName)
            continue
        }
        // 2) Above threshold — nothing to do
        if remaining > cfg.Threshold { continue }
        // 3) Already notified at this exact pct
        st, _ := e.db.GetAlertState(ctx, s.ModelName)
        if containsInt(st.NotifiedPcts, remaining) { continue }
        // 4) Build + send
        n := buildNotification(s, cfg.Threshold, remaining, st.NotifiedPcts, trend)
        if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
            slog.Warn("alert send failed", "model", s.ModelName, "pct", remaining, "err", err)
            continue  // do NOT advance state on failure
        }
        // 5) Advance dedup
        st.NotifiedPcts = appendUnique(st.NotifiedPcts, remaining)
        st.UpdatedAt = e.nowFn().UnixMilli()
        _ = e.db.SetAlertState(ctx, s.ModelName, st)
    }
    return nil
}
```

`buildNotification` queries `db.History(model, now-10m, now, 60s)` for
`TrendPoint[]` and pulls `IntervalEndAt`/`WeeklyEndAt` from the snapshot for
reset fields. Missing fields render as `—` (no error).

### 4.3 SendTest

```go
func (e *AlertEngine) SendTest(ctx context.Context) (int64, error) {
    cfg := e.cfg()
    if !cfg.Enabled { return 0, errConfigMissing }
    if cfg.URL == "" { return 0, errConfigMissing }
    snaps, _ := e.db.Latest(ctx)
    if len(snaps) == 0 {
        // build a synthetic snapshot with placeholder values so the card is still useful
        snaps = []storage.Snapshot{{ModelName: "general"}}
    }
    n := buildNotification(snaps[0], cfg.Threshold, 99, nil, nil)  // remaining=99, no trend
    n.IsTest = true
    n.Severity = SevInfo
    if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
        return 0, err
    }
    return e.nowFn().UnixMilli(), nil
}
```

`SendTest` does NOT read or write `alert_state`.

---

## 5. Feishu Client

### 5.1 URL parse + signing

```go
// internal/notify/feishu.go (pseudo)
func parseURLAndSecret(rawURL string) (cleanURL, secret string, err error) {
    u, err := url.Parse(rawURL)
    if err != nil { return "", "", err }
    secret = u.Query().Get("secret")
    if secret != "" {
        q := u.Query()
        q.Del("secret")
        u.RawQuery = q.Encode()
    }
    return u.String(), secret, nil
}

func hmacSign(secret, ts, body string) string {
    stringToSign := ts + "\n" + body
    h := hmac.New(sha256.New, []byte(secret))
    h.Write([]byte(stringToSign))
    return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
```

### 5.2 POST + retry

Backoff schedule: `0s, 1s, 3s, 10s`. Per-attempt timeout: `5s` (via
`http.Client{Timeout: 5s}`). Total worst case: ~14s + 5s × 4 attempts.

Response success criteria:
- HTTP 2xx AND
- JSON body contains `"StatusCode": 0` (or `"code": 0`, whichever the bot
  variant returns)

Final failure path: return `fmt.Errorf("feishu send failed after retries: %w", lastErr)`.
Engine logs warn and does not advance state.

### 5.3 Card payload

```json
{
  "timestamp": "<unix sec, only when secret present>",
  "sign": "<base64, only when secret present>",
  "msg_type": "interactive",
  "card": {
    "header": {
      "title": {"tag": "plain_text", "content": "⚠️ 配额告警 · general"},
      "template": "red"
    },
    "elements": [
      {"tag": "div", "fields": [
        {"text": {"tag": "lark_md", "content": "**模型**\n`general`"}},
        {"text": {"tag": "lark_md", "content": "**触发时间**\n2026-06-28 16:42:13"}},
        {"text": {"tag": "lark_md", "content": "**剩余**\n**60%**"}},
        {"text": {"tag": "lark_md", "content": "**已用**\n40%"}},
        {"text": {"tag": "lark_md", "content": "**阈值**\n≤80%"}},
        {"text": {"tag": "lark_md", "content": "**上次告警**\n80%"}},
        {"text": {"tag": "lark_md", "content": "**区间重置**\n16:45:55 (3分42秒后)"}},
        {"text": {"tag": "lark_md", "content": "**周重置**\n07-04 00:00 (5天7时后)"}},
        {"text": {"tag": "lark_md", "content": "**本周剩余**\n87%"}}
      ]},
      {"tag": "hr"},
      {"tag": "note", "elements": [
        {"tag": "plain_text", "content": "最近10分钟趋势(剩余%): 100 → 98 → 95 → 90 → 80 → 72 → 60"}},
        {"tag": "plain_text", "content": "区间重置于 16:45:55 · 周重置于 07-04 00:00"}}
      ]}
    ]
  }
}
```

Severity → template color:

| Severity | Remaining % | Template |
|---|---|---|
| SevInfo | (test) | `blue` |
| SevLow | > 50 | `green` |
| SevMid | 30–50 | `yellow` |
| SevHigh | 10–30 | `orange` |
| SevCritical | < 10 | `red` |

Test card title includes `[测试]` prefix; note line is `这是测试消息,不影响告警状态`.

---

## 6. HTTP API

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/settings/alert` | — | `200 {enabled, url, threshold, last_test_at}` (URL tail-masked server-side) |
| PUT | `/api/settings/alert` | `{enabled, url, threshold}` | `200 {ok:true}` / `400 {error}` |
| POST | `/api/settings/alert/test` | — | `200 {ok:true, sent_at}` / `400 {error:"config_missing"}` / `502 {error, reason}` / `500 {error, reason}` |

### 6.1 URL masking (server-side)

```go
func maskURL(u string) string {
    if u == "" { return "" }
    if len(u) <= 32 { return u[:4] + "..." + u[len(u)-4:] }
    return u[:24] + "..." + u[len(u)-4:]
}
```

The PUT request body carries the full unmasked URL; the response body always
returns the masked form.

### 6.2 Validation (PUT)

- `threshold`: integer, `0 < t ≤ 100` (default 80 if absent)
- `url`: if non-empty, must `url.Parse` cleanly AND `u.Host ∈ {open.feishu.cn, open.larksuite.com}`
- `enabled=true` requires `url != ""`

### 6.3 Server wiring

```go
// internal/server/server.go (Server struct)
AlertConfig func() storage.AlertConfig
AlertTest   func(ctx context.Context) (int64, error)
```

Routes added in `routes()`:
```go
s.Engine.GET("/api/settings/alert",        s.handleAlertGet)
s.Engine.PUT("/api/settings/alert",        s.handleAlertPut)
s.Engine.POST("/api/settings/alert/test",  s.handleAlertTest)
```

---

## 7. Scheduler Integration

```go
// internal/scheduler/scheduler.go
type AlertEngine interface {
    Evaluate(ctx context.Context, snaps []storage.Snapshot) error
}

// Scheduler struct: new field
alerter AlertEngine

// New() signature extended:
func New(f Fetcher, ins Inserter, b Broadcaster, keyFn func() (string, error),
    interval, pruneEvery time.Duration, retentionDays int,
    alerter AlertEngine) *Scheduler

// RunOnce — after Broadcast becomes a non-blocking fire:
go func(snaps []storage.Snapshot) {
    if sc.alerter == nil { return }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    if err := sc.alerter.Evaluate(ctx, snaps); err != nil {
        slog.Warn("alert evaluate", "err", err)
    }
}(snaps)
```

Rationale for goroutine: Feishu POST + retry can take up to ~14s; running
inline would push the next 10s tick past its deadline.

`alerter` is optional (nil-safe) so existing scheduler tests continue to work
unchanged.

---

## 8. Web UI

### 8.1 Modal markup

Appended inside `.modal-body`, below the existing API Key section:

```html
<div class="alert-section">
  <button class="section-toggle" id="alertToggle" aria-expanded="false">
    <span class="chev">▸</span>
    <span class="section-title">告警通知</span>
    <span class="badge" id="alertBadge">未配置</span>
  </button>
  <div class="section-body hidden" id="alertBody">
    <div class="form-row">
      <label class="switch">
        <input type="checkbox" id="alertEnabled" />
        <span>启用</span>
      </label>
    </div>
    <div class="form-row">
      <label for="alertURL">Webhook URL</label>
      <input id="alertURL" type="text" autocomplete="off"
             placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/..." />
      <small class="muted">支持 ?secret=xxx 自动签名</small>
    </div>
    <div class="form-row">
      <label for="alertThreshold">阈值 (剩余%)</label>
      <input id="alertThreshold" type="number" min="1" max="100" value="80" />
      <small class="muted">剩余≤该值告警;每下降1%再告警一次</small>
    </div>
    <div class="form-error hidden" id="alertError"></div>
    <div class="form-actions">
      <button id="alertTestBtn" class="link">发送测试</button>
      <div class="spacer"></div>
      <button id="alertSaveBtn" class="primary">保存</button>
    </div>
  </div>
</div>
```

### 8.2 Behaviour

- On `openModal()` → `GET /api/settings/alert` → set badge text (`未配置` /
  `已启用·阈值80%` / `已禁用`), fill inputs.
- Toggle expands/collapses section (chevron rotates 90°).
- `alertSaveBtn` → `PUT /api/settings/alert` with `{enabled, url, threshold}`;
  on success show inline confirmation; on 400 show `alertError`.
- `alertTestBtn` → `POST /api/settings/alert/test`; show inline status text
  (`发送中…` → `✓ 已发送` or `✗ <reason>`).
- `handleAlertPut`: detects `prevCfg.Enabled == true && newCfg.Enabled == false`
  and calls `db.ClearAllAlertStates(ctx)` before persisting new config.

### 8.3 CSS additions

```css
.alert-section { border-top: 1px solid var(--border); margin-top: 16px; padding-top: 12px; }
.section-toggle {
  display: flex; align-items: center; gap: 8px;
  width: 100%; background: none; border: 0; color: inherit;
  cursor: pointer; padding: 8px 0; font-size: 14px;
}
.section-toggle .chev { transition: transform 200ms ease; }
.section-toggle[aria-expanded="true"] .chev { transform: rotate(90deg); }
.section-toggle:hover { color: var(--accent-general); }
.section-body { padding-left: 12px; }
.badge.warn { background: rgba(245,158,11,.15); color: var(--status-warn); }
```

---

## 9. main.go Wiring

```go
// cmd/minimax-monitor/main.go (additions)
feishu := notify.NewFeishuClient()
engine := notify.NewAlertEngine(db, feishu, func() storage.AlertConfig {
    cfg, _ := db.GetAlertConfig(context.Background())
    return cfg
})

sched := scheduler.New(cli, db, hub, keyFn, cfg.PollInterval, 24*time.Hour, cfg.RetentionDays, engine)

srv := server.New(db, store)
srv.Hub = hub
srv.DBPath = absDB
srv.PollInterval = cfg.PollInterval
srv.Stats = sched.Stats
srv.Validator = func(ctx context.Context, key string) error { _, err := cli.Fetch(ctx, key); return err }
srv.OnKeyChange = func() {}
srv.AlertConfig = func() storage.AlertConfig { cfg, _ := db.GetAlertConfig(context.Background()); return cfg }
srv.AlertTest = engine.SendTest
```

No new env vars. No new CLI flags.

---

## 10. Error Handling

| Failure | Behavior |
|---|---|
| Feishu webhook 4xx/5xx | retry with backoff; final fail log warn; don't advance state |
| Feishu returns `StatusCode != 0` | same as HTTP failure |
| URL parse error in `parseURLAndSecret` | return immediately; engine logs warn |
| DB read/write error in engine | log warn, skip this model this tick |
| Threshold change at runtime | next tick reads fresh cfg; existing notified_pcts may no longer be relevant → on next reset they clear naturally |
| Settings PUT with bad URL | 400 `{error:"url must be on open.feishu.cn or open.larksuite.com"}` |
| Settings PUT with `enabled=true` but empty URL and no prior URL | 400 `{error:"url required when enabled"}` |
| Settings PUT with empty URL and prior URL exists | 200; URL preserved (frontend masks URL for security, so re-sending it is not possible) |
| Test button with `enabled=false` | 400 `{error:"config_missing"}` |
| Scheduler goroutine context cancel | alert evaluate aborts; broadcast unaffected |

---

## 11. Testing Strategy

| File | Tests |
|---|---|
| `internal/storage/settings_test.go` | round-trip AlertConfig; default values when keys absent; partial overrides preserve unset keys |
| `internal/storage/alert_state_test.go` | set/get/clear; append-unique; ClearAll |
| `internal/notify/feishu_test.go` | URL parse detects/removes `?secret=`; HMAC matches known vector; `httptest.Server` mock returning `StatusCode:0` (success), `StatusCode:230002` (retry), HTTP 500 (retry); retry attempts counted; abort on success |
| `internal/notify/alert_engine_test.go` | disabled → no calls; URL empty → no calls; above threshold → no calls; already notified → no calls; crossing threshold → 1 call; interval reset (remaining≥95) → state cleared; failed send → state NOT advanced |
| `internal/server/handlers_alert_test.go` | GET returns masked URL; PUT validates threshold range and URL domain; PUT disabled+empty URL succeeds; POST test with no config → 400; POST test with mocked Feishu → 200 |
| `internal/scheduler` smoke | fake AlertEngine records calls; after N ticks assert correct pcts triggered |

Test command: `go test ./...`. Coverage target: ≥ 70% for `internal/notify` and
new `internal/storage` files.

---

## 12. Acceptance Criteria

The v2 alert feature is "done" when:

1. `build.bat` (and `build-cross.bat`) still produce working binaries.
2. On a fresh DB, the two new tables (`settings`, `alert_state`) are created
   automatically on startup.
3. Settings modal shows a collapsible "告警通知" section; default-collapsed.
4. User can save `{enabled:false, url:"", threshold:80}` and reload sees the
   same values (defaults).
5. With a real Feishu webhook URL + `enabled:true`, the test button sends a
   card within 5s, returns `200 {ok:true, sent_at:<ms>}`.
6. With threshold 80, dropping from 81% → 80% triggers 1 alert; 80% → 79%
   triggers 1 more; 79% → 78% triggers 1 more. Repeating the same pct does
   NOT re-trigger.
7. After interval window rolls over (remaining ≥ 95), state clears, and the
   next time remaining drops ≤ threshold the cycle restarts.
8. Webhook URL with `?secret=...` causes the request body to carry
   `timestamp` + `sign`; Feishu's signature verification accepts it.
9. Disable toggle clears all `alert_state` rows.
10. `go test ./...` passes; new packages meet coverage target.

---

## 13. Risks

| Risk | Mitigation |
|---|---|
| Feishu response shape varies (StatusCode/code field naming) | Accept either `"StatusCode":0` or `"code":0` as success |
| Webhook URL leaked via `GET /api/settings/alert` | Always return masked URL server-side; client never sees full URL after save |
| Slow webhook blocks scheduler tick | Run evaluate in goroutine with 30s ctx timeout |
| HMAC implementation off-by-one (stringToSign format) | Unit-tested against Feishu's official reference vector |
| Test button spams chat during dev | Test card visually distinct (`[测试]` prefix + `这是测试消息` note); does not affect state so real alerts still fire normally |
| Process restart loses in-flight notifications | Accepted as documented non-goal |
| Weekly reset time computation is heuristic (assumes Monday 00:00) | Use `weekly_end_at` from API when present; fall back to next-Monday only when nil |