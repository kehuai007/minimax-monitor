package notify

import (
	"testing"
	"time"

	"minimax-monitor/internal/storage"
)

func intPtr(v int) *int        { return &v }
func int64Ptr(v int64) *int64 { return &v }

func TestSeverityFor(t *testing.T) {
	cases := []struct {
		remaining int
		want      Severity
	}{
		{100, SevLow},
		{80, SevLow},
		{79, SevLow},
		{50, SevLow},
		{49, SevMid},
		{30, SevMid},
		{29, SevHigh},
		{10, SevHigh},
		{9, SevCritical},
		{0, SevCritical},
	}
	for _, c := range cases {
		got := SeverityFor(c.remaining)
		if got != c.want {
			t.Errorf("SeverityFor(%d) = %v, want %v", c.remaining, got, c.want)
		}
	}
}

func TestFormatResetTime_Today(t *testing.T) {
	now := time.Now()
	ts := time.Date(now.Year(), now.Month(), now.Day(), 16, 45, 55, 0, now.Location()).UnixMilli()
	got := FormatResetTime(ts)
	want := "16:45:55"
	if got != want {
		t.Errorf("FormatResetTime = %q, want %q", got, want)
	}
}

func TestFormatResetRemain(t *testing.T) {
	cases := []struct {
		name string
		ms   int64
		want string
	}{
		{"past", -1000, "已过"},
		{"seconds", 30 * 1000, "30秒后"},
		{"minSec", 3*time.Minute.Milliseconds() + 42*1000, "3分42秒后"},
		{"hours", 2*time.Hour.Milliseconds() + 15*time.Minute.Milliseconds(), "2时15分后"},
		{"days", 5*24*time.Hour.Milliseconds() + 7*time.Hour.Milliseconds(), "5天7时后"},
	}
	for _, c := range cases {
		got := FormatResetRemain(c.ms)
		if got != c.want {
			t.Errorf("%s: FormatResetRemain(%d) = %q, want %q", c.name, c.ms, got, c.want)
		}
	}
}

func TestSeverityTemplate(t *testing.T) {
	cases := []struct {
		sev  Severity
		want string
	}{
		{SevInfo, "blue"},
		{SevLow, "green"},
		{SevMid, "yellow"},
		{SevHigh, "orange"},
		{SevCritical, "red"},
	}
	for _, c := range cases {
		if got := c.sev.Template(); got != c.want {
			t.Errorf("%v.Template() = %q, want %q", c.sev, got, c.want)
		}
	}
}

func TestBuildResetNotification_FullFields(t *testing.T) {
	pct := 0 // consumed = 0 → remaining = 100 at the moment of reset
	endAt := int64Ptr(time.Now().Add(5 * time.Minute).UnixMilli())
	weeklyEnd := int64Ptr(time.Now().Add(48 * time.Hour).UnixMilli())
	weeklyRem := intPtr(70)
	s := storage.Snapshot{
		ModelName:            "general",
		IntervalRemainingPct: &pct,
		IntervalEndAt:        endAt,
		WeeklyEndAt:          weeklyEnd,
		WeeklyRemainingPct:   weeklyRem,
	}
	prevPct := 13 // consumed = 87 → remaining = 13
	prev := storage.Snapshot{
		ModelName:            "general",
		IntervalRemainingPct: &prevPct,
	}
	now := time.Now()
	n := buildResetNotification(s, prev, []int{20, 18, 15, 13}, 80, nil, now)
	if n.Kind != KindReset {
		t.Errorf("Kind = %q, want %q", n.Kind, KindReset)
	}
	if n.Severity != SevInfo {
		t.Errorf("Severity = %v, want SevInfo", n.Severity)
	}
	if n.Used != 0 || n.Remaining != 100 {
		t.Errorf("Used=%d Remaining=%d, want 0/100", n.Used, n.Remaining)
	}
	if n.Threshold != 80 {
		t.Errorf("Threshold = %d, want 80", n.Threshold)
	}
	if n.WindowMaxConsumed == nil {
		t.Fatal("WindowMaxConsumed = nil, want non-nil")
	}
	if *n.WindowMaxConsumed != 87 {
		t.Errorf("WindowMaxConsumed = %d, want 87 (max of 100-prev=87 and 100-min(notified)=85)", *n.WindowMaxConsumed)
	}
	if n.IntervalResetAt == nil || *n.IntervalResetAt != *endAt {
		t.Errorf("IntervalResetAt not propagated from snapshot")
	}
	if n.WeeklyRemainingPct == nil || *n.WeeklyRemainingPct != 70 {
		t.Errorf("WeeklyRemainingPct not propagated from snapshot")
	}
}

func TestBuildResetNotification_NoNotified_NoPrev_NoWindowMax(t *testing.T) {
	pct := 0
	s := storage.Snapshot{ModelName: "general", IntervalRemainingPct: &pct}
	now := time.Now()
	n := buildResetNotification(s, storage.Snapshot{}, nil, 80, nil, now)
	if n.WindowMaxConsumed != nil {
		t.Errorf("WindowMaxConsumed = %d, want nil (no data to compute)", *n.WindowMaxConsumed)
	}
	if n.Kind != KindReset {
		t.Errorf("Kind = %q, want %q", n.Kind, KindReset)
	}
	if n.Used != 0 || n.Remaining != 100 {
		t.Errorf("Used=%d Remaining=%d, want 0/100", n.Used, n.Remaining)
	}
}

func TestBuildResetNotification_NotifiedHigherThanPrev(t *testing.T) {
	// Previous remaining = 50 (consumed = 50), but notified_pcts shows we
	// once saw consumed=92 (remaining=8). Max should be 92.
	pct := 0
	s := storage.Snapshot{ModelName: "general", IntervalRemainingPct: &pct}
	prevPct := 50
	prev := storage.Snapshot{ModelName: "general", IntervalRemainingPct: &prevPct}
	n := buildResetNotification(s, prev, []int{20, 12, 8}, 80, nil, time.Now())
	if n.WindowMaxConsumed == nil || *n.WindowMaxConsumed != 92 {
		t.Errorf("WindowMaxConsumed = %v, want 92 (from notified 8)", n.WindowMaxConsumed)
	}
}
