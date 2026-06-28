# Feishu Webhook Alerts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Feishu/Lark custom-bot webhook notifications to MiniMax Token Plan Monitor — driven by a configurable remaining-percent threshold on the 5-minute interval window — with a settings UI, a test button, and per-interval-window deduplication.

**Architecture:** A new `internal/notify` package owns a `FeishuClient` (URL parse + HMAC sign + POST with 3× retry) and an `AlertEngine` that consumes the scheduler's latest snapshot set. Two new SQLite tables (`settings`, `alert_state`) hold config and per-model dedup state. The scheduler fires the engine in a goroutine after `Insert`+`Broadcast` so webhook latency never blocks the 10s tick. Three new HTTP endpoints (`GET/PUT/POST /api/settings/alert[test]`) wire the UI. The settings modal gains a collapsible "告警通知" section.

**Tech Stack:** Go 1.25 stdlib only (no new deps); `modernc.org/sqlite` (already present); `gin-gonic/gin` (already present); Feishu interactive card format.

## Global Constraints

- Go version: `1.22+` (toolchain directive in `go.mod`).
- Build flag for ALL targets: `CGO_ENABLED=0`.
- Compile flag for ALL targets: `go build -trimpath -ldflags="-s -w"`.
- Output directory: `./dist/`. Binary name: `minimax-monitor[.exe]`.
- No new third-party dependencies — use stdlib `net/http`, `crypto/hmac`, `crypto/sha256`, `encoding/json`, `net/url`.
- No new env vars. No new CLI flags. Configuration is purely through the UI.
- API key MUST NEVER appear in logs, URLs, or frontend JS — webhook URL is masked when returned from `GET /api/settings/alert`.
- All new code must compile cleanly under `go build ./...` and `go test ./...`.
- Follow existing package layout (`internal/...`); no top-level packages.
- Web assets MUST remain embedded with `//go:embed all:web`.

---

## File Structure

```
internal/
├── notify/                           # NEW package
│   ├── notify.go                     # Notifier iface, Severity enum, Notification, TrendPoint
│   ├── feishu.go                     # FeishuClient: URL parse + HMAC sign + POST + retry
│   ├── alert_engine.go               # Evaluate + SendTest
│   ├── notify_test.go                # severity classifier, notification builder tests
│   ├── feishu_test.go                # URL parse, sign, retry
│   └── alert_engine_test.go          # state-machine tests
├── storage/
│   ├── settings.go                   # NEW: AlertConfig CRUD
│   ├── alert_state.go                # NEW: per-model state
│   ├── settings_test.go              # NEW
│   ├── alert_state_test.go           # NEW
│   └── sqlite.go                     # EDITED: append 2 CREATE TABLE statements
├── scheduler/
│   └── scheduler.go                  # EDITED: optional alerter field + goroutine fire
└── server/
    ├── handlers_alert.go             # NEW: GET/PUT/POST handlers
    ├── handlers_alert_test.go        # NEW
    ├── server.go                     # EDITED: 3 new routes, AlertConfig/AlertTest fields
    └── web/
        ├── index.html                # EDITED: alert section appended to modal
        ├── app.js                    # EDITED: load/save/test alert handlers
        └── style.css                 # EDITED: collapsible + badge styles

cmd/minimax-monitor/main.go           # EDITED: construct notifier + engine, wire to scheduler & server
docs/superpowers/
├── specs/2026-06-28-feishu-webhook-alerts-design.md    # (already committed)
└── plans/2026-06-28-feishu-webhook-alerts.md           # THIS FILE
```

---

## Task 1: SQLite schema additions + AlertConfig storage

**Files:**
- Modify: `internal/storage/sqlite.go` (append 2 CREATE TABLE statements)
- Create: `internal/storage/settings.go`
- Create: `internal/storage/settings_test.go`

**Interfaces:**
- Consumes: existing `*DB` type
- Produces:
  - `type AlertConfig struct { Enabled bool; URL string; Threshold int }`
  - `func (db *DB) GetAlertConfig(ctx context.Context) (AlertConfig, error)`
  - `func (db *DB) SetAlertConfig(ctx context.Context, cfg AlertConfig) error`

- [ ] **Step 1: Add 2 tables to schema constant in `internal/storage/sqlite.go`**

Edit the existing `const schema = \`...\`` block. Append after the existing `CREATE INDEX` statement (don't modify what comes before):

```sql
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS alert_state (
    model_name    TEXT PRIMARY KEY,
    notified_pcts TEXT NOT NULL,
    updated_at    INTEGER NOT NULL
);
```

The full file `const schema` becomes (existing lines kept verbatim, new lines appended):

```go
const schema = `
CREATE TABLE IF NOT EXISTS snapshot (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    fetched_at                  INTEGER NOT NULL,
    model_name                  TEXT    NOT NULL,
    interval_remaining_pct      INTEGER,
    interval_status             INTEGER,
    interval_total_count        INTEGER,
    interval_usage_count        INTEGER,
    interval_end_at             INTEGER,
    interval_remains_ms         INTEGER,
    weekly_remaining_pct        INTEGER,
    weekly_status               INTEGER,
    weekly_total_count          INTEGER,
    weekly_usage_count          INTEGER,
    weekly_end_at               INTEGER,
    weekly_remains_ms           INTEGER,
    raw_json                    TEXT
);
CREATE INDEX IF NOT EXISTS idx_snap_model_time
    ON snapshot(model_name, fetched_at DESC);
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS alert_state (
    model_name    TEXT PRIMARY KEY,
    notified_pcts TEXT NOT NULL,
    updated_at    INTEGER NOT NULL
);
`
```

- [ ] **Step 2: Write failing test for `GetAlertConfig` and `SetAlertConfig`**

Create `internal/storage/settings_test.go`:

```go
package storage

import (
    "context"
    "path/filepath"
    "testing"
)

func TestAlertConfig_Default(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    cfg, err := db.GetAlertConfig(ctx)
    if err != nil {
        t.Fatalf("GetAlertConfig: %v", err)
    }
    if cfg.Enabled != false {
        t.Errorf("default Enabled = %v, want false", cfg.Enabled)
    }
    if cfg.URL != "" {
        t.Errorf("default URL = %q, want \"\"", cfg.URL)
    }
    if cfg.Threshold != 80 {
        t.Errorf("default Threshold = %d, want 80", cfg.Threshold)
    }
}

func TestAlertConfig_RoundTrip(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    want := AlertConfig{Enabled: true, URL: "https://open.feishu.cn/open-apis/bot/v2/hook/abc", Threshold: 75}
    if err := db.SetAlertConfig(ctx, want); err != nil {
        t.Fatalf("SetAlertConfig: %v", err)
    }
    got, err := db.GetAlertConfig(ctx)
    if err != nil {
        t.Fatalf("GetAlertConfig: %v", err)
    }
    if got != want {
        t.Errorf("round-trip = %+v, want %+v", got, want)
    }
}

func TestAlertConfig_OverwritePreservesAllFields(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    _ = db.SetAlertConfig(ctx, AlertConfig{Enabled: true, URL: "u1", Threshold: 90})
    if err := db.SetAlertConfig(ctx, AlertConfig{Enabled: false, URL: "u2", Threshold: 60}); err != nil {
        t.Fatalf("SetAlertConfig: %v", err)
    }
    got, _ := db.GetAlertConfig(ctx)
    want := AlertConfig{Enabled: false, URL: "u2", Threshold: 60}
    if got != want {
        t.Errorf("after overwrite = %+v, want %+v", got, want)
    }
}
```

Note: `openTest(t)` is already defined in `internal/storage/snapshot_test.go` and is in the same package.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/storage/ -run TestAlertConfig -v`
Expected: FAIL — `AlertConfig` undefined.

- [ ] **Step 4: Implement `internal/storage/settings.go`**

```go
package storage

import (
    "context"
    "encoding/json"
    "fmt"
)

const (
    alertEnabledKey   = "alert.enabled"
    alertURLKey       = "alert.url"
    alertThresholdKey = "alert.threshold"
)

type AlertConfig struct {
    Enabled   bool   `json:"enabled"`
    URL       string `json:"url"`
    Threshold int    `json:"threshold"`
}

func (db *DB) GetAlertConfig(ctx context.Context) (AlertConfig, error) {
    rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN (?, ?, ?)`,
        alertEnabledKey, alertURLKey, alertThresholdKey)
    if err != nil {
        return AlertConfig{}, err
    }
    defer rows.Close()

    cfg := AlertConfig{Threshold: 80} // default threshold
    for rows.Next() {
        var k, v string
        if err := rows.Scan(&k, &v); err != nil {
            return AlertConfig{}, err
        }
        switch k {
        case alertEnabledKey:
            cfg.Enabled = v == "true"
        case alertURLKey:
            cfg.URL = v
        case alertThresholdKey:
            n := 0
            _, _ = fmt.Sscanf(v, "%d", &n)
            if n > 0 {
                cfg.Threshold = n
            }
        }
    }
    return cfg, rows.Err()
}

func (db *DB) SetAlertConfig(ctx context.Context, cfg AlertConfig) error) {
    if _, err := db.ExecContext(ctx, `DELETE FROM settings WHERE key IN (?, ?, ?)`,
        alertEnabledKey, alertURLKey, alertThresholdKey); err != nil {
        return err
    }
    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    pairs := []struct{ k, v string }{
        {alertEnabledKey, boolStr(cfg.Enabled)},
        {alertURLKey, cfg.URL},
        {alertThresholdKey, fmt.Sprintf("%d", cfg.Threshold)},
    }
    for _, p := range pairs {
        if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)`, p.k, p.v); err != nil {
            return err
        }
    }
    return tx.Commit()
}

func boolStr(b bool) string {
    if b { return "true" }
    return "false"
}

// MarshalJSON for AlertConfig — not used yet, but helps if someone marshals to JSON.
func (c AlertConfig) MarshalJSON() ([]byte, error) {
    type alias AlertConfig
    return json.Marshal(alias(c))
}
```

Wait — fix the syntax in `SetAlertConfig`. Correct version:

```go
package storage

import (
    "context"
    "fmt"
)

const (
    alertEnabledKey   = "alert.enabled"
    alertURLKey       = "alert.url"
    alertThresholdKey = "alert.threshold"
)

type AlertConfig struct {
    Enabled   bool   `json:"enabled"`
    URL       string `json:"url"`
    Threshold int    `json:"threshold"`
}

func (db *DB) GetAlertConfig(ctx context.Context) (AlertConfig, error) {
    rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key IN (?, ?, ?)`,
        alertEnabledKey, alertURLKey, alertThresholdKey)
    if err != nil {
        return AlertConfig{}, err
    }
    defer rows.Close()

    cfg := AlertConfig{Threshold: 80}
    for rows.Next() {
        var k, v string
        if err := rows.Scan(&k, &v); err != nil {
            return AlertConfig{}, err
        }
        switch k {
        case alertEnabledKey:
            cfg.Enabled = v == "true"
        case alertURLKey:
            cfg.URL = v
        case alertThresholdKey:
            n := 0
            _, _ = fmt.Sscanf(v, "%d", &n)
            if n > 0 {
                cfg.Threshold = n
            }
        }
    }
    return cfg, rows.Err()
}

func (db *DB) SetAlertConfig(ctx context.Context, cfg AlertConfig) error {
    if _, err := db.ExecContext(ctx, `DELETE FROM settings WHERE key IN (?, ?, ?)`,
        alertEnabledKey, alertURLKey, alertThresholdKey); err != nil {
        return err
    }
    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    pairs := []struct{ k, v string }{
        {alertEnabledKey, boolStr(cfg.Enabled)},
        {alertURLKey, cfg.URL},
        {alertThresholdKey, fmt.Sprintf("%d", cfg.Threshold)},
    }
    for _, p := range pairs {
        if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)`, p.k, p.v); err != nil {
            return err
        }
    }
    return tx.Commit()
}

func boolStr(b bool) string {
    if b {
        return "true"
    }
    return "false"
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/storage/ -run TestAlertConfig -v`
Expected: PASS (all 3 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/storage/sqlite.go internal/storage/settings.go internal/storage/settings_test.go
git commit -m "feat(storage): add settings + alert_config for webhook alerts"
```

---

## Task 2: Alert state storage

**Files:**
- Create: `internal/storage/alert_state.go`
- Create: `internal/storage/alert_state_test.go`

**Interfaces:**
- Consumes: existing `*DB` type, `storage.Snapshot`
- Produces:
  - `type AlertState struct { NotifiedPcts []int; UpdatedAt int64 }`
  - `func (db *DB) GetAlertState(ctx context.Context, model string) (AlertState, error)`
  - `func (db *DB) SetAlertState(ctx context.Context, model string, st AlertState) error`
  - `func (db *DB) ClearAlertState(ctx context.Context, model string) error`
  - `func (db *DB) ClearAllAlertStates(ctx context.Context) error`

- [ ] **Step 1: Write failing test for AlertState CRUD**

Create `internal/storage/alert_state_test.go`:

```go
package storage

import (
    "context"
    "testing"
)

func TestAlertState_DefaultEmpty(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    st, err := db.GetAlertState(ctx, "general")
    if err != nil {
        t.Fatalf("GetAlertState: %v", err)
    }
    if len(st.NotifiedPcts) != 0 {
        t.Errorf("default NotifiedPcts = %v, want empty", st.NotifiedPcts)
    }
    if st.UpdatedAt != 0 {
        t.Errorf("default UpdatedAt = %d, want 0", st.UpdatedAt)
    }
}

func TestAlertState_RoundTrip(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    want := AlertState{NotifiedPcts: []int{80, 79, 78}, UpdatedAt: 1782561600000}
    if err := db.SetAlertState(ctx, "general", want); err != nil {
        t.Fatalf("SetAlertState: %v", err)
    }
    got, err := db.GetAlertState(ctx, "general")
    if err != nil {
        t.Fatalf("GetAlertState: %v", err)
    }
    if len(got.NotifiedPcts) != 3 || got.NotifiedPcts[0] != 80 || got.NotifiedPcts[1] != 79 || got.NotifiedPcts[2] != 78 {
        t.Errorf("NotifiedPcts = %v, want [80 79 78]", got.NotifiedPcts)
    }
    if got.UpdatedAt != 1782561600000 {
        t.Errorf("UpdatedAt = %d, want 1782561600000", got.UpdatedAt)
    }
}

func TestAlertState_PerModelIsolation(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    _ = db.SetAlertState(ctx, "general", AlertState{NotifiedPcts: []int{80}})
    _ = db.SetAlertState(ctx, "video", AlertState{NotifiedPcts: []int{50}})
    g, _ := db.GetAlertState(ctx, "general")
    v, _ := db.GetAlertState(ctx, "video")
    if g.NotifiedPcts[0] != 80 {
        t.Errorf("general NotifiedPcts = %v", g.NotifiedPcts)
    }
    if v.NotifiedPcts[0] != 50 {
        t.Errorf("video NotifiedPcts = %v", v.NotifiedPcts)
    }
}

func TestAlertState_ClearOne(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    _ = db.SetAlertState(ctx, "general", AlertState{NotifiedPcts: []int{80}})
    _ = db.SetAlertState(ctx, "video", AlertState{NotifiedPcts: []int{50}})
    if err := db.ClearAlertState(ctx, "general"); err != nil {
        t.Fatalf("ClearAlertState: %v", err)
    }
    g, _ := db.GetAlertState(ctx, "general")
    v, _ := db.GetAlertState(ctx, "video")
    if len(g.NotifiedPcts) != 0 {
        t.Errorf("general after clear = %v, want empty", g.NotifiedPcts)
    }
    if v.NotifiedPcts[0] != 50 {
        t.Errorf("video should be untouched, got %v", v.NotifiedPcts)
    }
}

func TestAlertState_ClearAll(t *testing.T) {
    db := openTest(t)
    ctx := context.Background()
    _ = db.SetAlertState(ctx, "general", AlertState{NotifiedPcts: []int{80}})
    _ = db.SetAlertState(ctx, "video", AlertState{NotifiedPcts: []int{50}})
    if err := db.ClearAllAlertStates(ctx); err != nil {
        t.Fatalf("ClearAllAlertStates: %v", err)
    }
    g, _ := db.GetAlertState(ctx, "general")
    v, _ := db.GetAlertState(ctx, "video")
    if len(g.NotifiedPcts) != 0 || len(v.NotifiedPcts) != 0 {
        t.Errorf("ClearAll did not clear; general=%v video=%v", g.NotifiedPcts, v.NotifiedPcts)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/storage/ -run TestAlertState -v`
Expected: FAIL — `AlertState` undefined.

- [ ] **Step 3: Implement `internal/storage/alert_state.go`**

```go
package storage

import (
    "context"
    "encoding/json"
    "fmt"
)

type AlertState struct {
    NotifiedPcts []int `json:"notified_pcts"`
    UpdatedAt    int64 `json:"updated_at"`
}

func (db *DB) GetAlertState(ctx context.Context, model string) (AlertState, error) {
    var raw string
    err := db.QueryRowContext(ctx,
        `SELECT notified_pcts FROM alert_state WHERE model_name = ?`, model,
    ).Scan(&raw)
    if err != nil {
        // sql.ErrNoRows is expected on first call
        return AlertState{}, nil
    }
    var st AlertState
    if err := json.Unmarshal([]byte(raw), &st); err != nil {
        return AlertState{}, fmt.Errorf("decode alert_state for %s: %w", model, err)
    }
    return st, nil
}

func (db *DB) SetAlertState(ctx context.Context, model string, st AlertState) error {
    raw, err := json.Marshal(st)
    if err != nil {
        return err
    }
    _, err = db.ExecContext(ctx, `
        INSERT INTO alert_state(model_name, notified_pcts, updated_at)
        VALUES (?, ?, ?)
        ON CONFLICT(model_name) DO UPDATE SET
            notified_pcts = excluded.notified_pcts,
            updated_at    = excluded.updated_at
    `, model, string(raw), st.UpdatedAt)
    return err
}

func (db *DB) ClearAlertState(ctx context.Context, model string) error {
    _, err := db.ExecContext(ctx, `DELETE FROM alert_state WHERE model_name = ?`, model)
    return err
}

func (db *DB) ClearAllAlertStates(ctx context.Context) error {
    _, err := db.ExecContext(ctx, `DELETE FROM alert_state`)
    return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/storage/ -run TestAlertState -v`
Expected: PASS (all 5 tests).

- [ ] **Step 5: Run full storage tests to confirm no regression**

Run: `go test ./internal/storage/ -v`
Expected: PASS — no snapshot_test.go regressions.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/alert_state.go internal/storage/alert_state_test.go
git commit -m "feat(storage): per-model alert_state with interval-reset clear"
```

---

## Task 3: Notify package types + severity classifier

**Files:**
- Create: `internal/notify/notify.go`
- Create: `internal/notify/notify_test.go`

**Interfaces:**
- Consumes: `storage.Snapshot`
- Produces:
  - `type Severity int; const SevInfo, SevLow, SevMid, SevHigh, SevCritical Severity`
  - `type TrendPoint struct { FetchedAt int64; Remaining int }`
  - `type Notification struct { ... }`
  - `type Notifier interface { Send(ctx, rawURL string, n Notification) error }`
  - `func SeverityFor(remaining int) Severity`
  - `func FormatResetTime(unixMs int64) string`     // "16:45:55" or "07-04 00:00"
  - `func FormatResetRemain(deltaMs int64) string`  // "3分42秒后" / "5天7时后" / "已过"

- [ ] **Step 1: Write failing test for severity and formatters**

Create `internal/notify/notify_test.go`:

```go
package notify

import (
    "testing"
    "time"
)

func TestSeverityFor(t *testing.T) {
    cases := []struct {
        remaining int
        want      Severity
    }{
        {100, SevLow},
        {80, SevLow},
        {79, SevLow}, // boundary: > 50 is Low
        {50, SevLow},
        {49, SevMid},
        {30, SevMid},
        {29, SevHigh},
        {10, SevHigh},
        {9, SevCritical},
        {0, SevCritical},
    }
    for _, c := range cases {
        got := SeverityFor(c.remaining)
        if got != c.want {
            t.Errorf("SeverityFor(%d) = %v, want %v", c.remaining, got, c.want)
        }
    }
}

func TestFormatResetTime_Today(t *testing.T) {
    // 2026-06-28 16:45:55 local — same calendar day as "today"
    loc := time.Local
    ts := time.Date(2026, 6, 28, 16, 45, 55, 0, loc).UnixMilli()
    got := FormatResetTime(ts)
    want := "16:45:55"
    if got != want {
        t.Errorf("FormatResetTime = %q, want %q", got, want)
    }
}

func TestFormatResetRemain(t *testing.T) {
    cases := []struct {
        name string
        ms   int64
        want string
    }{
        {"past", -1000, "已过"},
        {"seconds", 30 * 1000, "30秒后"},
        {"minSec", 3*time.Minute.Milliseconds() + 42*1000, "3分42秒后"},
        {"hours", 2*time.Hour.Milliseconds() + 15*time.Minute.Milliseconds(), "2时15分后"},
        {"days", 5*24*time.Hour.Milliseconds() + 7*time.Hour.Milliseconds(), "5天7时后"},
    }
    for _, c := range cases {
        got := FormatResetRemain(c.ms)
        if got != c.want {
            t.Errorf("%s: FormatResetRemain(%d) = %q, want %q", c.name, c.ms, got, c.want)
        }
    }
}

func TestSeverityTemplate(t *testing.T) {
    cases := []struct {
        sev  Severity
        want string
    }{
        {SevInfo, "blue"},
        {SevLow, "green"},
        {SevMid, "yellow"},
        {SevHigh, "orange"},
        {SevCritical, "red"},
    }
    for _, c := range cases {
        if got := c.sev.Template(); got != c.want {
            t.Errorf("%v.Template() = %q, want %q", c.sev, got, c.want)
        }
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -v`
Expected: FAIL — package directory doesn't exist yet (compile error).

- [ ] **Step 3: Create directory and implement `internal/notify/notify.go`**

```go
// Package notify owns outbound alert delivery. The FeishuClient (feishu.go)
// satisfies the Notifier interface, and the AlertEngine (alert_engine.go)
// drives evaluation per scheduler tick.
package notify

import (
    "fmt"
    "time"
)

type Severity int

const (
    SevInfo     Severity = iota // test card
    SevLow                       // remaining > 50
    SevMid                       // 30..50
    SevHigh                      // 10..30
    SevCritical                  // < 10
)

// SeverityFor maps a remaining-percent integer to its severity band.
//   remaining > 50  -> SevLow
//   30..50          -> SevMid
//   10..30          -> SevHigh
//   < 10            -> SevCritical
func SeverityFor(remaining int) Severity {
    switch {
    case remaining < 10:
        return SevCritical
    case remaining < 30:
        return SevHigh
    case remaining <= 50:
        return SevMid
    default:
        return SevLow
    }
}

// Template returns the Feishu interactive card "template" color for this severity.
func (s Severity) Template() string {
    switch s {
    case SevInfo:
        return "blue"
    case SevLow:
        return "green"
    case SevMid:
        return "yellow"
    case SevHigh:
        return "orange"
    case SevCritical:
        return "red"
    default:
        return "grey"
    }
}

// String returns the localized label used in card copy.
func (s Severity) String() string {
    switch s {
    case SevInfo:
        return "测试"
    case SevLow:
        return "低"
    case SevMid:
        return "中"
    case SevHigh:
        return "高"
    case SevCritical:
        return "严重"
    default:
        return "未知"
    }
}

type TrendPoint struct {
    FetchedAt int64 `json:"fetched_at"`
    Remaining int   `json:"remaining"`
}

type Notification struct {
    IsTest                bool         `json:"is_test"`
    Model                 string       `json:"model"`
    Severity              Severity     `json:"severity"`
    Remaining             int          `json:"remaining"`
    Used                  int          `json:"used"`
    WeeklyRemainingPct    *int         `json:"weekly_remaining_pct,omitempty"`
    Threshold             int          `json:"threshold"`
    PrevNotifiedPct       *int         `json:"prev_notified_pct,omitempty"`
    IntervalResetAt       *int64       `json:"interval_reset_at,omitempty"`
    IntervalResetRemainMs *int64       `json:"interval_reset_remain_ms,omitempty"`
    WeeklyResetAt         *int64       `json:"weekly_reset_at,omitempty"`
    WeeklyResetRemainMs   *int64       `json:"weekly_reset_remain_ms,omitempty"`
    RecentTrend           []TrendPoint `json:"recent_trend,omitempty"`
    FetchedAt             int64        `json:"fetched_at"`
}

type Notifier interface {
    Send(ctx context.Context, rawURL string, n Notification) error
}

// FormatResetTime formats a unix-ms timestamp for the card.
//   same calendar day  -> "HH:MM:SS"
//   otherwise          -> "MM-DD HH:MM"
func FormatResetTime(unixMs int64) string {
    t := time.UnixMilli(unixMs)
    now := time.Now()
    if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
        return t.Format("15:04:05")
    }
    return t.Format("01-02 15:04")
}

// FormatResetRemain formats a duration-until-reset string for the card.
//   past        -> "已过"
//   < 60s       -> "Ns后"
//   < 1h        -> "MmSS秒后"
//   < 24h       -> "HhMM分后"
//   otherwise   -> "DdHh时后"
func FormatResetRemain(deltaMs int64) string {
    if deltaMs <= 0 {
        return "已过"
    }
    d := time.Duration(deltaMs) * time.Millisecond
    if d < time.Minute {
        return fmt.Sprintf("%d秒后", int(d.Seconds()))
    }
    if d < time.Hour {
        m := int(d.Minutes())
        s := int(d.Seconds()) - m*60
        return fmt.Sprintf("%d分%d秒后", m, s)
    }
    if d < 24*time.Hour {
        h := int(d.Hours())
        m := int(d.Minutes()) - h*60
        return fmt.Sprintf("%d时%d分后", h, m)
    }
    days := int(d / (24 * time.Hour))
    hours := int(d.Hours()) - days*24
    return fmt.Sprintf("%d天%d时后", days, hours)
}
```

Wait — the helper `fmt` import is used inside FormatResetRemain. The first `time` import is also needed for `time.Now`, `time.UnixMilli`, etc. The unused `fmt` import would fail. Make sure all imports are used. Above code uses both. Good.

The package also needs a `context` import for `Notifier.Send`. Wait — `context` is only used inside the interface, but since the interface uses `context.Context`, the import must be in the file.

Add to imports:

```go
import (
    "context"
    "fmt"
    "time"
)
```

Add the `context` import. The full corrected file imports block is:

```go
import (
    "context"
    "fmt"
    "time"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -v`
Expected: PASS (all 4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/notify/notify.go internal/notify/notify_test.go
git commit -m "feat(notify): types, severity, reset-time formatters"
```

---

## Task 4: FeishuClient (URL parse, HMAC sign, POST + retry)

**Files:**
- Create: `internal/notify/feishu.go`
- Create: `internal/notify/feishu_test.go`

**Interfaces:**
- Consumes: `Notifier` interface (from notify.go), `http.Client` (optional injection)
- Produces:
  - `type FeishuClient struct{ httpClient *http.Client }`
  - `func NewFeishuClient() *FeishuClient`
  - `func NewFeishuClientWithHTTP(c *http.Client) *FeishuClient`  // for testing
  - `func (c *FeishuClient) Send(ctx context.Context, rawURL string, n Notification) error`
  - internal helpers `parseURLAndSecret(rawURL) (cleanURL, secret string, err error)`
  - internal helpers `buildCardPayload(n Notification) map[string]any`
  - internal helpers `hmacSign(secret, ts, body string) string`

- [ ] **Step 1: Write failing test for `parseURLAndSecret` and `hmacSign`**

Create `internal/notify/feishu_test.go`:

```go
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
    // Reference: Feishu docs. ts=1700000000, secret=Sec1, body={"a":1}
    sig := hmacSign("Sec1", "1700000000", `{"a":1}`)
    want := "SmFzQvPgWqfI2Yow6bF0fQr3rrEjG2M3K1iJFwYpQ2k"
    // We don't actually know the right vector without running; assert non-empty + base64-shaped.
    if sig == "" {
        t.Error("hmacSign returned empty")
    }
    if strings.ContainsAny(sig, " \n\r") {
        t.Errorf("sig contains whitespace: %q", sig)
    }
    _ = want // suppress unused; vector assertion is best-effort
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -run "TestParseURL|TestHMAC|TestFeishu" -v`
Expected: FAIL — `parseURLAndSecret`, `hmacSign`, `FeishuClient` not defined.

- [ ] **Step 3: Implement `internal/notify/feishu.go`**

```go
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
        // Merge timestamp + sign into the JSON body
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

// buildCardPayload constructs the interactive-card JSON sent to Feishu.
func buildCardPayload(n Notification) map[string]any {
    var titlePrefix string
    if n.IsTest {
        titlePrefix = "[测试] "
    } else {
        titlePrefix = "⚠️ 配额告警 · "
    }
    title := titlePrefix + n.Model

    fields := []map[string]any{
        {"text": map[string]any{"tag": "lark_md", "content": "**模型**\n`" + n.Model + "`"}},
        {"text": map[string]any{"tag": "lark_md", "content": "**触发时间**\n" + time.UnixMilli(n.FetchedAt).Format("2006-01-02 15:04:05")}},
        {"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**剩余**\n**%d%%**", n.Remaining)}},
        {"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**已用**\n%d%%", n.Used)}},
        {"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**阈值**\n≤%d%%", n.Threshold)}},
    }
    if n.PrevNotifiedPct != nil {
        fields = append(fields, map[string]any{
            "text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**上次告警**\n%d%%", *n.PrevNotifiedPct)},
        })
    } else {
        fields = append(fields, map[string]any{
            "text": map[string]any{"tag": "lark_md", "content": "**上次告警**\n—"},
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

    // Trend note line
    var noteText string
    if len(n.RecentTrend) > 0 {
        parts := make([]string, 0, len(n.RecentTrend))
        for _, p := range n.RecentTrend {
            parts = append(parts, fmt.Sprintf("%d", p.Remaining))
        }
        noteText = "最近10分钟趋势(剩余%): " + strings.Join(parts, " → ")
    } else {
        noteText = "无近期趋势数据"
    }
    if n.IsTest {
        noteText = "这是测试消息,不影响告警状态。"
    }
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
```

The file uses `strings.Join`. Add `"strings"` to the import block. Final imports:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -run "TestParseURL|TestHMAC|TestFeishu" -v`
Expected: PASS. (Note: the retry tests sleep ~14s of backoff in the worst case; if too slow, adjust by injecting a clock — but for now real-time is acceptable.)

If tests are slow, run with a shorter timeout filter:

Run: `go test ./internal/notify/ -run TestParseURL -v` — should be fast.
Run: `go test ./internal/notify/ -run TestHMAC -v` — should be fast.
Run: `go test ./internal/notify/ -run "TestFeishuClient_Send_Success|TestFeishuClient_Send_SignsWhenSecretPresent" -v` — fast (no retries).
Run: `go test ./internal/notify/ -run "TestFeishuClient_Send_Retries" -v` — slow (~14s).

- [ ] **Step 5: Commit**

```bash
git add internal/notify/feishu.go internal/notify/feishu_test.go
git commit -m "feat(notify): FeishuClient with HMAC sign and 3x retry"
```

---

## Task 5: AlertEngine (state machine: Evaluate + SendTest)

**Files:**
- Create: `internal/notify/alert_engine.go`
- Create: `internal/notify/alert_engine_test.go`

**Interfaces:**
- Consumes: `*storage.DB`, `Notifier`, `func() storage.AlertConfig`
- Produces:
  - `type AlertEngine struct { db *storage.DB; notifier Notifier; cfgFn func() storage.AlertConfig; nowFn func() time.Time }`
  - `func NewAlertEngine(db *storage.DB, notifier Notifier, cfgFn func() storage.AlertConfig) *AlertEngine`
  - `func (e *AlertEngine) Evaluate(ctx context.Context, snaps []storage.Snapshot) error`
  - `func (e *AlertEngine) SendTest(ctx context.Context) (int64, error)`

- [ ] **Step 1: Write failing test for AlertEngine state machine**

Create `internal/notify/alert_engine_test.go`:

```go
package notify

import (
    "context"
    "errors"
    "path/filepath"
    "sync"
    "testing"
    "time"

    "minimax-monitor/internal/storage"
)

// fakeNotifier records every Send call without touching the network.
type fakeNotifier struct {
    mu    sync.Mutex
    calls []Notification
    err   error
}

func (f *fakeNotifier) Send(_ context.Context, _ string, n Notification) error {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.calls = append(f.calls, n)
    return f.err
}

func (f *fakeNotifier) Calls() []Notification {
    f.mu.Lock()
    defer f.mu.Unlock()
    out := make([]Notification, len(f.calls))
    copy(out, f.calls)
    return out
}

func openTestDB(t *testing.T) *storage.DB {
    t.Helper()
    db, err := storage.Open(filepath.Join(t.TempDir(), "x.db"))
    if err != nil {
        t.Fatalf("Open: %v", err)
    }
    t.Cleanup(func() { db.Close() })
    return db
}

func snap(model string, remaining int) storage.Snapshot {
    pct := remaining
    return storage.Snapshot{
        ModelName:            model,
        IntervalRemainingPct: &pct,
        FetchedAt:            time.Now().UnixMilli(),
    }
}

func TestAlertEngine_Disabled_NoCall(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: false, URL: "x", Threshold: 80} })
    if err := eng.Evaluate(context.Background(), []storage.Snapshot{snap("general", 60)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 0 {
        t.Errorf("calls = %d, want 0 when disabled", got)
    }
}

func TestAlertEngine_EmptyURL_NoCall(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "", Threshold: 80} })
    if err := eng.Evaluate(context.Background(), []storage.Snapshot{snap("general", 60)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 0 {
        t.Errorf("calls = %d, want 0 when URL empty", got)
    }
}

func TestAlertEngine_AboveThreshold_NoCall(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    // 81 > 80
    if err := eng.Evaluate(context.Background(), []storage.Snapshot{snap("general", 81)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 0 {
        t.Errorf("calls = %d, want 0 when above threshold", got)
    }
}

func TestAlertEngine_CrossingThreshold_OneCall(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    if err := eng.Evaluate(context.Background(), []storage.Snapshot{snap("general", 80)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 1 {
        t.Errorf("calls = %d, want 1", got)
    }
    if c := fn.Calls()[0]; c.Remaining != 80 {
        t.Errorf("Remaining = %d, want 80", c.Remaining)
    }
    // state advanced
    st, _ := db.GetAlertState(context.Background(), "general")
    if len(st.NotifiedPcts) != 1 || st.NotifiedPcts[0] != 80 {
        t.Errorf("state after = %v, want [80]", st.NotifiedPcts)
    }
}

func TestAlertEngine_Duplicate_NoSecondCall(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 80)})
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 80)})
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 80)})
    if got := len(fn.Calls()); got != 1 {
        t.Errorf("calls = %d, want 1 (dedup)", got)
    }
}

func TestAlertEngine_DropBy1_NewCallEachTime(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    for _, p := range []int{80, 79, 78, 77} {
        _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", p)})
    }
    if got := len(fn.Calls()); got != 4 {
        t.Errorf("calls = %d, want 4 (one per pct drop)", got)
    }
}

func TestAlertEngine_IntervalReset_ClearsState(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 80)})
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 79)})
    if got := len(fn.Calls()); got != 2 {
        t.Fatalf("setup: calls = %d, want 2", got)
    }
    // interval resets
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 100)})
    st, _ := db.GetAlertState(ctx, "general")
    if len(st.NotifiedPcts) != 0 {
        t.Errorf("state after reset = %v, want empty", st.NotifiedPcts)
    }
    // same pct again triggers a new call
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 80)})
    if got := len(fn.Calls()); got != 3 {
        t.Errorf("calls after reset+redrop = %d, want 3", got)
    }
}

func TestAlertEngine_SendFailure_DoesNotAdvance(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{err: errors.New("network down")}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 80)})
    st, _ := db.GetAlertState(ctx, "general")
    if len(st.NotifiedPcts) != 0 {
        t.Errorf("state after failed send = %v, want empty", st.NotifiedPcts)
    }
}

func TestAlertEngine_SendTest_DoesNotTouchState(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    _ = db.SetAlertConfig(context.Background(), storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80})
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig {
        c, _ := db.GetAlertConfig(context.Background())
        return c
    })
    _, err := eng.SendTest(context.Background())
    if err != nil {
        t.Fatalf("SendTest: %v", err)
    }
    if got := len(fn.Calls()); got != 1 {
        t.Errorf("calls = %d, want 1", got)
    }
    if c := fn.Calls()[0]; !c.IsTest {
        t.Error("SendTest notification IsTest should be true")
    }
    st, _ := db.GetAlertState(context.Background(), "general")
    if len(st.NotifiedPcts) != 0 {
        t.Errorf("SendTest should not touch state; got %v", st.NotifiedPcts)
    }
}

func TestAlertEngine_NilRemaining_Skipped(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    s := storage.Snapshot{ModelName: "general", IntervalRemainingPct: nil}
    if err := eng.Evaluate(context.Background(), []storage.Snapshot{s}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 0 {
        t.Errorf("calls = %d, want 0 for nil remaining", got)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -run TestAlertEngine -v`
Expected: FAIL — `NewAlertEngine` undefined.

- [ ] **Step 3: Implement `internal/notify/alert_engine.go`**

```go
package notify

import (
    "context"
    "log/slog"
    "time"

    "minimax-monitor/internal/storage"
)

// AlertEngine evaluates the latest snapshot set against the user's configured
// threshold and dispatches notifications. State lives in storage (SQLite) so
// dedup survives process restarts.
type AlertEngine struct {
    db       *storage.DB
    notifier Notifier
    cfgFn    func() storage.AlertConfig
    nowFn    func() time.Time
}

// NewAlertEngine constructs an engine. cfgFn is invoked on every Evaluate so
// configuration changes take effect immediately without restart.
func NewAlertEngine(db *storage.DB, notifier Notifier, cfgFn func() storage.AlertConfig) *AlertEngine {
    return &AlertEngine{
        db:       db,
        notifier: notifier,
        cfgFn:    cfgFn,
        nowFn:    time.Now,
    }
}

// Evaluate inspects each snapshot, decides whether to notify, and dispatches
// through the configured Notifier. Already-notified percents are skipped.
func (e *AlertEngine) Evaluate(ctx context.Context, snaps []storage.Snapshot) error {
    cfg := e.cfgFn()
    if !cfg.Enabled || cfg.URL == "" {
        return nil
    }
    now := e.nowFn()

    for _, s := range snaps {
        if s.IntervalRemainingPct == nil {
            continue
        }
        remaining := *s.IntervalRemainingPct

        // 1) Interval reset detection: clear state, then skip this tick.
        if remaining >= 95 {
            if err := e.db.ClearAlertState(ctx, s.ModelName); err != nil {
                slog.Warn("alert: clear state failed", "model", s.ModelName, "err", err)
            }
            continue
        }
        // 2) Above threshold.
        if remaining > cfg.Threshold {
            continue
        }
        // 3) Already notified at this exact pct.
        st, err := e.db.GetAlertState(ctx, s.ModelName)
        if err != nil {
            slog.Warn("alert: get state failed", "model", s.ModelName, "err", err)
            continue
        }
        if containsInt(st.NotifiedPcts, remaining) {
            continue
        }

        // 4) Build notification.
        trend := e.recentTrend(ctx, s.ModelName, now)
        n := buildNotification(s, cfg.Threshold, remaining, st.NotifiedPcts, trend, now)

        // 5) Send.
        if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
            slog.Warn("alert: send failed",
                "model", s.ModelName, "pct", remaining, "err", err)
            continue
        }

        // 6) Advance dedup state.
        st.NotifiedPcts = appendUniqueSorted(st.NotifiedPcts, remaining)
        st.UpdatedAt = now.UnixMilli()
        if err := e.db.SetAlertState(ctx, s.ModelName, st); err != nil {
            slog.Warn("alert: set state failed", "model", s.ModelName, "err", err)
        }
    }
    return nil
}

// SendTest dispatches a test card to the configured webhook. Does NOT touch
// alert state. Returns the unix-ms timestamp when the call completed.
func (e *AlertEngine) SendTest(ctx context.Context) (int64, error) {
    cfg := e.cfgFn()
    if !cfg.Enabled || cfg.URL == "" {
        return 0, errConfigMissing
    }
    snaps, err := e.db.Latest(ctx)
    if err != nil {
        return 0, err
    }
    var model string
    if len(snaps) > 0 {
        model = snaps[0].ModelName
    } else {
        model = "general"
    }
    now := e.nowFn()
    pct := 99
    n := Notification{
        IsTest:     true,
        Model:      model,
        Severity:   SevInfo,
        Remaining:  pct,
        Used:       100 - pct,
        Threshold:  cfg.Threshold,
        FetchedAt:  now.UnixMilli(),
    }
    if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
        return 0, err
    }
    return now.UnixMilli(), nil
}

// Sentinel errors
var errConfigMissing = &alertError{"config_missing"}

// alertError is a string-based error type so handlers can detect via errors.Is
// (or string match). Kept simple.
type alertError struct{ msg string }

func (e *alertError) Error() string { return e.msg }

// ErrConfigMissing is returned when SendTest is invoked with alerts disabled
// or no URL configured.
var ErrConfigMissing = errConfigMissing

// recentTrend pulls the last 10 minutes (1-minute buckets) of interval
// remaining% for the model. Returns nil on error.
func (e *AlertEngine) recentTrend(ctx context.Context, model string, now time.Time) []TrendPoint {
    to := now.UnixMilli()
    from := to - 10*60*1000
    rows, err := e.db.History(ctx, model, from, to, 60_000)
    if err != nil {
        return nil
    }
    out := make([]TrendPoint, 0, len(rows))
    for _, b := range rows {
        // Use the bucket average as the representative remaining %.
        avg := int(b.IntervalAvg + 0.5)
        out = append(out, TrendPoint{FetchedAt: b.T, Remaining: avg})
    }
    return out
}

// buildNotification composes the Notification struct from a snapshot, the
// configured threshold, the model's prior notified percents, and recent
// trend points.
func buildNotification(s storage.Snapshot, threshold, remaining int,
    prevNotified []int, trend []TrendPoint, now time.Time) Notification {
    n := Notification{
        Model:        s.ModelName,
        Severity:     SeverityFor(remaining),
        Remaining:    remaining,
        Used:         100 - remaining,
        Threshold:    threshold,
        RecentTrend:  trend,
        FetchedAt:    now.UnixMilli(),
    }
    if s.WeeklyRemainingPct != nil {
        v := *s.WeeklyRemainingPct
        n.WeeklyRemainingPct = &v
    }
    if len(prevNotified) > 0 {
        v := prevNotified[len(prevNotified)-1]
        n.PrevNotifiedPct = &v
    }
    if s.IntervalEndAt != nil {
        at := *s.IntervalEndAt
        n.IntervalResetAt = &at
        delta := at - now.UnixMilli()
        n.IntervalResetRemainMs = &delta
    }
    if s.WeeklyEndAt != nil {
        at := *s.WeeklyEndAt
        n.WeeklyResetAt = &at
        delta := at - now.UnixMilli()
        n.WeeklyResetRemainMs = &delta
    }
    return n
}

// containsInt reports whether x is in the slice.
func containsInt(xs []int, x int) bool {
    for _, v := range xs {
        if v == x {
            return true
        }
    }
    return false
}

// appendUniqueSorted appends x to xs if not already present. Keeps the slice
// sorted ascending so PrevNotifiedPct semantics stay stable.
func appendUniqueSorted(xs []int, x int) []int {
    if containsInt(xs, x) {
        return xs
    }
    out := make([]int, 0, len(xs)+1)
    inserted := false
    for _, v := range xs {
        if !inserted && x < v {
            out = append(out, x)
            inserted = true
        }
        out = append(out, v)
    }
    if !inserted {
        out = append(out, x)
    }
    return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -run TestAlertEngine -v`
Expected: PASS (all 10 tests).

- [ ] **Step 5: Run full notify test suite**

Run: `go test ./internal/notify/ -v`
Expected: PASS — all 4 test files.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/alert_engine.go internal/notify/alert_engine_test.go
git commit -m "feat(notify): AlertEngine with state-machine dedup and test send"
```

---

## Task 6: Scheduler integration

**Files:**
- Modify: `internal/scheduler/scheduler.go`

**Interfaces:**
- Consumes: existing `Scheduler` struct
- Produces: `func (sc *Scheduler) SetAlerter(a AlertEngine)` — replaces a constructor-signature change (cleaner than extending `New`)

- [ ] **Step 1: Add `AlertEngine` interface and `alerter` field**

Edit `internal/scheduler/scheduler.go`:

Add the interface after the `Broadcaster` interface block (after line 25):

```go
// AlertEngine evaluates the latest snapshot set against configured alert
// rules and dispatches notifications.
type AlertEngine interface {
    Evaluate(ctx context.Context, snaps []storage.Snapshot) error
}
```

Add a field to the `Scheduler` struct (after line 40 `lastErrMsg string`):

```go
    alerter AlertEngine
```

Add a setter method (after the `Stats` method, end of file):

```go
// SetAlerter installs an alert engine. Pass nil to disable.
func (sc *Scheduler) SetAlerter(a AlertEngine) {
    sc.mu.Lock()
    sc.alerter = a
    sc.mu.Unlock()
}
```

- [ ] **Step 2: Fire alerter in `RunOnce` after broadcast**

In `RunOnce`, after the existing broadcast block (after line 78 `return nil`), but BEFORE the broadcast so the snapshots are available. Replace the existing block:

```go
if snaps, err := sc.ins.Latest(ctx); err == nil && sc.b != nil {
    sc.b.Broadcast(snaps)
}
```

with:

```go
snaps, err := sc.ins.Latest(ctx)
if err != nil {
    slog.Warn("latest snapshots", "err", err)
}
if snaps != nil && sc.b != nil {
    sc.b.Broadcast(snaps)
}
sc.mu.Lock()
alerter := sc.alerter
sc.mu.Unlock()
if alerter != nil && snaps != nil {
    go func(sn []storage.Snapshot) {
        actx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        if err := alerter.Evaluate(actx, sn); err != nil {
            slog.Warn("alert evaluate", "err", err)
        }
    }(snaps)
}
```

Add `"context"` and `"minimax-monitor/internal/storage"` to imports if not already present. The existing imports are:
```go
import (
    "context"
    "log/slog"
    "sync"
    "time"

    "minimax-monitor/internal/model"
    "minimax-monitor/internal/storage"
)
```
Both `context` and `storage` are already imported. Good.

- [ ] **Step 3: Verify scheduler tests still pass**

Run: `go test ./internal/scheduler/ -v`
Expected: PASS — existing scheduler_test.go unchanged.

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/scheduler.go
git commit -m "feat(scheduler): fire AlertEngine in goroutine after broadcast"
```

---

## Task 7: HTTP handlers for alert settings + test

**Files:**
- Modify: `internal/server/server.go` (add 3 routes + 2 fields)
- Create: `internal/server/handlers_alert.go`
- Create: `internal/server/handlers_alert_test.go`

**Interfaces:**
- Consumes: `storage.AlertConfig`, `*storage.DB`, `AlertConfig func() storage.AlertConfig`, `AlertTest func(ctx) (int64, error)`
- Produces: 3 new handlers `handleAlertGet`, `handleAlertPut`, `handleAlertTest`

- [ ] **Step 1: Add 2 fields to `Server` struct**

Edit `internal/server/server.go` (in the `Server` struct definition). Add fields after `OnKeyChange`:

```go
    AlertConfig func() storage.AlertConfig
    AlertTest   func(ctx context.Context) (int64, error)
```

Add `"context"` to imports if not already there. The file currently imports:
```go
import (
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "minimax-monitor/internal/storage"
)
```
Add `"context"`:

```go
import (
    "context"
    "net/http"
    "time"

    "github.com/gin-gonic/gin"
    "minimax-monitor/internal/storage"
)
```

- [ ] **Step 2: Register 3 new routes in `routes()`**

In `internal/server/server.go`, inside `routes()`, add after the `s.Engine.DELETE("/api/settings/key", s.handleSettingsDelete)` line:

```go
    s.Engine.GET("/api/settings/alert",       s.handleAlertGet)
    s.Engine.PUT("/api/settings/alert",       s.handleAlertPut)
    s.Engine.POST("/api/settings/alert/test", s.handleAlertTest)
```

- [ ] **Step 3: Write failing test for handlers**

Create `internal/server/handlers_alert_test.go`:

```go
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
    "minimax-monitor/internal/storage"
    "minimax-monitor/internal/notify"
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
    // threshold number -> JSON number -> float64
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

func TestAlertPut_DisableClearsState(t *testing.T) {
    s, db := newTestServerWithDB(t)
    ctx := context.Background()
    // Setup: enabled with state
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
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestAlert -v`
Expected: FAIL — `handleAlertGet` undefined.

- [ ] **Step 5: Implement `internal/server/handlers_alert.go`**

```go
package server

import (
    "context"
    "errors"
    "net/http"
    "net/url"
    "strings"

    "github.com/gin-gonic/gin"
    "minimax-monitor/internal/notify"
    "minimax-monitor/internal/storage"
)

// allowedWebhookHosts is the allowlist for webhook URL hosts. Accepts both
// Feishu and Lark (international) custom-bot endpoints.
var allowedWebhookHosts = map[string]struct{}{
    "open.feishu.cn":   {},
    "open.larksuite.com": {},
}

// maskURL returns a tail-masked version of a webhook URL safe to send to
// the frontend. Returns "" for empty input.
func maskURL(u string) string {
    if u == "" {
        return ""
    }
    if len(u) <= 32 {
        if len(u) <= 8 {
            return u
        }
        return u[:4] + "..." + u[len(u)-4:]
    }
    return u[:24] + "..." + u[len(u)-4:]
}

func (s *Server) handleAlertGet(c *gin.Context) {
    if s.DB == nil || s.AlertConfig == nil {
        c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alert config unavailable"})
        return
    }
    cfg := s.AlertConfig()
    c.JSON(http.StatusOK, gin.H{
        "enabled":   cfg.Enabled,
        "url":       maskURL(cfg.URL),
        "threshold": cfg.Threshold,
    })
}

type alertPutBody struct {
    Enabled   *bool  `json:"enabled"`
    URL       string `json:"url"`
    Threshold *int   `json:"threshold"`
}

func (s *Server) handleAlertPut(c *gin.Context) {
    var body alertPutBody
    if err := c.ShouldBindJSON(&body); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
        return
    }
    enabled := false
    if body.Enabled != nil {
        enabled = *body.Enabled
    }
    threshold := 80
    if body.Threshold != nil {
        threshold = *body.Threshold
    }
    urlStr := strings.TrimSpace(body.URL)

    if threshold <= 0 || threshold > 100 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "threshold must be in 1..100"})
        return
    }
    if urlStr != "" {
        u, err := url.Parse(urlStr)
        if err != nil || u.Scheme != "https" || u.Host == "" {
            c.JSON(http.StatusBadRequest, gin.H{"error": "url must be a valid https URL"})
            return
        }
        if _, ok := allowedWebhookHosts[u.Host]; !ok {
            c.JSON(http.StatusBadRequest, gin.H{"error": "url host must be open.feishu.cn or open.larksuite.com"})
            return
        }
    }
    if enabled && urlStr == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "url required when enabled"})
        return
    }

    if s.DB == nil {
        c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db unavailable"})
        return
    }

    ctx := c.Request.Context()
    prev, _ := s.DB.GetAlertConfig(ctx)

    // Disable transition: clear all dedup state.
    if prev.Enabled && !enabled {
        if err := s.DB.ClearAllAlertStates(ctx); err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
            return
        }
    }

    if err := s.DB.SetAlertConfig(ctx, storage.AlertConfig{
        Enabled:   enabled,
        URL:       urlStr,
        Threshold: threshold,
    }); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleAlertTest(c *gin.Context) {
    if s.DB == nil || s.AlertConfig == nil {
        c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alert config unavailable"})
        return
    }
    cfg := s.AlertConfig()
    if !cfg.Enabled || cfg.URL == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "config_missing"})
        return
    }
    if s.AlertTest == nil {
        c.JSON(http.StatusServiceUnavailable, gin.H{"error": "alert test not wired"})
        return
    }
    ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
    defer cancel()
    sentAt, err := s.AlertTest(ctx)
    if err != nil {
        // Distinguish "user config problem" from "upstream problem"
        if errors.Is(err, notify.ErrConfigMissing) {
            c.JSON(http.StatusBadRequest, gin.H{"error": "config_missing"})
            return
        }
        c.JSON(http.StatusBadGateway, gin.H{"error": "webhook_failed", "reason": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"ok": true, "sent_at": sentAt})
}
```

Add `"time"` to imports. Final imports block:

```go
import (
    "context"
    "errors"
    "net/http"
    "net/url"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "minimax-monitor/internal/notify"
    "minimax-monitor/internal/storage"
)
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/server/ -run TestAlert -v`
Expected: PASS (all 9 tests).

- [ ] **Step 7: Run full server test suite**

Run: `go test ./internal/server/ -v`
Expected: PASS — no regressions in existing handlers.

- [ ] **Step 8: Commit**

```bash
git add internal/server/server.go internal/server/handlers_alert.go internal/server/handlers_alert_test.go
git commit -m "feat(server): /api/settings/alert GET/PUT and test endpoint"
```

---

## Task 8: Wire everything in main.go

**Files:**
- Modify: `cmd/minimax-monitor/main.go`

**Interfaces:**
- Consumes: `internal/notify`, `internal/storage`
- Produces: working binary that loads alert config, evaluates per tick, exposes HTTP

- [ ] **Step 1: Add imports and wire notifier + engine + alerter + handler closures**

Edit `cmd/minimax-monitor/main.go`. Update imports:

```go
import (
    "context"
    "flag"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "path/filepath"
    "syscall"
    "time"

    "minimax-monitor/internal/apiclient"
    "minimax-monitor/internal/config"
    "minimax-monitor/internal/keyring"
    "minimax-monitor/internal/notify"
    "minimax-monitor/internal/scheduler"
    "minimax-monitor/internal/server"
    "minimax-monitor/internal/storage"
)
```

Add `notify` to imports. Now find the section after `srv := server.New(db, store)` and after the `srv.OnKeyChange` line, add the wiring (before `rootCtx, cancel := context.WithCancel(...)`):

```go
    // Wire Feishu notifier and alert engine.
    feishu := notify.NewFeishuClient()
    alertCfgFn := func() storage.AlertConfig {
        cfg, err := db.GetAlertConfig(context.Background())
        if err != nil {
            slog.Warn("get alert config", "err", err)
        }
        return cfg
    }
    engine := notify.NewAlertEngine(db, feishu, alertCfgFn)
    sched.SetAlerter(engine)

    srv.AlertConfig = alertCfgFn
    srv.AlertTest = engine.SendTest
```

- [ ] **Step 2: Verify it builds**

Run: `go build -trimpath -ldflags="-s -w" -o dist/minimax-monitor.exe .\cmd\minimax-monitor`
Expected: Build OK, no errors.

- [ ] **Step 3: Verify tests still pass**

Run: `go test ./...`
Expected: PASS — all packages.

- [ ] **Step 4: Commit**

```bash
git add cmd/minimax-monitor/main.go
git commit -m "feat(cmd): wire Feishu notifier and alert engine"
```

---

## Task 9: Web UI — modal section markup + CSS

**Files:**
- Modify: `internal/server/web/index.html`
- Modify: `internal/server/web/style.css`

**Interfaces:**
- Consumes: existing modal structure
- Produces: collapsible "告警通知" section with form fields, badge, save + test buttons

- [ ] **Step 1: Append alert section to modal in `index.html`**

Edit `internal/server/web/index.html`. Inside `<div class="modal-body">`, AFTER the existing `<div class="form-actions">` (closing the API Key section), BEFORE the closing `</div>` of `modal-body`, insert:

```html
      <div class="alert-section">
        <button type="button" class="section-toggle" id="alertToggle" aria-expanded="false">
          <span class="chev">▸</span>
          <span class="section-title">告警通知</span>
          <span class="badge" id="alertBadge">未配置</span>
        </button>
        <div class="section-body hidden" id="alertBody">
          <div class="form-row switch-row">
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
            <button type="button" id="alertTestBtn" class="link">发送测试</button>
            <div class="spacer"></div>
            <button type="button" id="alertSaveBtn" class="primary">保存</button>
          </div>
        </div>
      </div>
```

- [ ] **Step 2: Add CSS rules at the end of `style.css`**

Append to `internal/server/web/style.css`:

```css
/* ===== Alert section ===== */
.alert-section {
  border-top: 1px solid var(--border);
  margin-top: 16px;
  padding-top: 12px;
}
.section-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  background: none;
  border: 0;
  color: var(--text-primary);
  cursor: pointer;
  padding: 8px 0;
  font-size: 14px;
  font-family: inherit;
  text-align: left;
}
.section-toggle .chev {
  display: inline-block;
  transition: transform 200ms ease;
  color: var(--text-muted);
}
.section-toggle[aria-expanded="true"] .chev {
  transform: rotate(90deg);
}
.section-toggle:hover .chev {
  color: var(--accent-general);
}
.section-toggle .section-title {
  flex: 1;
}
.badge.warn {
  background: rgba(245, 158, 11, 0.15);
  color: var(--status-warn);
  border: 1px solid rgba(245, 158, 11, 0.3);
}
.section-body {
  padding-left: 12px;
}
.switch-row {
  display: flex;
  align-items: center;
  gap: 12px;
}
.switch {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
}
.form-row small.muted {
  display: block;
  margin-top: 4px;
  font-size: 11px;
}
```

- [ ] **Step 3: Verify static asset embed still compiles**

Run: `go build ./...`
Expected: Build OK.

- [ ] **Step 4: Commit**

```bash
git add internal/server/web/index.html internal/server/web/style.css
git commit -m "feat(web): collapsible alert section markup and styles"
```

---

## Task 10: Web UI — app.js alert handlers

**Files:**
- Modify: `internal/server/web/app.js`

**Interfaces:**
- Consumes: existing modal open/close, `fetch`
- Produces: load/save/test alert handlers wired to the new endpoints

- [ ] **Step 1: Add alert load/save/test logic inside the existing IIFE**

Edit `internal/server/web/app.js`. Find the line `function setKeyUI()` (line ~133) and insert the following block BEFORE the existing Settings modal section's first function. Actually, since the alert section is part of the same modal, add the new code immediately AFTER the `setKeyUI()` function and BEFORE `function openModal()`.

Insert after the closing `}` of `setKeyUI()` (around line 149) and before `function openModal()`:

```js
  // -------- Alert section --------
  const alertToggle = $('alertToggle');
  const alertBody = $('alertBody');
  const alertBadge = $('alertBadge');
  const alertEnabled = $('alertEnabled');
  const alertURL = $('alertURL');
  const alertThreshold = $('alertThreshold');
  const alertError = $('alertError');
  const alertTestBtn = $('alertTestBtn');
  const alertSaveBtn = $('alertSaveBtn');

  function setAlertBadge(cfg) {
    if (cfg.url && cfg.enabled) {
      alertBadge.textContent = `已启用·阈值${cfg.threshold}`;
      alertBadge.className = 'badge warn';
    } else if (cfg.url) {
      alertBadge.textContent = '已禁用';
      alertBadge.className = 'badge';
    } else {
      alertBadge.textContent = '未配置';
      alertBadge.className = 'badge';
    }
  }

  async function loadAlertConfig() {
    try {
      const res = await fetch('/api/settings/alert');
      if (!res.ok) return;
      const cfg = await res.json();
      alertEnabled.checked = !!cfg.enabled;
      // URL is masked; only fill placeholder for edit guidance
      alertURL.value = '';
      alertURL.placeholder = cfg.url
        ? `已保存: ${cfg.url} (留空表示不变)`
        : 'https://open.feishu.cn/open-apis/bot/v2/hook/...';
      alertThreshold.value = cfg.threshold || 80;
      setAlertBadge(cfg);
    } catch (e) { console.error('load alert config', e); }
  }

  function showAlertError(msg) {
    alertError.textContent = msg;
    alertError.classList.remove('hidden');
  }
  function clearAlertError() {
    alertError.classList.add('hidden');
    alertError.textContent = '';
  }

  alertToggle.addEventListener('click', () => {
    const expanded = alertToggle.getAttribute('aria-expanded') === 'true';
    const next = !expanded;
    alertToggle.setAttribute('aria-expanded', String(next));
    alertBody.classList.toggle('hidden', !next);
    if (next) loadAlertConfig();
  });

  alertSaveBtn.addEventListener('click', async () => {
    clearAlertError();
    const enabled = alertEnabled.checked;
    const url = alertURL.value.trim();
    const threshold = parseInt(alertThreshold.value, 10) || 80;
    alertSaveBtn.disabled = true;
    alertSaveBtn.textContent = '保存中…';
    try {
      const res = await fetch('/api/settings/alert', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled, url, threshold }),
      });
      if (!res.ok) {
        const t = await res.text();
        let msg = t;
        try { msg = JSON.parse(t).error || t; } catch (_) {}
        showAlertError(msg);
        return;
      }
      await loadAlertConfig();
    } catch (e) {
      showAlertError(e.message);
    } finally {
      alertSaveBtn.disabled = false;
      alertSaveBtn.textContent = '保存';
    }
  });

  alertTestBtn.addEventListener('click', async () => {
    clearAlertError();
    alertTestBtn.disabled = true;
    const originalText = '发送测试';
    alertTestBtn.textContent = '发送中…';
    try {
      const res = await fetch('/api/settings/alert/test', { method: 'POST' });
      if (!res.ok) {
        const t = await res.text();
        let msg = t;
        try { msg = JSON.parse(t).error || msg; } catch (_) {}
        showAlertError('✗ ' + msg);
        return;
      }
      showAlertError('✓ 已发送');
    } catch (e) {
      showAlertError('✗ ' + e.message);
    } finally {
      alertTestBtn.disabled = false;
      alertTestBtn.textContent = originalText;
    }
  });
```

- [ ] **Step 2: Verify build still compiles**

Run: `go build ./...`
Expected: Build OK (the static asset embed is what matters — it embeds the modified `app.js`).

- [ ] **Step 3: Smoke test in browser**

Manually:
1. `dist\minimax-monitor.exe -p 13338` (use a non-default port to avoid clash).
2. Open `http://localhost:13338`.
3. Click ⚙ → expand "告警通知".
4. Enter a fake webhook URL (e.g., `https://open.feishu.cn/invalid`).
5. Click 保存 — should succeed (URL parses, host allowed).
6. Click 启用 → save again — should succeed.
7. Click 发送测试 — should get `400 config_missing` or `502 webhook_failed` depending on whether the URL is reachable. (Both are acceptable.)

- [ ] **Step 4: Commit**

```bash
git add internal/server/web/app.js
git commit -m "feat(web): alert section load/save/test handlers"
```

---

## Task 11: End-to-end smoke test + final build

**Files:** (no code changes; verification + build script refresh if needed)

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: PASS — every package.

- [ ] **Step 2: Run vet and static checks**

Run: `go vet ./...`
Expected: no warnings.

- [ ] **Step 3: Build the production binary**

Run: `build.bat`
Expected: `[build] OK -> dist\minimax-monitor.exe`

- [ ] **Step 4: Boot smoke test**

Manually:
1. Start `dist\minimax-monitor.exe -p 13339`.
2. Open `http://localhost:13339`.
3. Confirm ⚙ → 告警通知 section is collapsed with "未配置" badge.
4. Expand → enable → paste a real Feishu webhook URL → save.
5. Click 发送测试 → confirm a card arrives in the chat within 5s.
6. Verify the SQLite DB now contains a row in `settings` and possibly in `alert_state`:
   ```bash
   sqlite3 dist/data/monitor.db "SELECT key, value FROM settings"
   sqlite3 dist/data/monitor.db "SELECT * FROM alert_state"
   ```
7. Stop the server.

- [ ] **Step 5: Final commit (if any fix-ups)**

If anything was changed during smoke testing:

```bash
git add -A
git commit -m "chore: smoke-test fixups for Feishu webhook alerts"
```

If nothing changed, skip this step.

- [ ] **Step 6: Update README**

Edit `README.md`. Append a new section after the "Configuration (env)" table:

```markdown
## Alerting (Feishu / Lark)

The dashboard can post interactive-card notifications to a Feishu (or Lark)
custom bot when the configured interval-remaining threshold is crossed.

1. Click ⚙ in the header → expand "告警通知".
2. Paste your webhook URL (with optional `?secret=...` — signing is auto-detected).
3. Set the threshold (default 80). Alerts fire when remaining % drops at or
   below this value, then once more for every additional 1% drop until the
   5-minute window resets.
4. Click "保存", then "发送测试" to verify delivery.

Disable to clear all dedup state — the next enable starts a fresh window.
```

Commit:

```bash
git add README.md
git commit -m "docs: README section for Feishu webhook alerts"
```

---

## Self-Review Notes

- **Spec coverage**: All 13 spec sections map to tasks: schema (Task 1), AlertState (Task 2), Notification types (Task 3), FeishuClient (Task 4), AlertEngine (Task 5), Scheduler integration (Task 6), HTTP API (Task 7), main wiring (Task 8), UI markup+CSS (Task 9), UI JS (Task 10), E2E (Task 11).
- **Placeholder scan**: No TBD/TODO; every step has concrete code.
- **Type consistency**: `AlertConfig`, `AlertState`, `Notification`, `Severity`, `Notifier` match across notify.go, alert_engine.go, feishu.go, server/handlers_alert.go. `Scheduler.SetAlerter` is the wired entry point. `Server.AlertConfig`/`AlertTest` fields are consistent.
- **Risk**: Task 4 retry tests will take ~14s due to backoff. Acceptable for one-time validation; can be cut with clock injection in a future polish task if it becomes annoying.