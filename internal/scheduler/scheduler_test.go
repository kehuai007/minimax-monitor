package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"minimax-monitor/internal/model"
	"minimax-monitor/internal/storage"
)

type fakeFetcher struct {
	calls atomic.Int32
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) (*model.APIResponse, error) {
	f.calls.Add(1)
	return &model.APIResponse{
		ModelRemains: []model.ModelRemains{{ModelName: "general", CurrentIntervalRemainingPct: 50}},
		BaseResp:     model.BaseResp{StatusCode: 0, StatusMsg: "ok"},
	}, nil
}

type fakeInserter struct {
	inserts atomic.Int32
	prunes  atomic.Int32
}

func (f *fakeInserter) Insert(_ context.Context, _ *model.APIResponse, _ time.Time) error {
	f.inserts.Add(1)
	return nil
}
func (f *fakeInserter) Latest(_ context.Context) ([]storage.Snapshot, error) {
	return nil, nil
}
func (f *fakeInserter) PruneOlderThan(_ context.Context, _ int64) (int64, error) {
	f.prunes.Add(1)
	return 0, nil
}

type fakeBroadcaster struct {
	atomic.Int32
}

func (f *fakeBroadcaster) Broadcast(_ []storage.Snapshot) { f.Add(1) }

func TestRunOnce_FetchAndInsert(t *testing.T) {
	f := &fakeFetcher{}
	i := &fakeInserter{}
	b := &fakeBroadcaster{}
	sc := New(f, i, b, func() (string, error) { return "k", nil },
		10*time.Second, 24*time.Hour, 31)
	if err := sc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if f.calls.Load() != 1 {
		t.Errorf("fetch calls = %d", f.calls.Load())
	}
	if i.inserts.Load() != 1 {
		t.Errorf("inserts = %d", i.inserts.Load())
	}
}

func TestRunOnce_NoKeySkips(t *testing.T) {
	f := &fakeFetcher{}
	i := &fakeInserter{}
	sc := New(f, i, &fakeBroadcaster{}, func() (string, error) { return "", errNoKey }, 0, 0, 0)
	if err := sc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if f.calls.Load() != 0 {
		t.Errorf("should not fetch without key")
	}
}

var errNoKey = &configErr{}

type configErr struct{}

func (c *configErr) Error() string { return "no key" }
