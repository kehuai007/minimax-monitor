package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"minimax-monitor/internal/apiclient"
	"minimax-monitor/internal/config"
	"minimax-monitor/internal/keyring"
	"minimax-monitor/internal/notify"
	"minimax-monitor/internal/scheduler"
	"minimax-monitor/internal/server"
	"minimax-monitor/internal/storage"
	"minimax-monitor/internal/version"
)

func main() {
	port := flag.Int("p", 13337, "listen port")
	flag.Parse()
	slog.Info("starting minimax-monitor", "version", version.Version, "port", *port)
	cfg := config.Load()
	setupLogging(cfg.LogLevel)

	absDB, _ := filepath.Abs(cfg.DBPath)
	dir := filepath.Dir(absDB)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("mkdir db dir", "err", err)
		os.Exit(1)
	}
	db, err := storage.Open(absDB)
	if err != nil {
		slog.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	store := keyring.New(cfg.KeyringService, cfg.KeyringUser)
	hub := server.NewWSHub()
	cli := apiclient.New(cfg.APIURL)

	sched := scheduler.New(cli, db, hub,
		func() (string, error) { return store.Get() },
		cfg.PollInterval, 24*time.Hour, cfg.RetentionDays)

	srv := server.New(db, store)
	srv.Hub = hub
	srv.DBPath = absDB
	srv.PollInterval = cfg.PollInterval
	srv.Stats = sched.Stats
	srv.Validator = func(ctx context.Context, key string) error {
		_, err := cli.Fetch(ctx, key)
		return err
	}
	srv.OnKeyChange = func() { /* scheduler is already running and re-checks keyFn each tick */ }

	// Wire Feishu notifier and alert engine.
	feishu := notify.NewFeishuClient()
	alertCfgFn := func() storage.AlertConfig {
		cfg, err := db.GetAlertConfig(context.Background())
		if err != nil {
			slog.Warn("get alert config", "err", err)
		}
		return cfg
	}
	engine := notify.NewAlertEngine(db, feishu, alertCfgFn)
	sched.SetAlerter(engine)

	srv.AlertConfig = alertCfgFn
	srv.AlertTest = engine.SendTest

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Start(rootCtx)

	addr := fmt.Sprintf("0.0.0.0:%d", *port)
	slog.Info("listening", "addr", addr, "db", absDB)

	go func() {
		if err := srv.Run(addr); err != nil && err != http.ErrServerClosed {
			slog.Error("server", "err", err)
			cancel()
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("shutting down")
	cancel()
}

func setupLogging(level slog.Level) {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}