package keyring

import (
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping keyring test in CI")
	}
	svc := "minimax-monitor-test"
	usr := "round-trip"
	_ = Delete(svc, usr) // best-effort cleanup
	s := New(svc, usr)
	if err := s.Set("secret-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "secret-1" {
		t.Errorf("got %q, want secret-1", got)
	}
	if err := Delete(svc, usr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(); err == nil {
		t.Errorf("expected error after delete, got nil")
	}
}