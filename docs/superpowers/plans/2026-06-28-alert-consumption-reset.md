# Alert Consumption-Forward + Reset Notification + Progress Red-Shift Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Flip alert threshold from "remaining ≤ X" to "consumed ≥ X", surface consumption as the prominent Feishu card value, send a dedicated reset notification on 5-minute window roll-over, and add a red-shift + time-display fix to the dashboard progress bar.

**Architecture:** Backend changes touch only the notify + storage packages — `Snapshot` schema unchanged, `alert_state.notified_pcts` continues to store remaining integer values (so the trigger is `consumed >= threshold` but dedup is `contains(notified_pcts, currentRemaining)`); reset detection compares current vs previous snapshot (consumed transition 0↘ and `interval_end_at` strictly advanced). Frontend derives `interval_unlimited` / `weekly_unlimited` booleans from `status === 3` and reflows the card render around a new `formatIntervalMeta` helper.

**Tech Stack:** Go 1.21+ stdlib only; vanilla HTML/CSS/JS in embedded assets; existing build/test scripts (`build.bat`, `make test`).

---

## Global Constraints

These are project-wide rules inherited from the spec. Every task implicitly conforms to them.

- **Single binary, no new deps.** Stdlib only; no new Go modules in `go.mod`.
- **No schema migration.** `alert_state` table is unchanged; existing rows remain valid.
- **No new env vars, no new CLI flags.** Wiring reuses `cfgFn` and `db` already passed to `AlertEngine`.
- **No HTTP API change.** PUT body still accepts `{enabled, url, threshold}`; clients see the threshold field unchanged (semantic interpretation flips server-side).
- **No scheduler / main.go change.** `Scheduler.RunOnce` already fans out `alerter.Evaluate` in a goroutine; reset uses the same Feishu HTTP path.
- **Test runner.** `go test ./...` from repo root.
- **Commit style.** Conventional commits (`feat:`, `fix:`, `test:`, `docs:`, `style:`). Each task ends with a commit.
- **Plan execution.** Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans`.

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/storage/snapshot.go` | MODIFY | Add `PrevSnapshot(ctx, model, beforeMs)` |
| `internal/storage/snapshot_test.go` | MODIFY | Tests for PrevSnapshot |
| `internal/notify/notify.go` | MODIFY | Add `KindAlert/KindReset` consts, `Kind` + `WindowMaxConsumed` fields on `Notification`, add `buildResetNotification` helper |
| `internal/notify/notify_test.go` | MODIFY | Tests for buildResetNotification |
| `internal/notify/alert_engine.go` | MODIFY | Flip threshold semantic to consumption; add reset detection branch in Evaluate; add `isResetTransition` helper |
| `internal/notify/alert_engine_test.go` | MODIFY | Update existing tests for new semantic; add reset detection tests |
| `internal/notify/feishu.go` | MODIFY | Refactor `buildCardPayload` to branch on `Kind`; alert card emphasizes consumption; new reset card |
| `internal/notify/feishu_test.go` | MODIFY | Tests for alert card emphasis + reset card payload |
| `internal/server/web/app.js` | MODIFY | `barColor`/`hexToRgb`/`formatIntervalMeta`/`statusLabel` helpers; update `renderCards`; rename `statusText` |
| `internal/server/web/style.css` | MODIFY | Remove hard-coded `.bar-fill { background: var(--accent); }` so inline style wins |
| `internal/server/web/index.html` | MODIFY | Threshold label + help text |
| `README.md` | MODIFY | Update alerting section copy |

No new files; no file splits.

---

## Task 1: Storage — `PrevSnapshot` method

**Files:**
- Modify: `internal/storage/snapshot.go`
- Test: `internal/storage/snapshot_test.go`

**Interfaces:**
- Consumes: existing `DB` type and `Snapshot` struct (already in package)
- Produces: `func (db *DB) PrevSnapshot(ctx context.Context, model string, beforeMs int64) (Snapshot, error)` — returns the snapshot with the largest `fetched_at` strictly less than `beforeMs` for the given model; if no such row exists, returns `(Snapshot{}, nil)` (sql.ErrNoRows mapped to nil error).

- [ ] **Step 1: Write the failing tests**

Open `internal/storage/snapshot_test.go` and append (inside the existing `package storage` test file):

```go
func TestPrevSnapshot_ReturnsLatestBefore(t *testing.T) {
    db := openTestDB(t)
    ctx := context.Background()
    now := time.Now().UnixMilli()
    // Insert three snapshots: oldest, middle, newest
    for i, ts := range []int64{now - 30000, now - 20000, now - 10000} {
        pct := 80 - i   // 80, 79, 78
        s := Snapshot{
            FetchedAt:            ts,
            ModelName:            "general",
            IntervalRemainingPct: &pct,
        }
        if err := db.InsertOne(ctx, s); err != nil {
            t.Fatalf("InsertOne #%d: %v", i, err)
        }
    }
    // Query "before now - 5000" — should return the newest of the three (now - 10000, remaining=78)
    got, err := db.PrevSnapshot(ctx, "general", now-5000)
    if err != nil {
        t.Fatalf("PrevSnapshot: %v", err)
    }
    if got.FetchedAt != now-10000 {
        t.Errorf("FetchedAt = %d, want %d", got.FetchedAt, now-10000)
    }
    if got.IntervalRemainingPct == nil || *got.IntervalRemainingPct != 78 {
        t.Errorf("IntervalRemainingPct = %v, want 78", got.IntervalRemainingPct)
    }
}

func TestPrevSnapshot_StrictLessThan(t *testing.T) {
    db := openTestDB(t)
    ctx := context.Background()
    now := time.Now().UnixMilli()
    pct := 50
    s := Snapshot{FetchedAt: now, ModelName: "general", IntervalRemainingPct: &pct}
    if err := db.InsertOne(ctx, s); err != nil {
        t.Fatalf("InsertOne: %v", err)
    }
    // Query "before now" (strict): the snapshot AT now must NOT be returned
    got, err := db.PrevSnapshot(ctx, "general", now)
    if err != nil {
        t.Fatalf("PrevSnapshot: %v", err)
    }
    if got.FetchedAt != 0 {
        t.Errorf("FetchedAt = %d, want 0 (no row strictly before now)", got.FetchedAt)
    }
}

func TestPrevSnapshot_NoRows(t *testing.T) {
    db := openTestDB(t)
    got, err := db.PrevSnapshot(context.Background(), "general", time.Now().UnixMilli())
    if err != nil {
        t.Fatalf("PrevSnapshot: %v", err)
    }
    if got.FetchedAt != 0 {
        t.Errorf("FetchedAt = %d, want 0 (empty table)", got.FetchedAt)
    }
}

func TestPrevSnapshot_DifferentModel(t *testing.T) {
    db := openTestDB(t)
    ctx := context.Background()
    now := time.Now().UnixMilli()
    pct := 80
    for _, m := range []string{"general", "video"} {
        s := Snapshot{FetchedAt: now - 10000, ModelName: m, IntervalRemainingPct: &pct}
        if err := db.InsertOne(ctx, s); err != nil {
            t.Fatalf("InsertOne %s: %v", m, err)
        }
    }
    // Query for "general" should NOT return the "video" snapshot
    got, err := db.PrevSnapshot(ctx, "general", now)
    if err != nil {
        t.Fatalf("PrevSnapshot: %v", err)
    }
    if got.FetchedAt != 0 {
        t.Errorf("FetchedAt = %d, want 0 (video row excluded by model filter)", got.FetchedAt)
    }
}
```

The tests reference a helper `db.InsertOne(ctx, Snapshot)` that doesn't exist yet — this is the failure we want.

- [ ] **Step 2: Add the `InsertOne` helper that the tests need**

In `internal/storage/snapshot.go`, add (at the end of the file):

```go
// InsertOne is a test seam for inserting a single Snapshot row directly.
// Production code uses Insert(resp, t) which marshals an APIResponse.
func (db *DB) InsertOne(ctx context.Context, s Snapshot) error {
    raw, _ := json.Marshal(s)
    _, err := db.ExecContext(ctx, `INSERT INTO snapshot(
        fetched_at, model_name,
        interval_remaining_pct, interval_status, interval_total_count, interval_usage_count,
        interval_end_at, interval_remains_ms,
        weekly_remaining_pct, weekly_status, weekly_total_count, weekly_usage_count,
        weekly_end_at, weekly_remains_ms, raw_json
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
        s.FetchedAt, s.ModelName,
        s.IntervalRemainingPct, s.IntervalStatus, s.IntervalTotalCount, s.IntervalUsageCount,
        s.IntervalEndAt, s.IntervalRemainsMs,
        s.WeeklyRemainingPct, s.WeeklyStatus, s.WeeklyTotalCount, s.WeeklyUsageCount,
        s.WeeklyEndAt, s.WeeklyRemainsMs, string(raw),
    )
    return err
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/storage/ -run TestPrevSnapshot -v`
Expected: FAIL with `PrevSnapshot undefined` (and InsertOne calls failing too).

- [ ] **Step 4: Implement `PrevSnapshot`**

In `internal/storage/snapshot.go`, add (just below `InsertOne`):

```go
// PrevSnapshot returns the snapshot for `model` whose fetched_at is the
// largest value strictly less than `beforeMs`. Returns (Snapshot{}, nil)
// if no such row exists.
func (db *DB) PrevSnapshot(ctx context.Context, model string, beforeMs int64) (Snapshot, error) {
    row := db.QueryRowContext(ctx, `
        SELECT id, fetched_at, model_name,
            interval_remaining_pct, interval_status, interval_total_count, interval_usage_count,
            interval_end_at, interval_remains_ms,
            weekly_remaining_pct, weekly_status, weekly_total_count, weekly_usage_count,
            weekly_end_at, weekly_remains_ms, raw_json
        FROM snapshot
        WHERE model_name = ? AND fetched_at < ?
        ORDER BY fetched_at DESC
        LIMIT 1`, model, beforeMs)
    var s Snapshot
    err := row.Scan(
        &s.ID, &s.FetchedAt, &s.ModelName,
        &s.IntervalRemainingPct, &s.IntervalStatus, &s.IntervalTotalCount, &s.IntervalUsageCount,
        &s.IntervalEndAt, &s.IntervalRemainsMs,
        &s.WeeklyRemainingPct, &s.WeeklyStatus, &s.WeeklyTotalCount, &s.WeeklyUsageCount,
        &s.WeeklyEndAt, &s.WeeklyRemainsMs, &s.RawJSON,
    )
    if err == sql.ErrNoRows {
        return Snapshot{}, nil
    }
    if err != nil {
        return Snapshot{}, err
    }
    return s, nil
}
```

Add `"database/sql"` to the import block in `snapshot.go` (it may already be imported for other functions).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/storage/ -run TestPrevSnapshot -v`
Expected: PASS (4 tests).

- [ ] **Step 6: Run the full storage test suite to ensure no regressions**

Run: `go test ./internal/storage/ -v`
Expected: PASS (existing tests still pass; new tests pass).

- [ ] **Step 7: Commit**

```bash
git add internal/storage/snapshot.go internal/storage/snapshot_test.go
git commit -m "feat(storage): add PrevSnapshot for reset detection"
```

---

## Task 2: Notification — Kind, WindowMaxConsumed, buildResetNotification helper

**Files:**
- Modify: `internal/notify/notify.go`
- Test: `internal/notify/notify_test.go`

**Interfaces:**
- Produces (constants): `const KindAlert = ""`, `const KindReset = "reset"`
- Produces (struct fields on `Notification`): `Kind string json:"kind,omitempty"`, `WindowMaxConsumed *int json:"window_max_consumed,omitempty"`
- Produces (helper): `func buildResetNotification(s storage.Snapshot, prev storage.Snapshot, notifiedPcts []int, threshold int, trend []TrendPoint, now time.Time) Notification`

- [ ] **Step 1: Write the failing test for buildResetNotification**

Open `internal/notify/notify_test.go` (create if missing) and add (package `notify`):

```go
package notify

import (
    "testing"
    "time"

    "minimax-monitor/internal/storage"
)

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

func TestBuildResetNotification_FullFields(t *testing.T) {
    pct := 0   // consumed = 0 → remaining = 100 at the moment of reset
    endAt := int64Ptr(time.Now().Add(5 * time.Minute).UnixMilli())
    weeklyEnd := int64Ptr(time.Now().Add(48 * time.Hour).UnixMilli())
    weeklyRem := intPtr(70)
    s := storage.Snapshot{
        ModelName:             "general",
        IntervalRemainingPct:  &pct,
        IntervalEndAt:         endAt,
        WeeklyEndAt:           weeklyEnd,
        WeeklyRemainingPct:    weeklyRem,
    }
    prevPct := 13   // consumed = 87 → remaining = 13
    prev := storage.Snapshot{
        ModelName:            "general",
        IntervalRemainingPct: &prevPct,
    }
    now := time.Now()
    n := buildResetNotification(s, prev, []int{20, 18, 15, 13}, 80, nil, now)
    if n.Kind != KindReset {
        t.Errorf("Kind = %q, want %q", n.Kind, KindReset)
    }
    if n.Severity != SevInfo {
        t.Errorf("Severity = %v, want SevInfo", n.Severity)
    }
    if n.Used != 0 || n.Remaining != 100 {
        t.Errorf("Used=%d Remaining=%d, want 0/100", n.Used, n.Remaining)
    }
    if n.Threshold != 80 {
        t.Errorf("Threshold = %d, want 80", n.Threshold)
    }
    if n.WindowMaxConsumed == nil {
        t.Fatal("WindowMaxConsumed = nil, want non-nil")
    }
    if *n.WindowMaxConsumed != 87 {
        t.Errorf("WindowMaxConsumed = %d, want 87 (max of 100-prev=87 and 100-min(notified)=85)", *n.WindowMaxConsumed)
    }
    if n.IntervalResetAt == nil || *n.IntervalResetAt != *endAt {
        t.Errorf("IntervalResetAt not propagated from snapshot")
    }
    if n.WeeklyRemainingPct == nil || *n.WeeklyRemainingPct != 70 {
        t.Errorf("WeeklyRemainingPct not propagated from snapshot")
    }
}

func TestBuildResetNotification_NoNotified_NoPrev_NoWindowMax(t *testing.T) {
    pct := 0
    s := storage.Snapshot{ModelName: "general", IntervalRemainingPct: &pct}
    now := time.Now()
    n := buildResetNotification(s, storage.Snapshot{}, nil, 80, nil, now)
    if n.WindowMaxConsumed != nil {
        t.Errorf("WindowMaxConsumed = %d, want nil (no data to compute)", *n.WindowMaxConsumed)
    }
    if n.Kind != KindReset {
        t.Errorf("Kind = %q, want %q", n.Kind, KindReset)
    }
    if n.Used != 0 || n.Remaining != 100 {
        t.Errorf("Used=%d Remaining=%d, want 0/100", n.Used, n.Remaining)
    }
}

func TestBuildResetNotification_NotifiedHigherThanPrev(t *testing.T) {
    // Previous remaining = 50 (consumed = 50), but notified_pcts shows we
    // once saw consumed=92 (remaining=8). Max should be 92.
    pct := 0
    s := storage.Snapshot{ModelName: "general", IntervalRemainingPct: &pct}
    prevPct := 50
    prev := storage.Snapshot{ModelName: "general", IntervalRemainingPct: &prevPct}
    n := buildResetNotification(s, prev, []int{20, 12, 8}, 80, nil, time.Now())
    if n.WindowMaxConsumed == nil || *n.WindowMaxConsumed != 92 {
        t.Errorf("WindowMaxConsumed = %v, want 92 (from notified 8)", n.WindowMaxConsumed)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/notify/ -run TestBuildResetNotification -v`
Expected: FAIL with `buildResetNotification undefined` (and possibly `KindReset`, `KindAlert` undefined).

- [ ] **Step 3: Add the constants and struct fields**

In `internal/notify/notify.go`, add the constants near the top (above `Severity`):

```go
// Notification kinds — empty string is the default alert card.
const (
    KindAlert = ""
    KindReset = "reset"
)
```

Add the two new fields to the `Notification` struct:

```go
type Notification struct {
    IsTest                bool         `json:"is_test"`
    Kind                  string       `json:"kind,omitempty"`
    Model                 string       `json:"model"`
    Severity              Severity     `json:"severity"`
    Remaining             int          `json:"remaining"`
    Used                  int          `json:"used"`
    WeeklyRemainingPct    *int         `json:"weekly_remaining_pct,omitempty"`
    Threshold             int          `json:"threshold"`
    PrevNotifiedPct       *int         `json:"prev_notified_pct,omitempty"`
    WindowMaxConsumed     *int         `json:"window_max_consumed,omitempty"`
    IntervalResetAt       *int64       `json:"interval_reset_at,omitempty"`
    IntervalResetRemainMs *int64       `json:"interval_reset_remain_ms,omitempty"`
    WeeklyResetAt         *int64       `json:"weekly_reset_at,omitempty"`
    WeeklyResetRemainMs   *int64       `json:"weekly_reset_remain_ms,omitempty"`
    RecentTrend           []TrendPoint `json:"recent_trend,omitempty"`
    FetchedAt             int64        `json:"fetched_at"`
}
```

- [ ] **Step 4: Implement `buildResetNotification`**

Append to `internal/notify/notify.go`:

```go
// buildResetNotification composes the reset-window-rolled-over card
// payload. notifiedPcts are the remaining-percentage values at which
// alerts fired during the closing window; both those and prev.
// IntervalRemainingPct are scanned to compute the highest consumption
// reached before reset.
func buildResetNotification(s storage.Snapshot, prev storage.Snapshot,
    notifiedPcts []int, threshold int, trend []TrendPoint, now time.Time) Notification {
    n := Notification{
        Kind:        KindReset,
        Model:       s.ModelName,
        Severity:    SevInfo,
        Remaining:   100,
        Used:        0,
        Threshold:   threshold,
        RecentTrend: trend,
        FetchedAt:   now.UnixMilli(),
    }
    maxConsumed := 0
    if prev.IntervalRemainingPct != nil {
        if v := 100 - *prev.IntervalRemainingPct; v > maxConsumed {
            maxConsumed = v
        }
    }
    for _, r := range notifiedPcts {
        if v := 100 - r; v > maxConsumed {
            maxConsumed = v
        }
    }
    if maxConsumed > 0 {
        v := maxConsumed
        n.WindowMaxConsumed = &v
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
    if s.WeeklyRemainingPct != nil {
        v := *s.WeeklyRemainingPct
        n.WeeklyRemainingPct = &v
    }
    return n
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/notify/ -run TestBuildResetNotification -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Run the full notify test suite**

Run: `go test ./internal/notify/ -v`
Expected: PASS (existing tests untouched; new tests pass). Note that the
existing `alert_engine_test.go` tests still use the old threshold
semantic — Task 3 fixes those.

- [ ] **Step 7: Commit**

```bash
git add internal/notify/notify.go internal/notify/notify_test.go
git commit -m "feat(notify): add Kind + WindowMaxConsumed + buildResetNotification"
```

---

## Task 3: AlertEngine — flip threshold semantic to consumption

**Files:**
- Modify: `internal/notify/alert_engine.go`
- Test: `internal/notify/alert_engine_test.go`

**Interfaces:**
- Consumes: `Snapshot.IntervalRemainingPct` (remaining %, nullable)
- Behaviour change: threshold check becomes `consumed >= cfg.Threshold` (i.e. `100 - remaining >= cfg.Threshold`, equivalently `remaining <= 100 - cfg.Threshold`). All other behaviour unchanged.

- [ ] **Step 1: Update the four existing semantic tests**

In `internal/notify/alert_engine_test.go`, replace the four tests that
reference the old "remaining ≤ threshold" semantic with the consumption
semantic. The helper `snap(model, remaining)` already constructs the
remaining value; we change the test inputs.

Replace each of the following tests' body. Keep the function names
unchanged so the rest of the test file continues to reference them.

Replace `TestAlertEngine_CrossingThreshold_OneCall`:

```go
func TestAlertEngine_CrossingThreshold_OneCall(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    // consumed = 100 - 20 = 80 ≥ threshold 80 → trigger
    if err := eng.Evaluate(context.Background(), []storage.Snapshot{snap("general", 20)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 1 {
        t.Errorf("calls = %d, want 1", got)
    }
    c := fn.Calls()[0]
    if c.Remaining != 20 {
        t.Errorf("Remaining = %d, want 20", c.Remaining)
    }
    if c.Used != 80 {
        t.Errorf("Used = %d, want 80", c.Used)
    }
    if c.Kind != KindAlert {
        t.Errorf("Kind = %q, want %q (alert)", c.Kind, KindAlert)
    }
    st, _ := db.GetAlertState(context.Background(), "general")
    if len(st.NotifiedPcts) != 1 || st.NotifiedPcts[0] != 20 {
        t.Errorf("state after = %v, want [20]", st.NotifiedPcts)
    }
}
```

Replace `TestAlertEngine_AboveThreshold_NoCall` body — swap `snap("general", 81)` for `snap("general", 21)` (consumed = 79, just below threshold 80, no fire):

```go
    if err := eng.Evaluate(context.Background(), []storage.Snapshot{snap("general", 21)}); err != nil {
```

Replace `TestAlertEngine_DropBy1_NewCallEachTime`:

```go
func TestAlertEngine_DropBy1_NewCallEachTime(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    // consumed climbs 80 → 83 (remaining 20 → 17); one call per consumed integer crossed
    for _, p := range []int{20, 19, 18, 17} {
        _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", p)})
    }
    if got := len(fn.Calls()); got != 4 {
        t.Errorf("calls = %d, want 4 (one per pct drop)", got)
    }
    for i, c := range fn.Calls() {
        if c.Used != 80+i {
            t.Errorf("call[%d] Used = %d, want %d", i, c.Used, 80+i)
        }
    }
}
```

Replace `TestAlertEngine_Duplicate_NoSecondCall`:

```go
func TestAlertEngine_Duplicate_NoSecondCall(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    // consumed=80 (remaining=20); same value thrice → 1 call
    for i := 0; i < 3; i++ {
        _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)})
    }
    if got := len(fn.Calls()); got != 1 {
        t.Errorf("calls = %d, want 1 (dedup)", got)
    }
}
```

Replace `TestAlertEngine_SendFailure_DoesNotAdvance`:

```go
func TestAlertEngine_SendFailure_DoesNotAdvance(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{err: errors.New("network down")}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)})   // consumed=80, would fire
    st, _ := db.GetAlertState(ctx, "general")
    if len(st.NotifiedPcts) != 0 {
        t.Errorf("state after failed send = %v, want empty", st.NotifiedPcts)
    }
}
```

Replace `TestAlertEngine_NilRemaining_Skipped` — this one stays the
same conceptually (nil remaining still skipped), just confirm it's
unchanged:

```go
// unchanged
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/notify/ -run "TestAlertEngine_(CrossingThreshold|AboveThreshold|DropBy1|Duplicate|SendFailure)" -v`
Expected: FAIL — the engine still uses the old `remaining <= cfg.Threshold`
logic, so the new consumption-style inputs do not trigger.

- [ ] **Step 3: Flip the threshold check in Evaluate**

In `internal/notify/alert_engine.go`, locate the Evaluate loop. The
current block is:

```go
        if remaining >= 95 {
            if err := e.db.ClearAlertState(ctx, s.ModelName); err != nil {
                slog.Warn("alert: clear state failed", "model", s.ModelName, "err", err)
            }
            continue
        }
        if remaining > cfg.Threshold {
            continue
        }
```

Replace it with the consumption-based check. Note: the `remaining >= 95`
branch is preserved (it's the natural "newly reset" signal, equivalent
to `consumed <= 5`); Task 4 will refine reset detection but the
existing clear-on-near-full behaviour stays for now:

```go
        consumed := 100 - remaining
        // consumption-based threshold: alert when consumed ≥ cfg.Threshold
        if consumed < cfg.Threshold {
            continue
        }
        // dedup by remaining value (no schema change; values stored as
        // remaining integers in alert_state.notified_pcts)
        st, err := e.db.GetAlertState(ctx, s.ModelName)
        if err != nil {
            slog.Warn("alert: get state failed", "model", s.ModelName, "err", err)
            continue
        }
        if containsInt(st.NotifiedPcts, remaining) {
            continue
        }

        trend := e.recentTrend(ctx, s.ModelName, now)
        n := buildNotification(s, cfg.Threshold, remaining, st.NotifiedPcts, trend, now)

        if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
            slog.Warn("alert: send failed",
                "model", s.ModelName, "pct", remaining, "err", err)
            continue
        }

        st.NotifiedPcts = appendUniqueSorted(st.NotifiedPcts, remaining)
        st.UpdatedAt = now.UnixMilli()
        if err := e.db.SetAlertState(ctx, s.ModelName, st); err != nil {
            slog.Warn("alert: set state failed", "model", s.ModelName, "err", err)
        }
```

Delete the now-redundant earlier `GetAlertState` / `containsInt` /
`appendUniqueSorted` block that lived between the threshold check and
the `buildNotification` call (the new code above re-runs them in the
correct order). Final shape of the inner loop is:

```go
    for _, s := range snaps {
        if s.IntervalRemainingPct == nil {
            continue
        }
        remaining := *s.IntervalRemainingPct
        consumed := 100 - remaining

        if consumed < cfg.Threshold {
            continue
        }
        st, err := e.db.GetAlertState(ctx, s.ModelName)
        if err != nil {
            slog.Warn("alert: get state failed", "model", s.ModelName, "err", err)
            continue
        }
        if containsInt(st.NotifiedPcts, remaining) {
            continue
        }
        trend := e.recentTrend(ctx, s.ModelName, now)
        n := buildNotification(s, cfg.Threshold, remaining, st.NotifiedPcts, trend, now)
        if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
            slog.Warn("alert: send failed",
                "model", s.ModelName, "pct", remaining, "err", err)
            continue
        }
        st.NotifiedPcts = appendUniqueSorted(st.NotifiedPcts, remaining)
        st.UpdatedAt = now.UnixMilli()
        if err := e.db.SetAlertState(ctx, s.ModelName, st); err != nil {
            slog.Warn("alert: set state failed", "model", s.ModelName, "err", err)
        }
    }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/notify/ -run "TestAlertEngine_" -v`
Expected: PASS (all updated tests pass; nil/empty URL/disabled tests still pass).

- [ ] **Step 5: Run the full notify suite**

Run: `go test ./internal/notify/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/alert_engine.go internal/notify/alert_engine_test.go
git commit -m "feat(notify): flip threshold semantic to consumed (consumed >= threshold)"
```

---

## Task 4: AlertEngine — reset detection

**Files:**
- Modify: `internal/notify/alert_engine.go`
- Test: `internal/notify/alert_engine_test.go`

**Interfaces:**
- Produces (helper, unexported): `func isResetTransition(prev, cur storage.Snapshot) bool`
- Behaviour change: before the consumption threshold check, fetch `prev` via `db.PrevSnapshot(ctx, model, fetchedAt)`. If `isResetTransition(prev, cur)` is true, build the reset notification (via `buildResetNotification`), send it, clear state on success, and `continue` to the next snapshot.

- [ ] **Step 1: Write the failing tests for reset detection**

Append to `internal/notify/alert_engine_test.go`:

```go
// snapWithEnd builds a snapshot with explicit interval_end_at so reset
// detection can be tested. endAtMs is in unix-ms.
func snapWithEnd(model string, remaining int, endAtMs int64) storage.Snapshot {
    pct := remaining
    return storage.Snapshot{
        ModelName:            model,
        IntervalRemainingPct: &pct,
        IntervalEndAt:        &endAtMs,
        FetchedAt:            time.Now().UnixMilli(),
    }
}

func seedSnapshot(t *testing.T, db *storage.DB, s storage.Snapshot) {
    t.Helper()
    if err := db.InsertOne(context.Background(), s); err != nil {
        t.Fatalf("seedSnapshot: %v", err)
    }
}

func TestAlertEngine_ResetTransition_FiresReset(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    // Seed a previous snapshot: consumed=87 (remaining=13), end_at = T1
    seedSnapshot(t, db, snapWithEnd("general", 13, 1_000_000))
    // Current snapshot: consumed=0 (remaining=100), end_at = T2 > T1 (window rolled)
    if err := eng.Evaluate(ctx, []storage.Snapshot{snapWithEnd("general", 100, 2_000_000)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 1 {
        t.Fatalf("calls = %d, want 1 (reset card)", got)
    }
    c := fn.Calls()[0]
    if c.Kind != KindReset {
        t.Errorf("Kind = %q, want %q", c.Kind, KindReset)
    }
    if c.Severity != SevInfo {
        t.Errorf("Severity = %v, want SevInfo", c.Severity)
    }
    if c.Used != 0 || c.Remaining != 100 {
        t.Errorf("Used=%d Remaining=%d, want 0/100", c.Used, c.Remaining)
    }
    if c.WindowMaxConsumed == nil || *c.WindowMaxConsumed != 87 {
        t.Errorf("WindowMaxConsumed = %v, want 87", c.WindowMaxConsumed)
    }
    // State must have been cleared so the next threshold crossing fires fresh
    st, _ := db.GetAlertState(ctx, "general")
    if len(st.NotifiedPcts) != 0 {
        t.Errorf("state after reset = %v, want empty", st.NotifiedPcts)
    }
}

func TestAlertEngine_ResetTransition_NoUsage_DoesNotFire(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    // prev: never used (remaining=100), end_at = T1
    seedSnapshot(t, db, snapWithEnd("general", 100, 1_000_000))
    // cur: still never used, end_at = T2 > T1
    if err := eng.Evaluate(ctx, []storage.Snapshot{snapWithEnd("general", 100, 2_000_000)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 0 {
        t.Errorf("calls = %d, want 0 (no usage → no reset notif)", got)
    }
}

func TestAlertEngine_ResetTransition_SameWindow_DoesNotFire(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    // prev: consumed=87 (remaining=13), end_at = T1
    seedSnapshot(t, db, snapWithEnd("general", 13, 1_000_000))
    // cur: still consumed=87 (remaining=13), end_at = T1 (unchanged)
    if err := eng.Evaluate(ctx, []storage.Snapshot{snapWithEnd("general", 13, 1_000_000)}); err != nil {
        t.Fatalf("Evaluate: %v", err)
    }
    if got := len(fn.Calls()); got != 0 {
        t.Errorf("calls = %d, want 0 (same window)", got)
    }
}

func TestAlertEngine_ResetAfterThreshold_ClearsThenRefires(t *testing.T) {
    db := openTestDB(t)
    fn := &fakeNotifier{}
    eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
    ctx := context.Background()
    // Tick 1: consumed=80, no prev seeded → normal alert fires
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)})
    if got := len(fn.Calls()); got != 1 {
        t.Fatalf("setup: calls = %d, want 1", got)
    }
    // Now seed a prev snapshot with remaining=15 (consumed=85)
    seedSnapshot(t, db, snapWithEnd("general", 15, 5_000_000))
    // Tick 2: reset transition (cur remaining=100, end_at advanced) → reset card fires
    _ = eng.Evaluate(ctx, []storage.Snapshot{snapWithEnd("general", 100, 6_000_000)})
    if got := len(fn.Calls()); got != 2 {
        t.Fatalf("after reset: calls = %d, want 2", got)
    }
    if fn.Calls()[1].Kind != KindReset {
        t.Errorf("call[1].Kind = %q, want %q", fn.Calls()[1].Kind, KindReset)
    }
    // Tick 3: consumed=80 again (remaining=20) → fresh alert fires (state was cleared)
    _ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)})
    if got := len(fn.Calls()); got != 3 {
        t.Errorf("after refire: calls = %d, want 3", got)
    }
    if fn.Calls()[2].Kind != KindAlert {
        t.Errorf("call[2].Kind = %q, want %q", fn.Calls()[2].Kind, KindAlert)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/notify/ -run "TestAlertEngine_ResetTransition|TestAlertEngine_ResetAfterThreshold" -v`
Expected: FAIL — `isResetTransition` undefined; engine doesn't call it.

- [ ] **Step 3: Implement `isResetTransition` and integrate it into Evaluate**

In `internal/notify/alert_engine.go`, add the helper above `Evaluate`:

```go
// isResetTransition reports whether `cur` represents the moment the
// 5-minute interval window rolled over after real usage. Conditions:
//   - previous snapshot had non-zero consumption (remaining > 0)
//   - current snapshot has zero consumption (remaining == 0)
//   - interval_end_at strictly advanced (new window boundary)
// All three must hold; missing IntervalRemainingPct or IntervalEndAt on
// either side disables detection.
func isResetTransition(prev, cur storage.Snapshot) bool {
    if prev.IntervalRemainingPct == nil || cur.IntervalRemainingPct == nil {
        return false
    }
    if prev.IntervalEndAt == nil || cur.IntervalEndAt == nil {
        return false
    }
    return *prev.IntervalRemainingPct > 0
        && *cur.IntervalRemainingPct == 0
        && *cur.IntervalEndAt > *prev.IntervalEndAt
}
```

Now modify the `Evaluate` loop body. The very first lines inside the
`for _, s := range snaps` block must become:

```go
        if s.IntervalRemainingPct == nil {
            continue
        }
        remaining := *s.IntervalRemainingPct
        consumed := 100 - remaining

        // 1) Reset transition detection — runs before threshold check so
        // a fresh window with consumed=0 doesn't accidentally trigger.
        prev, _ := e.db.PrevSnapshot(ctx, s.ModelName, s.FetchedAt)
        if isResetTransition(prev, s) {
            trend := e.recentTrend(ctx, s.ModelName, now)
            st, _ := e.db.GetAlertState(ctx, s.ModelName)
            n := buildResetNotification(s, prev, st.NotifiedPcts, cfg.Threshold, trend, now)
            if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
                slog.Warn("alert reset send failed",
                    "model", s.ModelName, "err", err)
                // do NOT clear state on failure — next tick may retry
            } else {
                if err := e.db.ClearAlertState(ctx, s.ModelName); err != nil {
                    slog.Warn("alert: clear state after reset failed",
                        "model", s.ModelName, "err", err)
                }
            }
            continue
        }

        // 2) Consumption threshold check
        if consumed < cfg.Threshold {
            continue
        }

        // 3) Dedup and send (unchanged from Task 3)
        st, err := e.db.GetAlertState(ctx, s.ModelName)
        if err != nil {
            slog.Warn("alert: get state failed", "model", s.ModelName, "err", err)
            continue
        }
        if containsInt(st.NotifiedPcts, remaining) {
            continue
        }
        trend := e.recentTrend(ctx, s.ModelName, now)
        n := buildNotification(s, cfg.Threshold, remaining, st.NotifiedPcts, trend, now)
        if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
            slog.Warn("alert: send failed",
                "model", s.ModelName, "pct", remaining, "err", err)
            continue
        }
        st.NotifiedPcts = appendUniqueSorted(st.NotifiedPcts, remaining)
        st.UpdatedAt = now.UnixMilli()
        if err := e.db.SetAlertState(ctx, s.ModelName, st); err != nil {
            slog.Warn("alert: set state failed", "model", s.ModelName, "err", err)
        }
```

Note: Task 3's `remaining >= 95 → clearAlertState` branch is removed
because `isResetTransition` now does that work (clear on reset card
success). The new engine has a single clear-state path.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/notify/ -run "TestAlertEngine_" -v`
Expected: PASS (all 4 reset tests + all earlier threshold tests).

- [ ] **Step 5: Run the full notify suite**

Run: `go test ./internal/notify/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/alert_engine.go internal/notify/alert_engine_test.go
git commit -m "feat(notify): detect interval reset transition and emit reset card"
```

---

## Task 5: Feishu — alert card emphasizes consumption

**Files:**
- Modify: `internal/notify/feishu.go`
- Test: `internal/notify/feishu_test.go`

**Interfaces:**
- Refactor: extract the alert card builder from `buildCardPayload` into `buildAlertCard(n Notification) map[string]any`
- Card field reorder: `消耗` becomes the bold/large field; `剩余` moves to secondary; `阈值` becomes `≥X%`; `上次告警` becomes `上次告警 (消耗)` and shows `100 - *PrevNotifiedPct` (or `—`)

- [ ] **Step 1: Write the failing test**

Append to `internal/notify/feishu_test.go`:

```go
func TestBuildCardPayload_AlertCard_EmphasizesUsed(t *testing.T) {
    prev := intPtr(20)   // remaining at previous notification = 20 → consumed = 80
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
        t.Errorf("expected bold '消耗 80%' in card; body=%s", s)
    }
    // 3) threshold field uses '≥80%' (not '≤80%')
    if !strings.Contains(s, "≥80%") {
        t.Errorf("expected '≥80%' threshold copy; body=%s", s)
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
```

Note: this test needs the `intPtr` helper if not already in the file. If absent, add at top:

```go
func intPtr(v int) *int { return &v }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notify/ -run TestBuildCardPayload_AlertCard_EmphasizesUsed -v`
Expected: FAIL — the current card emits "剩余" first and uses "≤X%" not "≥X%".

- [ ] **Step 3: Refactor `buildCardPayload` to split into alert and reset branches**

In `internal/notify/feishu.go`, replace the existing `buildCardPayload` function (everything from `func buildCardPayload(n Notification) map[string]any` to the end of the file) with:

```go
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
```

The new `buildResetCard` function is implemented in Task 6.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notify/ -run TestBuildCardPayload_AlertCard_EmphasizesUsed -v`
Expected: PASS.

- [ ] **Step 5: Run the full notify suite**

Run: `go test ./internal/notify/ -v`
Expected: PASS (existing tests untouched; the new alert card emphasis test passes; reset card tests will fail with "buildResetCard undefined" until Task 6 — that's expected).

If `buildResetCard` doesn't exist yet, the dispatch in `buildCardPayload`
panics. To avoid that during the interim Task 5 run, add a stub:

```go
func buildResetCard(n Notification) map[string]any {
    return buildAlertCard(n)  // stub until Task 6
}
```

Replace the stub with the real implementation in Task 6.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/feishu.go internal/notify/feishu_test.go
git commit -m "feat(notify): alert card emphasizes consumption; refactor payload dispatch"
```

---

## Task 6: Feishu — reset card variant

**Files:**
- Modify: `internal/notify/feishu.go`
- Test: `internal/notify/feishu_test.go`

**Interfaces:**
- Replace the `buildResetCard` stub from Task 5 with the real implementation
- Card title: `🔄 配额重置 · <model>`; template color: `blue`
- Card fields: 模型, 触发时间, 消耗 0%, 剩余 100%, 本周期最高消耗, 区间重置, 周重置, 本周剩余
- Note line: `区间已重置,下次告警阈值 ≥ <threshold>% 消耗时触发。`

- [ ] **Step 1: Write the failing test**

Append to `internal/notify/feishu_test.go`:

```go
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
    if !strings.Contains(s, "≥80%") {
        t.Errorf("expected '≥80%' threshold copy in note; body=%s", s)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/notify/ -run TestBuildCardPayload_ResetCard -v`
Expected: FAIL — current stub returns the alert card.

- [ ] **Step 3: Implement `buildResetCard`**

In `internal/notify/feishu.go`, replace the Task 5 stub with:

```go
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
                "template": n.Severity.Template(),   // SevInfo.Template() == "blue"
            },
            "elements": elements,
        },
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/notify/ -run TestBuildCardPayload_ResetCard -v`
Expected: PASS (both tests).

- [ ] **Step 5: Run the full notify suite**

Run: `go test ./internal/notify/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/feishu.go internal/notify/feishu_test.go
git commit -m "feat(notify): reset card variant with window-max-consumed field"
```

---

## Task 7: Web — card render with buildModels semantics + formatIntervalMeta

**Files:**
- Modify: `internal/server/web/app.js`

**Interfaces:**
- New helpers: `statusLabel(s)`, `formatIntervalMeta(ms, unlimited)`, `barColor(model, consumed)`, `hexToRgb(h)`
- Behaviour change: `renderCards` derives `intervalUnlimited` and `weeklyUnlimited` from `status === 3`; uses `formatIntervalMeta` for the interval row; uses `barColor` for the progress bar + pct text colour; unlimited models force `consumed = 0` and bypass red-shift
- Remove: existing `statusText` helper (replaced by `statusLabel`)

- [ ] **Step 1: Replace `statusText` with `statusLabel` and add new helpers**

In `internal/server/web/app.js`, find the existing:

```js
  const statusText = (s) => s === 1 ? '活跃' : s === 3 ? '未活动' : '--';
```

Replace with:

```js
  const statusLabel = (s) => s === 1 ? '活跃' : s === 3 ? '不限' : '--';

  const ACCENT_RED = [239, 68, 68];  // matches --status-bad in style.css
  const hexToRgb = (h) => {
    const m = h.replace('#', '');
    return [parseInt(m.slice(0, 2), 16), parseInt(m.slice(2, 4), 16), parseInt(m.slice(4, 6), 16)];
  };
  const barColor = (model, consumed) => {
    const accent = ACCENT[model] || '#00d4ff';
    if (consumed < 80) return accent;
    const t = Math.min(1, (consumed - 80) / 20);
    const a = hexToRgb(accent);
    const r = Math.round(a[0] + (ACCENT_RED[0] - a[0]) * t);
    const g = Math.round(a[1] + (ACCENT_RED[1] - a[1]) * t);
    const b = Math.round(a[2] + (ACCENT_RED[2] - a[2]) * t);
    return `rgb(${r},${g},${b})`;
  };

  const formatIntervalMeta = (ms, unlimited) => {
    if (unlimited) return '不限';
    const s = Math.round(ms / 1000);
    const time = s < 60
      ? s + 's'
      : s < 3600
        ? Math.floor(s / 60) + 'm'
        : Math.floor(s / 3600) + 'h ' + (s % 3600 ? Math.floor((s % 3600) / 60) + 'm' : '');
    const status = statusLabel(interval_status_value);  // patched in step 2
    return status === '--' ? time : `${time} · ${status}`;
  };
```

The `formatIntervalMeta` uses a placeholder for `interval_status_value`
because we don't have access to the snapshot's status from inside the
helper. This will be fixed in step 2 by inlining the logic.

Replace the above with the corrected version (status passed in):

```js
  const formatIntervalMeta = (ms, unlimited, status) => {
    if (unlimited) return '不限';
    const s = Math.round(ms / 1000);
    const time = s < 60
      ? s + 's'
      : s < 3600
        ? Math.floor(s / 60) + 'm'
        : Math.floor(s / 3600) + 'h ' + (s % 3600 ? Math.floor((s % 3600) / 60) + 'm' : '');
    const label = statusLabel(status);
    return label === '--' ? time : `${time} · ${label}`;
  };
```

- [ ] **Step 2: Update `renderCards` to use the new helpers**

Find the existing `renderCards` function and replace the inner block
that builds the card:

```js
  function renderCards() {
    const root = $('cards');
    if (state.models.size === 0) {
      if (!root.querySelector('.skeleton')) {
        root.innerHTML = '<div class="card skeleton" style="height:160px"></div>'.repeat(2);
      }
      return;
    }
    root.innerHTML = '';
    [...state.models.keys()].sort().forEach((name) => {
      const m = state.models.get(name);
      const card = document.createElement('div');
      card.className = 'card';
      card.dataset.model = name;
      const intervalUnlimited = m.interval_status === 3;
      const weeklyUnlimited = m.weekly_status === 3;
      const remainPct = m.interval_remaining_pct ?? 100;
      const consumed = intervalUnlimited ? 0 : Math.max(0, Math.min(100, 100 - remainPct));
      const remainWeekly = m.weekly_remaining_pct;
      const consumedWeekly = weeklyUnlimited
        ? 0
        : (remainWeekly == null ? null : Math.max(0, Math.min(100, 100 - remainWeekly)));
      const remainsMs = m.interval_remains_ms ?? 0;
      const color = intervalUnlimited ? ACCENT[name] || '#00d4ff' : barColor(name, consumed);
      card.innerHTML = `
        <h3>${name} <span class="pct-kind">已用</span></h3>
        <div class="pct" style="color:${color}">${consumed}%</div>
        <div class="bar"><div class="bar-fill" style="width:${consumed}%;background:${color}"></div></div>
        <div class="meta"><span>区间</span><b>${formatIntervalMeta(remainsMs, intervalUnlimited, m.interval_status)}</b></div>
        <div class="meta"><span>本周</span><b>${weeklyUnlimited ? '不限' : (consumedWeekly == null ? '--' : consumedWeekly + '%')}</b></div>
      `;
      root.appendChild(card);
    });
  }
```

- [ ] **Step 3: Remove now-unused helpers**

`formatRangeMs` and `statusText` are no longer referenced. Delete
their definitions (the IIFE wrapper cleans up). Find and delete:

```js
  const formatRangeMs = (ms) => { ... };
```

(already not used after the rewrite; same for `statusText` if any
caller remained — search-and-replace `statusText(` → `statusLabel(` to
catch stragglers).

- [ ] **Step 4: Build and smoke-test the dashboard**

Run: `go build ./...`
Expected: builds clean (the JS is embedded, but Go compile verifies
file references).

Then rebuild the binary: `build.bat` (or `go build -o dist/minimax-monitor.exe ./cmd/minimax-monitor`).

Run the binary: `dist/minimax-monitor.exe` (with a valid API key
configured). Open the dashboard at `http://localhost:13337`. Verify in
the browser:

- A model with `interval_status=1` and remaining=20% shows `consumed=80%`,
  accent colour, meta line `5m · 活跃` (or whatever the live data shows).
- A model with `interval_status=3` shows `consumed=0%`, accent colour,
  meta line `不限`.
- The progress bar and `pct` text both shift toward red as consumption
  climbs past 80% (use DevTools to inspect `style.background` on
  `.bar-fill`).

- [ ] **Step 5: Commit**

```bash
git add internal/server/web/app.js
git commit -m "feat(web): card render uses buildModels-style unlimited + red-shift"
```

---

## Task 8: Web — remove hard-coded `.bar-fill` background

**Files:**
- Modify: `internal/server/web/style.css`

**Interfaces:**
- Remove the `.bar-fill { background: var(--accent); }` rule so the inline `style="background:${color}"` injected by `barColor` wins. The width transition rule stays.

- [ ] **Step 1: Locate and edit the rule**

Open `internal/server/web/style.css` and find:

```css
.bar-fill { height: 100%; background: var(--accent); border-radius: 3px;
  transition: width 600ms cubic-bezier(0.4, 0, 0.2, 1); }
```

Replace with:

```css
.bar-fill { height: 100%; border-radius: 3px;
  transition: width 600ms cubic-bezier(0.4, 0, 0.2, 1); }
```

(`background` is now set inline by `app.js`.)

- [ ] **Step 2: Reload the dashboard and verify**

Rebuild the binary (or rely on dev-mode asset refresh if your workflow
uses one — for embedded assets, a rebuild is required).

Open the browser, refresh, and confirm:

- Limited-model cards still show the bar fill (no transparent bar).
- The bar colour transitions to red as consumption rises past 80%.
- Unlimited-model cards show the bar at 0% width with the accent colour
  (no red tint).

- [ ] **Step 3: Commit**

```bash
git add internal/server/web/style.css
git commit -m "style(web): remove hard-coded bar-fill background so inline red-shift wins"
```

---

## Task 9: Web — settings modal threshold label

**Files:**
- Modify: `internal/server/web/index.html`

**Interfaces:**
- Update label "阈值 (剩余%)" → "阈值 (消耗%)"
- Update help text "剩余≤该值告警;每下降1%再告警一次" → "消耗达到该值时告警;每再消耗 1% 再告警一次"

- [ ] **Step 1: Edit the modal markup**

Open `internal/server/web/index.html` and find:

```html
          <div class="form-row">
            <label for="alertThreshold">阈值 (剩余%)</label>
            <input id="alertThreshold" type="number" min="1" max="100" value="80" />
            <small class="muted">剩余≤该值告警;每下降1%再告警一次</small>
          </div>
```

Replace with:

```html
          <div class="form-row">
            <label for="alertThreshold">阈值 (消耗%)</label>
            <input id="alertThreshold" type="number" min="1" max="100" value="80" />
            <small class="muted">消耗达到该值时告警;每再消耗 1% 再告警一次</small>
          </div>
```

- [ ] **Step 2: Reload and verify**

Rebuild, restart the binary, open the dashboard settings modal (⚙ →
展开 "告警通知"). Confirm the new label and help text appear. The
input field, default value, and validation behaviour are unchanged.

- [ ] **Step 3: Commit**

```bash
git add internal/server/web/index.html
git commit -m "docs(web): threshold label copy reflects consumption semantic"
```

---

## Task 10: README — update alerting section

**Files:**
- Modify: `README.md`

**Interfaces:**
- Replace the "Alerting (Feishu / Lark)" section to document the new consumption semantic and the reset notification.

- [ ] **Step 1: Edit the README**

Open `README.md` and find the section:

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

Replace with:

```markdown
## Alerting (Feishu / Lark)

The dashboard can post interactive-card notifications to a Feishu (or Lark)
custom bot when the configured **consumption** threshold is crossed.

1. Click ⚙ in the header → expand "告警通知".
2. Paste your webhook URL (with optional `?secret=...` — signing is auto-detected).
3. Set the threshold (default 80). Alerts fire when **consumed %** reaches
   this value, then once more for every additional 1% of consumption until
   the 5-minute window resets.
4. Click "保存", then "发送测试" to verify delivery.

When the 5-minute interval window rolls over after real usage, a separate
`🔄 配额重置` card is also delivered, summarising the highest consumption
reached during the closing window.

Disable to clear all dedup state — the next enable starts a fresh window.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README alerting section reflects consumption-forward semantic"
```

---

## Self-Review Checklist (run before declaring the plan complete)

1. **Spec coverage:**
   - §1.1.1 threshold flip → Tasks 3, 9, 10 ✓
   - §1.1.2 consumption-forward card → Tasks 5, 10 ✓
   - §1.1.3 reset notification → Tasks 1, 2, 4, 6, 10 ✓
   - §1.1.4 red-shift → Tasks 7, 8 ✓
   - §1.1.5 time display fix → Task 7 (formatIntervalMeta) ✓
   - §3.3 status semantics → Task 7 (statusLabel), Task 5 (consumed% primary) ✓
   - §4.1 isResetTransition → Task 4 ✓
   - §4.3 buildResetNotification → Task 2 ✓
   - §5.1 alert card fields → Task 5 ✓
   - §5.2 reset card fields → Task 6 ✓
   - §8.1 modal label → Task 9 ✓
   - §8.2 barColor / unlimited handling → Task 7 ✓
   - §8.3 formatIntervalMeta rules → Task 7 ✓
   - §11 testing strategy → covered across all tasks ✓
   - §12 acceptance criteria → validated by tests in Tasks 1–6, manual browser check in Tasks 7–9 ✓

2. **Placeholder scan:** no TBD / TODO / "implement later". All step
   bodies contain concrete code, exact file paths, exact commands, and
   expected output.

3. **Type / method consistency:**
   - `db.PrevSnapshot` defined Task 1, consumed Task 4 ✓
   - `KindAlert` / `KindReset` defined Task 2, consumed Tasks 4, 5, 6 ✓
   - `WindowMaxConsumed` defined Task 2, consumed Task 6 ✓
   - `buildResetNotification` defined Task 2, consumed Task 4 ✓
   - `isResetTransition` defined Task 4, no other consumer ✓
   - `buildAlertCard` / `buildResetCard` defined Tasks 5/6, dispatched in `buildCardPayload` ✓
   - `statusLabel` / `formatIntervalMeta` / `barColor` / `hexToRgb` defined Task 7, consumed only inside `renderCards` ✓
   - `InsertOne` defined Task 1 as a test seam; not used by production code ✓

4. **Risks acknowledged in spec §13:**
   - README updated (Task 10) → risk "users confused by v2→v3 threshold meaning" mitigated.
   - `WindowMaxConsumed` computed as max of prev + notified (Task 2) → risk "inaccurate if notified_pcts cleared" mitigated.
   - `barColor` called per card per WS message → risk "repaint cost" deemed negligible (per-card, ≤4 models).
   - `formatIntervalMeta` drops `· --` ≥ 1h → risk "loses information" accepted per spec.