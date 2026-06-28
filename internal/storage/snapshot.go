package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"minimax-monitor/internal/model"
)

type Snapshot struct {
	ID                    int64  `json:"id"`
	FetchedAt             int64  `json:"fetched_at"`
	ModelName             string `json:"model_name"`
	IntervalRemainingPct  *int   `json:"interval_remaining_pct"`
	IntervalStatus        *int   `json:"interval_status"`
	IntervalTotalCount    *int64 `json:"interval_total_count"`
	IntervalUsageCount    *int64 `json:"interval_usage_count"`
	IntervalEndAt         *int64 `json:"interval_end_at"`
	IntervalRemainsMs     *int64 `json:"interval_remains_ms"`
	WeeklyRemainingPct    *int   `json:"weekly_remaining_pct"`
	WeeklyStatus          *int   `json:"weekly_status"`
	WeeklyTotalCount      *int64 `json:"weekly_total_count"`
	WeeklyUsageCount      *int64 `json:"weekly_usage_count"`
	WeeklyEndAt           *int64 `json:"weekly_end_at"`
	WeeklyRemainsMs       *int64 `json:"weekly_remains_ms"`
	RawJSON               string `json:"raw_json"`
}

type Bucket struct {
	T         int64
	Min, Max  float64
	Avg       float64
}

func (db *DB) Insert(ctx context.Context, resp *model.APIResponse, t time.Time) error {
	raw, _ := json.Marshal(resp)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO snapshot(
		fetched_at, model_name,
		interval_remaining_pct, interval_status, interval_total_count, interval_usage_count,
		interval_end_at, interval_remains_ms,
		weekly_remaining_pct, weekly_status, weekly_total_count, weekly_usage_count,
		weekly_end_at, weekly_remains_ms, raw_json
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range resp.ModelRemains {
		if _, err := stmt.ExecContext(ctx,
			t.UnixMilli(), m.ModelName,
			m.CurrentIntervalRemainingPct, m.CurrentIntervalStatus,
			m.CurrentIntervalTotalCount, m.CurrentIntervalUsageCount,
			m.EndTime, m.RemainsTime,
			m.CurrentWeeklyRemainingPct, m.CurrentWeeklyStatus,
			m.CurrentWeeklyTotalCount, m.CurrentWeeklyUsageCount,
			m.WeeklyEndTime, m.WeeklyRemainsTime, string(raw),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) Latest(ctx context.Context) ([]Snapshot, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.fetched_at, s.model_name,
			s.interval_remaining_pct, s.interval_status, s.interval_total_count, s.interval_usage_count,
			s.interval_end_at, s.interval_remains_ms,
			s.weekly_remaining_pct, s.weekly_status, s.weekly_total_count, s.weekly_usage_count,
			s.weekly_end_at, s.weekly_remains_ms, s.raw_json
		FROM snapshot s
		JOIN (
			SELECT model_name, MAX(id) AS max_id
			FROM snapshot GROUP BY model_name
		) m ON s.id = m.max_id
		ORDER BY s.model_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(
			&s.ID, &s.FetchedAt, &s.ModelName,
			&s.IntervalRemainingPct, &s.IntervalStatus, &s.IntervalTotalCount, &s.IntervalUsageCount,
			&s.IntervalEndAt, &s.IntervalRemainsMs,
			&s.WeeklyRemainingPct, &s.WeeklyStatus, &s.WeeklyTotalCount, &s.WeeklyUsageCount,
			&s.WeeklyEndAt, &s.WeeklyRemainsMs, &s.RawJSON,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) History(ctx context.Context, modelName string, fromMs, toMs, bucketMs int64) ([]Bucket, error) {
	if bucketMs <= 0 {
		return nil, fmt.Errorf("bucketMs must be > 0")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT (fetched_at / ?) * ? AS bucket_t,
			MIN(interval_remaining_pct), MAX(interval_remaining_pct), AVG(interval_remaining_pct)
		FROM snapshot
		WHERE model_name = ? AND fetched_at >= ? AND fetched_at < ?
		GROUP BY bucket_t
		ORDER BY bucket_t`, bucketMs, bucketMs, modelName, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.T, &b.Min, &b.Max, &b.Avg); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (db *DB) PruneOlderThan(ctx context.Context, cutoffMs int64) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM snapshot WHERE fetched_at < ?`, cutoffMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
