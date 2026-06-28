package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"minimax-monitor/internal/model"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sampleResp() *model.APIResponse {
	return &model.APIResponse{
		ModelRemains: []model.ModelRemains{
			{
				ModelName: "general", CurrentIntervalRemainingPct: 90, CurrentWeeklyRemainingPct: 100,
				EndTime: 1782561600000, RemainsTime: 3600000,
			},
			{
				ModelName: "video", CurrentIntervalRemainingPct: 100, CurrentWeeklyRemainingPct: 100,
			},
		},
		BaseResp: model.BaseResp{StatusCode: 0, StatusMsg: "success"},
	}
}

func TestInsert_AndLatest(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.UnixMilli(1782543600000)
	if err := db.Insert(ctx, sampleResp(), now); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	rows, err := db.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Latest len = %d, want 2", len(rows))
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.ModelName] = *r.IntervalRemainingPct
	}
	if got["general"] != 90 || got["video"] != 100 {
		t.Errorf("latest pcts = %+v", got)
	}
}

func TestHistory_Bucketed(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	// t0 aligned to 30s bucket grid; 9 rows over 90s.
	// interval values 10,20,...,90 (decreasing); weekly values 90,80,...,10 (decreasing).
	t0 := time.UnixMilli(1700000010000)
	for i := 0; i < 9; i++ {
		resp := &model.APIResponse{
			ModelRemains: []model.ModelRemains{{
				ModelName:                "general",
				CurrentIntervalRemainingPct: 10 + i*10,
				CurrentWeeklyRemainingPct:   90 - i*10,
			}},
			BaseResp: model.BaseResp{StatusCode: 0, StatusMsg: "ok"},
		}
		if err := db.Insert(ctx, resp, t0.Add(time.Duration(i*10)*time.Second)); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}
	buckets, err := db.History(ctx, "general",
		t0.UnixMilli(),
		t0.Add(90*time.Second).UnixMilli(),
		30_000)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(buckets) != 3 {
		t.Fatalf("bucket count = %d, want 3", len(buckets))
	}
	// Interval series
	if buckets[0].IntervalMin != 10 || buckets[0].IntervalMax != 30 || buckets[0].IntervalAvg != 20 {
		t.Errorf("bucket 0 interval = %+v", buckets[0])
	}
	if buckets[1].IntervalMin != 40 || buckets[1].IntervalMax != 60 || buckets[1].IntervalAvg != 50 {
		t.Errorf("bucket 1 interval = %+v", buckets[1])
	}
	if buckets[2].IntervalMin != 70 || buckets[2].IntervalMax != 90 || buckets[2].IntervalAvg != 80 {
		t.Errorf("bucket 2 interval = %+v", buckets[2])
	}
	// Weekly series (reverse of interval)
	if buckets[0].WeeklyMin != 70 || buckets[0].WeeklyMax != 90 || buckets[0].WeeklyAvg != 80 {
		t.Errorf("bucket 0 weekly = %+v", buckets[0])
	}
	if buckets[1].WeeklyMin != 40 || buckets[1].WeeklyMax != 60 || buckets[1].WeeklyAvg != 50 {
		t.Errorf("bucket 1 weekly = %+v", buckets[1])
	}
	if buckets[2].WeeklyMin != 10 || buckets[2].WeeklyMax != 30 || buckets[2].WeeklyAvg != 20 {
		t.Errorf("bucket 2 weekly = %+v", buckets[2])
	}
}

func TestPrune(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	old := time.UnixMilli(1000)
	recent := time.UnixMilli(2000)
	_ = db.Insert(ctx, &model.APIResponse{
		ModelRemains: []model.ModelRemains{{ModelName: "g", CurrentIntervalRemainingPct: 1}},
		BaseResp:     model.BaseResp{StatusCode: 0},
	}, old)
	_ = db.Insert(ctx, &model.APIResponse{
		ModelRemains: []model.ModelRemains{{ModelName: "g", CurrentIntervalRemainingPct: 2}},
		BaseResp:     model.BaseResp{StatusCode: 0},
	}, recent)
	n, err := db.PruneOlderThan(ctx, 1500)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned = %d, want 1", n)
	}
	rows, _ := db.Latest(ctx)
	if len(rows) != 1 || *rows[0].IntervalRemainingPct != 2 {
		t.Errorf("after prune = %+v", rows)
	}
}

// TestSnapshot_JSONTags guards the WS broadcast contract: app.js reads
// snake_case field names. If anyone removes/renames a json tag, this test
// fails before the UI silently goes blank.
func TestSnapshot_JSONTags(t *testing.T) {
	pct := 87
	remaining := int64(1234567)
	s := Snapshot{
		ModelName:            "general",
		FetchedAt:            1700000000000,
		IntervalRemainingPct: &pct,
		IntervalRemainsMs:    &remaining,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	required := []string{
		"model_name", "fetched_at",
		"interval_remaining_pct", "interval_remains_ms",
		"weekly_remaining_pct", "weekly_remains_ms",
	}
	for _, k := range required {
		if _, ok := got[k]; !ok {
			t.Errorf("Snapshot JSON missing required field %q; got %s", k, string(b))
		}
	}
	if got["model_name"] != "general" {
		t.Errorf("model_name = %v, want general", got["model_name"])
	}
}

func TestPrevSnapshot_ReturnsLatestBefore(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	// Insert three snapshots: oldest, middle, newest
	for i, ts := range []int64{now - 30000, now - 20000, now - 10000} {
		pct := 80 - i // 80, 79, 78
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
	db := openTest(t)
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
	db := openTest(t)
	got, err := db.PrevSnapshot(context.Background(), "general", time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("PrevSnapshot: %v", err)
	}
	if got.FetchedAt != 0 {
		t.Errorf("FetchedAt = %d, want 0 (empty table)", got.FetchedAt)
	}
}

func TestPrevSnapshot_DifferentModel(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	pct := 80
	// Insert ONLY a "video" snapshot. Querying for "general" must NOT return it.
	s := Snapshot{FetchedAt: now - 10000, ModelName: "video", IntervalRemainingPct: &pct}
	if err := db.InsertOne(ctx, s); err != nil {
		t.Fatalf("InsertOne: %v", err)
	}
	got, err := db.PrevSnapshot(ctx, "general", now)
	if err != nil {
		t.Fatalf("PrevSnapshot: %v", err)
	}
	if got.FetchedAt != 0 {
		t.Errorf("FetchedAt = %d, want 0 (video row excluded by model filter)", got.FetchedAt)
	}
}
