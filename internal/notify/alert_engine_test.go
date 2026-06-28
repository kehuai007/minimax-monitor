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
	_ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 100)})
	st, _ := db.GetAlertState(ctx, "general")
	if len(st.NotifiedPcts) != 0 {
		t.Errorf("state after reset = %v, want empty", st.NotifiedPcts)
	}
	_ = eng.Evaluate(ctx, []storage.Snapshot{snap("general", 20)})
	if got := len(fn.Calls()); got != 3 {
		t.Errorf("calls after reset+redrop = %d, want 3", got)
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