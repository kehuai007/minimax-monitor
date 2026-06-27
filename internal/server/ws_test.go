package server

import (
	"testing"
)

func TestWSHub_EmptyBroadcast(t *testing.T) {
	h := NewWSHub()
	if h.snap != nil {
		t.Fatalf("expected snap nil, got %v", h.snap)
	}
	h.Broadcast(nil)
}