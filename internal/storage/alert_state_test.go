package storage

import (
	"context"
	"testing"
)

func TestAlertState_DefaultEmpty(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	st, err := db.GetAlertState(ctx, "general")
	if err != nil {
		t.Fatalf("GetAlertState: %v", err)
	}
	if len(st.NotifiedPcts) != 0 {
		t.Errorf("default NotifiedPcts = %v, want empty", st.NotifiedPcts)
	}
	if st.UpdatedAt != 0 {
		t.Errorf("default UpdatedAt = %d, want 0", st.UpdatedAt)
	}
}

func TestAlertState_RoundTrip(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	want := AlertState{NotifiedPcts: []int{80, 79, 78}, UpdatedAt: 1782561600000}
	if err := db.SetAlertState(ctx, "general", want); err != nil {
		t.Fatalf("SetAlertState: %v", err)
	}
	got, err := db.GetAlertState(ctx, "general")
	if err != nil {
		t.Fatalf("GetAlertState: %v", err)
	}
	if len(got.NotifiedPcts) != 3 || got.NotifiedPcts[0] != 80 || got.NotifiedPcts[1] != 79 || got.NotifiedPcts[2] != 78 {
		t.Errorf("NotifiedPcts = %v, want [80 79 78]", got.NotifiedPcts)
	}
	if got.UpdatedAt != 1782561600000 {
		t.Errorf("UpdatedAt = %d, want 1782561600000", got.UpdatedAt)
	}
}

func TestAlertState_PerModelIsolation(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	_ = db.SetAlertState(ctx, "general", AlertState{NotifiedPcts: []int{80}})
	_ = db.SetAlertState(ctx, "video", AlertState{NotifiedPcts: []int{50}})
	g, _ := db.GetAlertState(ctx, "general")
	v, _ := db.GetAlertState(ctx, "video")
	if g.NotifiedPcts[0] != 80 {
		t.Errorf("general NotifiedPcts = %v", g.NotifiedPcts)
	}
	if v.NotifiedPcts[0] != 50 {
		t.Errorf("video NotifiedPcts = %v", v.NotifiedPcts)
	}
}

func TestAlertState_ClearOne(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	_ = db.SetAlertState(ctx, "general", AlertState{NotifiedPcts: []int{80}})
	_ = db.SetAlertState(ctx, "video", AlertState{NotifiedPcts: []int{50}})
	if err := db.ClearAlertState(ctx, "general"); err != nil {
		t.Fatalf("ClearAlertState: %v", err)
	}
	g, _ := db.GetAlertState(ctx, "general")
	v, _ := db.GetAlertState(ctx, "video")
	if len(g.NotifiedPcts) != 0 {
		t.Errorf("general after clear = %v, want empty", g.NotifiedPcts)
	}
	if v.NotifiedPcts[0] != 50 {
		t.Errorf("video should be untouched, got %v", v.NotifiedPcts)
	}
}

func TestAlertState_ClearAll(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	_ = db.SetAlertState(ctx, "general", AlertState{NotifiedPcts: []int{80}})
	_ = db.SetAlertState(ctx, "video", AlertState{NotifiedPcts: []int{50}})
	if err := db.ClearAllAlertStates(ctx); err != nil {
		t.Fatalf("ClearAllAlertStates: %v", err)
	}
	g, _ := db.GetAlertState(ctx, "general")
	v, _ := db.GetAlertState(ctx, "video")
	if len(g.NotifiedPcts) != 0 || len(v.NotifiedPcts) != 0 {
		t.Errorf("ClearAll did not clear; general=%v video=%v", g.NotifiedPcts, v.NotifiedPcts)
	}
}