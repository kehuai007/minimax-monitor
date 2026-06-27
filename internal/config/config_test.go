package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("RETENTION_DAYS", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("KEYRING_SERVICE", "")
	t.Setenv("KEYRING_USER", "")
	t.Setenv("API_URL", "")

	c := Load()
	if c.PollInterval != 10*time.Second {
		t.Errorf("PollInterval = %v, want 10s", c.PollInterval)
	}
	if c.DBPath != "./data/monitor.db" {
		t.Errorf("DBPath = %q, want ./data/monitor.db", c.DBPath)
	}
	if c.RetentionDays != 31 {
		t.Errorf("RetentionDays = %d, want 31", c.RetentionDays)
	}
	if c.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", c.LogLevel)
	}
	if c.KeyringService != "minimax-monitor" || c.KeyringUser != "default" {
		t.Errorf("Keyring = %s/%s", c.KeyringService, c.KeyringUser)
	}
	if c.APIURL != "https://www.minimaxi.com/v1/token_plan/remains" {
		t.Errorf("APIURL = %q", c.APIURL)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "30s")
	t.Setenv("DB_PATH", "/tmp/x.db")
	t.Setenv("RETENTION_DAYS", "7")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("KEYRING_SERVICE", "svc")
	t.Setenv("KEYRING_USER", "u")
	t.Setenv("API_URL", "https://example.com/api")

	c := Load()
	if c.PollInterval != 30*time.Second || c.DBPath != "/tmp/x.db" ||
		c.RetentionDays != 7 || c.LogLevel != slog.LevelDebug ||
		c.KeyringService != "svc" || c.KeyringUser != "u" ||
		c.APIURL != "https://example.com/api" {
		t.Errorf("override mismatch: %+v", c)
	}
}

func TestLoad_InvalidIntervalFallsBack(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "garbage")
	c := Load()
	if c.PollInterval != 10*time.Second {
		t.Errorf("invalid interval = %v, want default 10s", c.PollInterval)
	}
}
