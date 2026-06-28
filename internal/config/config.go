package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port           int
	PollInterval   time.Duration
	DBPath         string
	RetentionDays  int
	LogLevel       slog.Level
	KeyringService string
	KeyringUser    string
	APIURL         string
}

func Load() Config {
	return Config{
		PollInterval:   durationEnv("POLL_INTERVAL", 10*time.Second),
		DBPath:         strEnv("DB_PATH", "./data/monitor.db"),
		RetentionDays:  intEnv("RETENTION_DAYS", 31),
		LogLevel:       levelEnv("LOG_LEVEL", slog.LevelInfo),
		KeyringService: strEnv("KEYRING_SERVICE", "minimax-monitor"),
		KeyringUser:    strEnv("KEYRING_USER", "default"),
		APIURL:         strEnv("API_URL", "https://www.minimaxi.com/v1/token_plan/remains"),
	}
}

func strEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func durationEnv(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func levelEnv(key string, def slog.Level) slog.Level {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return def
	}
}
