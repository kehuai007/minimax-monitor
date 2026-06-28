package storage

import (
	"context"
	"encoding/json"
	"fmt"
)

type AlertState struct {
	NotifiedPcts []int `json:"notified_pcts"`
	UpdatedAt    int64 `json:"updated_at"`
}

func (db *DB) GetAlertState(ctx context.Context, model string) (AlertState, error) {
	var raw string
	err := db.QueryRowContext(ctx,
		`SELECT notified_pcts FROM alert_state WHERE model_name = ?`, model,
	).Scan(&raw)
	if err != nil {
		// sql.ErrNoRows is expected on first call
		return AlertState{}, nil
	}
	var st AlertState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return AlertState{}, fmt.Errorf("decode alert_state for %s: %w", model, err)
	}
	return st, nil
}

func (db *DB) SetAlertState(ctx context.Context, model string, st AlertState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
        INSERT INTO alert_state(model_name, notified_pcts, updated_at)
        VALUES (?, ?, ?)
        ON CONFLICT(model_name) DO UPDATE SET
            notified_pcts = excluded.notified_pcts,
            updated_at    = excluded.updated_at
    `, model, string(raw), st.UpdatedAt)
	return err
}

func (db *DB) ClearAlertState(ctx context.Context, model string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM alert_state WHERE model_name = ?`, model)
	return err
}

func (db *DB) ClearAllAlertStates(ctx context.Context) error {
	_, err := db.ExecContext(ctx, `DELETE FROM alert_state`)
	return err
}