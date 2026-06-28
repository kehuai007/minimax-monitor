package notify

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
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
	if err := eng.Evaluate(context.Background(), []storage.Snapshot{snap("general", 21)}); err != nil {
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

func TestAlertEngine_IntervalReset_ClearsState(t *testing.T) {
	db := openTestDB(t)
	fn := &fakeNotifier{}
	eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
	ctx := context.Background()
	// consumed = 80 (remaining=20); consumed = 81 (remaining=19)
	_ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)})
	_ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 19)})
	if got := len(fn.Calls()); got != 2 {
		t.Fatalf("setup: calls = %d, want 2", got)
	}
	// Seed a prev snapshot with remaining=15 (consumed=85), end_at = T1.
	// This anchors the next snapshot as a reset transition (not just a
	// low-consumption tick).
	seedSnapshot(t, db, snapWithEnd("general", 15, 5_000_000))
	_ = eng.Evaluate(ctx, []storage.Snapshot{snapWithEnd("general", 100, 6_000_000)})
	st, _ := db.GetAlertState(ctx, "general")
	if len(st.NotifiedPcts) != 0 {
		t.Errorf("state after reset = %v, want empty", st.NotifiedPcts)
	}
	if fn.Calls()[2].Kind != KindReset {
		t.Errorf("reset tick Kind = %q, want %q", fn.Calls()[2].Kind, KindReset)
	}
	_ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)})
	if got := len(fn.Calls()); got != 4 {
		t.Errorf("calls after reset+redrop = %d, want 4", got)
	}
	if fn.Calls()[3].Kind != KindAlert {
		t.Errorf("refire Kind = %q, want %q", fn.Calls()[3].Kind, KindAlert)
	}
}

func TestAlertEngine_SendFailure_DoesNotAdvance(t *testing.T) {
	db := openTestDB(t)
	fn := &fakeNotifier{err: errors.New("network down")}
	eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
	ctx := context.Background()
	_ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)}) // consumed=80, would fire
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
	// Pin FetchedAt to a time strictly earlier than any subsequent
	// snapWithEnd call. Windows' UnixMilli() can return the same value
	// for two calls in the same millisecond, which would make
	// PrevSnapshot return empty and break reset-detection tests.
	s.FetchedAt = time.Now().UnixMilli() - 1000
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
	// No RESET card should fire when end_at hasn't advanced.
	// (The threshold path may still fire — dedup is state-based, not
	// prev-based — but the reset-transition guard must not produce a card.)
	for i, c := range fn.Calls() {
		if c.Kind == KindReset {
			t.Errorf("call[%d].Kind = %q, want no reset card (same window)", i, c.Kind)
		}
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

// TestAlertEngine_PrevSnapshotError_NoPanicNoFire is a regression test for the
// silent-swallow bug: when PrevSnapshot returns an error (e.g. transient SQLite
// lock, schema drift, disk full), the engine must NOT panic, must NOT fire the
// reset card, and must surface the error via slog.Warn. We simulate the failure
// by closing the DB before Evaluate — every DB call then returns an error.
func TestAlertEngine_PrevSnapshotError_NoPanicNoFire(t *testing.T) {
	db := openTestDB(t)
	fn := &fakeNotifier{}
	eng := NewAlertEngine(db, fn, func() storage.AlertConfig { return storage.AlertConfig{Enabled: true, URL: "x", Threshold: 80} })
	// Close the DB so any DB call inside Evaluate fails. This simulates the
	// transient DB-error condition the fix targets.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	// Capture slog output so we can assert the Warn line is emitted.
	var logBuf safeBuffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prevLogger)

	ctx := context.Background()
	// Supply a snapshot that would otherwise look like a reset transition
	// (remaining=100, with end_at set). With PrevSnapshot erroring, the
	// engine must not fire the reset card.
	cur := snapWithEnd("general", 100, 2_000_000)
	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Evaluate panicked on PrevSnapshot error: %v", r)
		}
	}()
	if err := eng.Evaluate(ctx, []storage.Snapshot{cur}); err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if got := len(fn.Calls()); got != 0 {
		t.Errorf("calls = %d, want 0 (reset must not fire when PrevSnapshot errors)", got)
	}
	// The error must have been logged via slog.Warn.
	logs := logBuf.String()
	if !contains(logs, "prev snapshot lookup failed") {
		t.Errorf("expected slog.Warn with 'prev snapshot lookup failed' message, got logs:\n%s", logs)
	}
}

// safeBuffer is a minimal thread-safe bytes.Buffer for capturing slog output.
type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// contains reports whether substr appears in s.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}