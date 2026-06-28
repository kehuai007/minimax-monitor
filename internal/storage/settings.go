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