package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"minimax-monitor/internal/model"
	"minimax-monitor/internal/storage"
)

type Fetcher interface {
	Fetch(ctx context.Context, key string) (*model.APIResponse, error)
}

type Inserter interface {
	Insert(ctx context.Context, resp *model.APIResponse, t time.Time) error
	Latest(ctx context.Context) ([]storage.Snapshot, error)
	PruneOlderThan(ctx context.Context, cutoffMs int64) (int64, error)
}

type Broadcaster interface {
	Broadcast(snap []storage.Snapshot)
}

type Scheduler struct {
	f              Fetcher
	ins            Inserter
	b              Broadcaster
	keyFn          func() (string, error)
	interval       time.Duration
	pruneEvery     time.Duration
	retentionDays  int
	nowFn          func() time.Time
	mu             sync.Mutex
	running        bool
	consecErrors   int
	lastFetchAt    time.Time
	lastErrMsg     string
}

func New(f Fetcher, ins Inserter, b Broadcaster, keyFn func() (string, error),
	interval, pruneEvery time.Duration, retentionDays int) *Scheduler {
	return &Scheduler{
		f: f, ins: ins, b: b, keyFn: keyFn,
		interval: interval, pruneEvery: pruneEvery, retentionDays: retentionDays,
		nowFn: time.Now,
	}
}

func (sc *Scheduler) RunOnce(ctx context.Context) error {
	key, err := sc.keyFn()
	if err != nil || key == "" {
		slog.Debug("scheduler: no key, skipping tick")
		return nil
	}
	resp, err := sc.f.Fetch(ctx, key)
	sc.mu.Lock()
	sc.lastFetchAt = sc.nowFn()
	if err != nil {
		sc.consecErrors++
		sc.lastErrMsg = err.Error()
		sc.mu.Unlock()
		slog.Warn("fetch failed", "err", err)
		return err
	}
	sc.consecErrors = 0
	sc.lastErrMsg = ""
	sc.mu.Unlock()
	if err := sc.ins.Insert(ctx, resp, sc.nowFn()); err != nil {
		slog.Error("insert failed", "err", err)
		return err
	}
	if snaps, err := sc.ins.Latest(ctx); err == nil && sc.b != nil {
		sc.b.Broadcast(snaps)
	}
	return nil
}

func (sc *Scheduler) Start(ctx context.Context) {
	sc.mu.Lock()
	if sc.running {
		sc.mu.Unlock()
		return
	}
	sc.running = true
	sc.mu.Unlock()
	defer func() {
		sc.mu.Lock()
		sc.running = false
		sc.mu.Unlock()
	}()

	_ = sc.RunOnce(ctx)
	tick := time.NewTicker(sc.interval)
	prune := time.NewTicker(sc.pruneEvery)
	defer tick.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = sc.RunOnce(ctx)
		case <-prune.C:
			cutoff := sc.nowFn().AddDate(0, 0, -sc.retentionDays).UnixMilli()
			if n, err := sc.ins.PruneOlderThan(ctx, cutoff); err == nil && n > 0 {
				slog.Info("pruned old snapshots", "rows", n)
			}
		}
	}
}

func (sc *Scheduler) Stats() (lastFetchAt time.Time, consecErrors int, lastErr string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.lastFetchAt, sc.consecErrors, sc.lastErrMsg
}
