package storage

import (
	"context"
	"testing"
)

func TestAlertConfig_Default(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	cfg, err := db.GetAlertConfig(ctx)
	if err != nil {
		t.Fatalf("GetAlertConfig: %v", err)
	}
	if cfg.Enabled != false {
		t.Errorf("default Enabled = %v, want false", cfg.Enabled)
	}
	if cfg.URL != "" {
		t.Errorf("default URL = %q, want \"\"", cfg.URL)
	}
	if cfg.Threshold != 80 {
		t.Errorf("default Threshold = %d, want 80", cfg.Threshold)
	}
}

func TestAlertConfig_RoundTrip(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	want := AlertConfig{Enabled: true, URL: "https://open.feishu.cn/open-apis/bot/v2/hook/abc", Threshold: 75}
	if err := db.SetAlertConfig(ctx, want); err != nil {
		t.Fatalf("SetAlertConfig: %v", err)
	}
	got, err := db.GetAlertConfig(ctx)
	if err != nil {
		t.Fatalf("GetAlertConfig: %v", err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
}

func TestAlertConfig_OverwritePreservesAllFields(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	_ = db.SetAlertConfig(ctx, AlertConfig{Enabled: true, URL: "u1", Threshold: 90})
	if err := db.SetAlertConfig(ctx, AlertConfig{Enabled: false, URL: "u2", Threshold: 60}); err != nil {
		t.Fatalf("SetAlertConfig: %v", err)
	}
	got, _ := db.GetAlertConfig(ctx)
	want := AlertConfig{Enabled: false, URL: "u2", Threshold: 60}
	if got != want {
		t.Errorf("after overwrite = %+v, want %+v", got, want)
	}
}