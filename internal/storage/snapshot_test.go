package storage

import (
	"context"
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
	// t0 aligned to 30s bucket grid; 9 rows over 90s with values 10,20,...,90
	t0 := time.UnixMilli(1700000010000)
	// buckets: [t0, t0+30s): 10,20,30 → min=10 avg=20 max=30
	//          [t0+30s, t0+60s): 40,50,60 → min=40 avg=50 max=60
	//          [t0+60s, t0+90s): 70,80,90 → min=70 avg=80 max=90
	for i := 0; i < 9; i++ {
		resp := &model.APIResponse{
			ModelRemains: []model.ModelRemains{{
				ModelName: "general", CurrentIntervalRemainingPct: 10 + i*10,
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
	if buckets[0].Min != 10 || buckets[0].Max != 30 || buckets[0].Avg != 20 {
		t.Errorf("bucket 0 = %+v", buckets[0])
	}
	if buckets[1].Min != 40 || buckets[1].Max != 60 || buckets[1].Avg != 50 {
		t.Errorf("bucket 1 = %+v", buckets[1])
	}
	if buckets[2].Min != 70 || buckets[2].Max != 90 || buckets[2].Avg != 80 {
		t.Errorf("bucket 2 = %+v", buckets[2])
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
