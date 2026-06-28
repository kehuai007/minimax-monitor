package notify

import (
	"testing"
	"time"
)

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
	loc := time.Local
	ts := time.Date(2026, 6, 28, 16, 45, 55, 0, loc).UnixMilli()
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
