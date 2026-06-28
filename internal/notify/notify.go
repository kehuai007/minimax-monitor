// Package notify owns outbound alert delivery. The FeishuClient (feishu.go)
// satisfies the Notifier interface, and the AlertEngine (alert_engine.go)
// drives evaluation per scheduler tick.
package notify

import (
	"context"
	"fmt"
	"time"

	"minimax-monitor/internal/storage"
)

// Notification kinds — empty string is the default alert card.
const (
	KindAlert = ""
	KindReset = "reset"
)

type Severity int

const (
	SevInfo     Severity = iota // test card
	SevLow                      // remaining > 50
	SevMid                      // 30..50
	SevHigh                     // 10..30
	SevCritical                 // < 10
)

// SeverityFor maps a remaining-percent integer to its severity band.
func SeverityFor(remaining int) Severity {
	switch {
	case remaining < 10:
		return SevCritical
	case remaining < 30:
		return SevHigh
	case remaining < 50:
		return SevMid
	default:
		return SevLow
	}
}

// Template returns the Feishu interactive card "template" color for this severity.
func (s Severity) Template() string {
	switch s {
	case SevInfo:
		return "blue"
	case SevLow:
		return "green"
	case SevMid:
		return "yellow"
	case SevHigh:
		return "orange"
	case SevCritical:
		return "red"
	default:
		return "grey"
	}
}

// String returns the localized label used in card copy.
func (s Severity) String() string {
	switch s {
	case SevInfo:
		return "测试"
	case SevLow:
		return "低"
	case SevMid:
		return "中"
	case SevHigh:
		return "高"
	case SevCritical:
		return "严重"
	default:
		return "未知"
	}
}

type TrendPoint struct {
	FetchedAt int64 `json:"fetched_at"`
	Remaining int   `json:"remaining"`
}

type Notification struct {
	IsTest                bool         `json:"is_test"`
	Kind                  string       `json:"kind,omitempty"`
	Model                 string       `json:"model"`
	Severity              Severity     `json:"severity"`
	Remaining             int          `json:"remaining"`
	Used                  int          `json:"used"`
	WeeklyRemainingPct    *int         `json:"weekly_remaining_pct,omitempty"`
	Threshold             int          `json:"threshold"`
	PrevNotifiedPct       *int         `json:"prev_notified_pct,omitempty"`
	WindowMaxConsumed     *int         `json:"window_max_consumed,omitempty"`
	IntervalResetAt       *int64       `json:"interval_reset_at,omitempty"`
	IntervalResetRemainMs *int64       `json:"interval_reset_remain_ms,omitempty"`
	WeeklyResetAt         *int64       `json:"weekly_reset_at,omitempty"`
	WeeklyResetRemainMs   *int64       `json:"weekly_reset_remain_ms,omitempty"`
	RecentTrend           []TrendPoint `json:"recent_trend,omitempty"`
	FetchedAt             int64        `json:"fetched_at"`
}

type Notifier interface {
	Send(ctx context.Context, rawURL string, n Notification) error
}

// FormatResetTime formats a unix-ms timestamp for the card.
func FormatResetTime(unixMs int64) string {
	t := time.UnixMilli(unixMs)
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04:05")
	}
	return t.Format("01-02 15:04")
}

// FormatResetRemain formats a duration-until-reset string for the card.
func FormatResetRemain(deltaMs int64) string {
	if deltaMs <= 0 {
		return "已过"
	}
	d := time.Duration(deltaMs) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%d秒后", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		return fmt.Sprintf("%d分%d秒后", m, s)
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return fmt.Sprintf("%d时%d分后", h, m)
	}
	days := int(d / (24 * time.Hour))
	hours := int(d.Hours()) - days*24
	return fmt.Sprintf("%d天%d时后", days, hours)
}

// buildResetNotification composes the reset-window-rolled-over card
// payload. notifiedPcts are the remaining-percentage values at which
// alerts fired during the closing window; both those and prev.
// IntervalRemainingPct are scanned to compute the highest consumption
// reached before reset.
func buildResetNotification(s storage.Snapshot, prev storage.Snapshot,
	notifiedPcts []int, threshold int, trend []TrendPoint, now time.Time) Notification {
	n := Notification{
		Kind:        KindReset,
		Model:       s.ModelName,
		Severity:    SevInfo,
		Remaining:   100,
		Used:        0,
		Threshold:   threshold,
		RecentTrend: trend,
		FetchedAt:   now.UnixMilli(),
	}
	maxConsumed := 0
	if prev.IntervalRemainingPct != nil {
		if v := 100 - *prev.IntervalRemainingPct; v > maxConsumed {
			maxConsumed = v
		}
	}
	for _, r := range notifiedPcts {
		if v := 100 - r; v > maxConsumed {
			maxConsumed = v
		}
	}
	if maxConsumed > 0 {
		v := maxConsumed
		n.WindowMaxConsumed = &v
	}
	if s.IntervalEndAt != nil {
		at := *s.IntervalEndAt
		n.IntervalResetAt = &at
		delta := at - now.UnixMilli()
		n.IntervalResetRemainMs = &delta
	}
	if s.WeeklyEndAt != nil {
		at := *s.WeeklyEndAt
		n.WeeklyResetAt = &at
		delta := at - now.UnixMilli()
		n.WeeklyResetRemainMs = &delta
	}
	if s.WeeklyRemainingPct != nil {
		v := *s.WeeklyRemainingPct
		n.WeeklyRemainingPct = &v
	}
	return n
}
