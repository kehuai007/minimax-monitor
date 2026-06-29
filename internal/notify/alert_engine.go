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

// isResetTransition reports whether `cur` represents the moment the
// 5-minute interval window rolled over after real usage. Conditions:
//   - previous snapshot had real usage (consumed > 0, i.e. remaining < 100)
//   - current snapshot is fresh (consumed == 0, i.e. remaining == 100)
//   - interval_end_at strictly advanced (new window boundary)
// All three must hold; missing IntervalRemainingPct or IntervalEndAt on
// either side disables detection.
//
// Predicate note: integer pct domain is 0..100. `*prev.IntervalRemainingPct > 0`
// (consumed > 0) and `*prev.IntervalRemainingPct < 100` (remaining < 100) are
// equivalent at this resolution — a value of 0 means consumed=100 which is
// impossible, and `< 100` reads more clearly as "still has headroom".
func isResetTransition(prev, cur storage.Snapshot) bool {
	if prev.IntervalRemainingPct == nil || cur.IntervalRemainingPct == nil {
		return false
	}
	if prev.IntervalEndAt == nil || cur.IntervalEndAt == nil {
		return false
	}
	return *prev.IntervalRemainingPct < 100 &&
		*cur.IntervalRemainingPct == 100 &&
		*cur.IntervalEndAt > *prev.IntervalEndAt
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
		consumed := 100 - remaining

		// 1) Reset transition detection — runs before threshold check so
		// a fresh window with consumed=0 doesn't accidentally trigger.
		prev, err := e.db.PrevSnapshot(ctx, s.ModelName, s.FetchedAt)
		if err != nil {
			// Surface transient DB errors (SQLite locked, schema drift,
			// disk full) instead of silently swallowing them: a zero-value
			// prev would make isResetTransition return false and the reset
			// notification would never fire. We log and fall through to
			// the threshold branch so the engine can still alert.
			slog.Warn("alert: prev snapshot lookup failed",
				"model", s.ModelName, "err", err)
			// continue to threshold branch — do NOT skip the snapshot entirely
		}
		if isResetTransition(prev, s) {
			trend := e.recentTrend(ctx, s.ModelName, now)
			st, _ := e.db.GetAlertState(ctx, s.ModelName)
			n := buildResetNotification(s, prev, st.NotifiedPcts, cfg.Threshold, trend, now)
			if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
				slog.Warn("alert reset send failed",
					"model", s.ModelName, "err", err)
				// do NOT clear state on failure — next tick may retry
			} else {
				// ClearAlertState failure after a successful send is a
				// state-machine break: the next tick's threshold crossing
				// may dedup against stale NotifiedPcts from the prior
				// window and silently skip. Log loudly (error level) so
				// operators see this, but still fall through to the
				// threshold branch below so any concurrent threshold
				// crossing on this snapshot can still fire.
				if err := e.db.ClearAlertState(ctx, s.ModelName); err != nil {
					slog.Error("alert: clear state after reset failed",
						"model", s.ModelName, "err", err)
				}
				continue
			}
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
	}
	return nil
}

// SendTest dispatches a test card mirroring a real threshold alert. It uses
// the latest snapshot's full data (remaining, weekly, reset times, 10-min
// trend, previous notified pct) so the user can verify the card layout that
// production alerts will produce. Only IsTest and Severity differ from a real
// alert: IsTest=true causes the title prefix to switch to "[测试]" and the
// footer note to read "这是测试消息..."; Severity is forced to SevInfo so the
// card template color is blue regardless of the snapshot's remaining value.
func (e *AlertEngine) SendTest(ctx context.Context) (int64, error) {
	cfg := e.cfgFn()
	if !cfg.Enabled || cfg.URL == "" {
		return 0, ErrConfigMissing
	}
	snaps, err := e.db.Latest(ctx)
	if err != nil {
		return 0, err
	}

	now := e.nowFn()

	// Prefer the first latest snapshot with a real remaining value so the
	// test card shows actual data. Fall back to a "general" placeholder at
	// 100% remaining so the card still renders when no snapshot exists yet
	// (e.g. right after install, before the first poll completes).
	var s storage.Snapshot
	picked := false
	for _, snap := range snaps {
		if snap.IntervalRemainingPct != nil {
			s = snap
			picked = true
			break
		}
	}
	if !picked {
		pct := 100
		s = storage.Snapshot{
			ModelName:            "general",
			IntervalRemainingPct: &pct,
			FetchedAt:            now.UnixMilli(),
		}
	}

	var trend []TrendPoint
	var prevNotified []int
	if picked {
		trend = e.recentTrend(ctx, s.ModelName, now)
		if st, err := e.db.GetAlertState(ctx, s.ModelName); err == nil {
			prevNotified = st.NotifiedPcts
		}
	}

	n := buildNotification(s, cfg.Threshold, *s.IntervalRemainingPct, prevNotified, trend, now)
	n.IsTest = true
	n.Severity = SevInfo
	n.Kind = KindAlert

	if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
		return 0, err
	}
	return now.UnixMilli(), nil
}

// ErrConfigMissing is returned when SendTest is invoked with alerts disabled
// or no URL configured.
var ErrConfigMissing = &alertError{"config_missing"}

type alertError struct{ msg string }

func (e *alertError) Error() string { return e.msg }

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
		Model:       s.ModelName,
		Severity:    SeverityFor(remaining),
		Remaining:   remaining,
		Used:        100 - remaining,
		Threshold:   threshold,
		RecentTrend: trend,
		FetchedAt:   now.UnixMilli(),
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