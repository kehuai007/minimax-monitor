package storage

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS snapshot (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    fetched_at                  INTEGER NOT NULL,
    model_name                  TEXT    NOT NULL,
    interval_remaining_pct      INTEGER,
    interval_status             INTEGER,
    interval_total_count        INTEGER,
    interval_usage_count        INTEGER,
    interval_end_at             INTEGER,
    interval_remains_ms         INTEGER,
    weekly_remaining_pct        INTEGER,
    weekly_status               INTEGER,
    weekly_total_count          INTEGER,
    weekly_usage_count          INTEGER,
    weekly_end_at               INTEGER,
    weekly_remains_ms           INTEGER,
    raw_json                    TEXT
);
CREATE INDEX IF NOT EXISTS idx_snap_model_time
    ON snapshot(model_name, fetched_at DESC);
`

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-64000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if _, err := sqlDB.Exec(schema); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{DB: sqlDB}, nil
}
