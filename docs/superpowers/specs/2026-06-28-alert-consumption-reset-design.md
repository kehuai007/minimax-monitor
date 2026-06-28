# MiniMax Token Plan Monitor — Alert Consumption-Forward + Reset Notification + Progress Red-Shift

**Date**: 2026-06-28
**Status**: Draft (pending user review)
**Target**: Flip alert threshold semantics from "remaining ≤ X" to
"consumed ≥ X" (forward-looking consumption), make consumption the
prominent field on Feishu cards, send a dedicated reset notification when
the 5-minute interval window rolls over after real usage, add a linear
red-shift to the dashboard progress bar from 80% to 100% consumption, and
fix the "33m · --" scientific-not display.

---

## 1. Goals & Non-Goals

### 1.1 Goals

1. **Threshold semantic flip**: `cfg.Threshold` represents a *consumption*
   percentage, not a remaining percentage. Default 80 means "alert when
   80% of the quota has been consumed" (i.e. remaining ≤ 20%). Existing
   `alert.threshold` config value is preserved; only its interpretation
   changes.
2. **Consumption-forward notification card**: the Feishu interactive card
   must surface the *consumed* percentage as the prominent field (bold
   large text) and the *remaining* percentage as a secondary field. The
   settings threshold label changes to "阈值 (消耗%)".
3. **Reset notification**: when the 5-minute interval window rolls over
   after real usage (previous consumed > 0, current consumed = 0, and
   `interval_end_at` strictly advanced), the engine sends **one
   additional** Feishu card titled "🔄 配额重置 · <model>" with blue
   (info) severity. After sending, dedup state is cleared so the next
   threshold crossing starts a fresh notification cycle.
4. **Progress bar red-shift**: once consumption reaches 80%, the card's
   progress bar fill color and the large percentage number transition
   from the model's accent color (cyan for `general`, purple for `video`)
   linearly toward `#ef4444` (red) as consumption approaches 100%.
5. **Display fix**: when the remaining-time display would render `<1h` as
   e.g. `33m`, suppress the trailing `· --` status (no informative value
   when status is unknown); when ≥ 1h, the previous format remains.

### 1.2 Non-Goals (this v3)

- Multi-channel notifications (email, ntfy, Telegram, etc.) — Feishu only.
- Per-model thresholds — single global threshold (unchanged from v2).
- Weekly-window alerts — only interval window (unchanged from v2).
- Migration of pre-existing `alert_state` rows: the stored `notified_pcts`
  list continues to represent *remaining* integer values; only the
  trigger condition and display conversion change. No row rewrite.
- Reset detection in the absence of a previous snapshot (cold start):
  if the previous snapshot cannot be fetched (DB error or first ever
  tick), no reset notification fires.
- Animated color transitions (CSS `transition` on fill color): the bar
  width already animates over 600ms; we will inject a static color per
  render and let width-anim mask any sudden change. (Adding a separate
  color transition is YAGNI; the existing cubic-bezier width animation
  dominates the visual.)
- Multiple reset notifications in a single window — the `isReset`
  condition is by construction true exactly once per roll-over.
- WebSocket protocol changes — server still emits the same `snapshot`
  message shape; only client-side rendering is new.

---

## 2. Architecture & File Layout

```
internal/
├── notify/
│   ├── notify.go                    # EDITED: Notification.Kind, WindowMaxConsumed, buildResetNotification helper
│   ├── alert_engine.go              # EDITED: threshold → consumed; new reset detection; PrevSnapshot fetch
│   ├── feishu.go                    # EDITED: card emphasis on consumption; new reset card branch
│   ├── alert_engine_test.go         # EDITED: updated threshold semantics + reset tests
│   ├── feishu_test.go               # EDITED: new reset card payload test
│   └── notify_test.go               # EDITED (if any helpers added)
└── storage/
    ├── snapshot.go                  # EDITED: add PrevSnapshot(ctx, model, beforeMs)
    └── snapshot_test.go             # EDITED: new PrevSnapshot tests

internal/server/web/
├── index.html                       # EDITED: alert threshold label/placeholder text
├── app.js                           # EDITED: barColor() + pct-text color, formatRangeMs fix, drop "· --"
└── style.css                        # EDITED: remove hard-coded bar-fill background; allow inline override

cmd/minimax-monitor/main.go          # UNCHANGED (no new wiring)
docs/superpowers/
└── specs/2026-06-28-alert-consumption-reset-design.md   # THIS FILE
```

No new third-party dependencies. No schema migration — `alert_state`
table unchanged.

---

## 3. Data Model

### 3.1 SQLite additions

None. The existing `alert_state.notified_pcts` continues to store
*remaining* integer values (e.g. `[20, 19, 18]` meaning "we notified when
remaining was 20%, 19%, 18%"). No row rewrite, no version bump.

### 3.2 Go types

```go
// internal/notify/notify.go (EDITED)
const (
    KindAlert = ""         // default alert card
    KindReset = "reset"    // interval window rolled over
)

type Notification struct {
    IsTest                bool         `json:"is_test"`
    Kind                  string       `json:"kind,omitempty"`           // NEW
    Model                 string       `json:"model"`
    Severity              Severity     `json:"severity"`
    Remaining             int          `json:"remaining"`
    Used                  int          `json:"used"`
    WeeklyRemainingPct    *int         `json:"weekly_remaining_pct,omitempty"`
    Threshold             int          `json:"threshold"`                // consumed %
    PrevNotifiedPct       *int         `json:"prev_notified_pct,omitempty"`  // NEW SEMANTICS: remaining value; UI shows 100-x as "consumed"
    WindowMaxConsumed     *int         `json:"window_max_consumed,omitempty"` // NEW: highest consumption % seen this window (for reset card)
    IntervalResetAt       *int64       `json:"interval_reset_at,omitempty"`
    IntervalResetRemainMs *int64       `json:"interval_reset_remain_ms,omitempty"`
    WeeklyResetAt         *int64       `json:"weekly_reset_at,omitempty"`
    WeeklyResetRemainMs   *int64       `json:"weekly_reset_remain_ms,omitempty"`
    RecentTrend           []TrendPoint `json:"recent_trend,omitempty"`
    FetchedAt             int64        `json:"fetched_at"`
}
```

```go
// internal/storage/snapshot.go (NEW)
func (db *DB) PrevSnapshot(ctx context.Context, model string, beforeMs int64) (Snapshot, error)
// Returns the snapshot whose fetched_at is the largest value < beforeMs.
// sql.ErrNoRows is mapped to (Snapshot{}, nil) so callers can treat "no
// previous snapshot" as "not a reset".
```

---

## 4. Notification Engine

### 4.1 Reset detection

```go
// internal/notify/alert_engine.go
func isResetTransition(prev, cur storage.Snapshot) bool {
    if prev.IntervalRemainingPct == nil || cur.IntervalRemainingPct == nil {
        return false
    }
    if prev.IntervalEndAt == nil || cur.IntervalEndAt == nil {
        return false
    }
    return *prev.IntervalRemainingPct > 0     // previous tick had usage
        && *cur.IntervalRemainingPct == 0     // current tick is at full quota
        && *cur.IntervalEndAt > *prev.IntervalEndAt   // interval boundary advanced
}
```

Rationale: `interval_end_at` is the absolute epoch-ms of the next
interval boundary. Within one 5-minute window it is constant; on a
window roll-over it jumps forward to the new boundary. The "previous
consumed > 0" condition suppresses notifications for windows where the
user never used anything (the "0%→0% 不算" rule from the product
spec).

### 4.2 New Evaluate loop

```go
func (e *AlertEngine) Evaluate(ctx context.Context, snaps []storage.Snapshot) error {
    cfg := e.cfgFn()
    if !cfg.Enabled || cfg.URL == "" { return nil }
    now := e.nowFn()

    for _, s := range snaps {
        if s.IntervalRemainingPct == nil { continue }
        remaining := *s.IntervalRemainingPct
        consumed := 100 - remaining

        // 1) Reset detection — runs before threshold check so a fresh
        // window with consumed=0 doesn't trigger an alert.
        prev, _ := e.db.PrevSnapshot(ctx, s.ModelName, s.FetchedAt)
        if isResetTransition(prev, s) {
            trend := e.recentTrend(ctx, s.ModelName, now)
            // compute this window's max consumed from notified_pcts + recent trend
            n := buildResetNotification(s, prev, cfg.Threshold, trend, now)
            if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
                slog.Warn("alert reset send failed", "model", s.ModelName, "err", err)
            } else {
                _ = e.db.ClearAlertState(ctx, s.ModelName)
            }
            continue
        }

        // 2) Above threshold (i.e. consumed < threshold) — skip silently
        if consumed < cfg.Threshold { continue }

        // 3) Already notified at this exact remaining pct — skip
        st, _ := e.db.GetAlertState(ctx, s.ModelName)
        if containsInt(st.NotifiedPcts, remaining) { continue }

        // 4) Build + send
        trend := e.recentTrend(ctx, s.ModelName, now)
        n := buildNotification(s, cfg.Threshold, remaining, st.NotifiedPcts, trend, now)
        if err := e.notifier.Send(ctx, cfg.URL, n); err != nil {
            slog.Warn("alert send failed", "model", s.ModelName, "pct", remaining, "err", err)
            continue   // do NOT advance state on failure
        }

        // 5) Advance dedup
        st.NotifiedPcts = appendUniqueSorted(st.NotifiedPcts, remaining)
        st.UpdatedAt = now.UnixMilli()
        _ = e.db.SetAlertState(ctx, s.ModelName, st)
    }
    return nil
}
```

### 4.3 buildResetNotification

```go
func buildResetNotification(s storage.Snapshot, prev storage.Snapshot,
    threshold int, trend []TrendPoint, now time.Time) Notification {
    n := Notification{
        Kind:      KindReset,
        Model:     s.ModelName,
        Severity:  SevInfo,
        Remaining: 100,
        Used:      0,
        Threshold: threshold,
        Trend:     trend,
        FetchedAt: now.UnixMilli(),
    }
    // WindowMaxConsumed = 100 - min(remaining in notified_pcts ∪ prev.remaining)
    var maxConsumed int
    if prev.IntervalRemainingPct != nil {
        maxConsumed = 100 - *prev.IntervalRemainingPct
    }
    // (notified_pcts is not available here without an extra fetch; we use prev
    //  snapshot's remaining as a lower-bound approximation. To be precise we
    //  pull notified_pcts via db.GetAlertState before calling this helper — see
    //  the implementation plan for the exact call sequence.)
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
```

To compute `WindowMaxConsumed` accurately, the caller must first call
`db.GetAlertState` and inspect `notified_pcts` (which are remaining
values at which alerts fired this window); the maximum consumption in
the window is the larger of:
- `100 - *prev.IntervalRemainingPct` (consumed at the moment before reset)
- `100 - min(notified_pcts)` (consumed at each alert)

We pass the resulting max into `buildResetNotification`. The
implementation plan will spell out the exact ordering.

---

## 5. Feishu Client — Card Variants

### 5.1 Alert card (Kind = "")

Fields reordered so consumption is the prominent value:

| # | Field | Format |
|---|---|---|
| 1 | 模型 | `general` |
| 2 | 触发时间 | `2026-06-28 18:42:13` |
| 3 | **消耗** | **`80%`** (bold large) |
| 4 | 剩余 | 20% |
| 5 | 阈值 | ≥80% |
| 6 | 上次告警 (消耗) | 79% (computed `100 - *prev_notified_remaining`); `—` if none |
| 7 | 区间重置 | `<time> (<remain>)` |
| 8 | 周重置 | `<time> (<remain>)` |
| 9 | 本周剩余 | 87% |

Title: `⚠️ 配额告警 · <model>` (unchanged).
Template color: based on `Severity` (green/yellow/orange/red) — unchanged.

### 5.2 Reset card (Kind = "reset")

| # | Field | Format |
|---|---|---|
| 1 | 模型 | `general` |
| 2 | 触发时间 | `2026-06-28 18:45:00` |
| 3 | 消耗 | 0% |
| 4 | 剩余 | 100% |
| 5 | 本周期最高消耗 | 87% (or `—` if no alerts this window) |
| 6 | 区间重置 | `<time> (<remain>)` |
| 7 | 周重置 | `<time> (<remain>)` |
| 8 | 本周剩余 | 87% |

Title: `🔄 配额重置 · <model>`.
Template color: `blue` (SevInfo).
Note line: `区间已重置,下次告警阈值 ≥ <threshold>% 消耗时触发。`

### 5.3 Test card

Unchanged (SevInfo, blue, `[测试]` prefix). Keeps using alert card
layout but `IsTest = true`.

### 5.4 Implementation — single card builder

`buildCardPayload(n)` inspects `n.Kind`:

```go
func buildCardPayload(n Notification) map[string]any {
    if n.Kind == KindReset {
        return buildResetCard(n)
    }
    return buildAlertCard(n)
}
```

Both helpers share field-rendering utilities for time and pct fields.

---

## 6. HTTP API

No endpoint changes. PUT body still accepts `{enabled, url, threshold}`
and the validation `0 < threshold ≤ 100` is preserved. The numeric
value of `threshold` is now interpreted as consumed %, but the wire
format is identical — clients see no protocol change.

---

## 7. Scheduler Integration

Unchanged. `Scheduler.RunOnce` already fans out `alerter.Evaluate` in a
goroutine after `Broadcast`. The reset notification uses the same
Feishu HTTP path with retry; worst-case latency ~14s is the same as for
threshold alerts.

---

## 8. Web UI

### 8.1 Settings modal — threshold label

```html
<div class="form-row">
  <label for="alertThreshold">阈值 (消耗%)</label>
  <input id="alertThreshold" type="number" min="1" max="100" value="80" />
  <small class="muted">消耗达到该值时告警;每再消耗 1% 再告警一次</small>
</div>
```

### 8.2 Progress bar — red shift

`renderCards` computes a per-card color and applies it inline to both
`.bar-fill` and `.pct`:

```js
function barColor(model, consumed) {
  const accent = ACCENT[model] || '#00d4ff';
  if (consumed < 80) return accent;
  const t = Math.min(1, (consumed - 80) / 20);   // 0..1
  const a = hexToRgb(accent);
  const R = [239, 68, 68];                        // --status-bad
  const r = Math.round(a[0] + (R[0] - a[0]) * t);
  const g = Math.round(a[1] + (R[1] - a[1]) * t);
  const b = Math.round(a[2] + (R[2] - a[2]) * t);
  return `rgb(${r},${g},${b})`;
}

function hexToRgb(h) {
  const m = h.replace('#','');
  return [parseInt(m.slice(0,2),16), parseInt(m.slice(2,4),16), parseInt(m.slice(4,6),16)];
}
```

Template change:

```js
card.innerHTML = `
  <h3>${name} <span class="pct-kind">已用</span></h3>
  <div class="pct" style="color:${color}">${consumed}%</div>
  <div class="bar"><div class="bar-fill" style="width:${consumed}%;background:${color}"></div></div>
  <div class="meta"><span>区间</span><b>${formatRangeMs(remainsMs)}${iStatus === '--' ? '' : ' · ' + iStatus}</b></div>
  <div class="meta"><span>本周</span><b>${consumedWeekly == null ? '--' : consumedWeekly}% · ${wStatus}</b></div>
`;
```

The CSS rule `.bar-fill { background: var(--accent); }` is dropped (the
inline style wins).

### 8.3 Time display — `<1h` drop status

```js
const formatRangeMs = (ms) => {
  const s = Math.round(ms / 1000);
  if (s < 60) return s + 's';
  if (s < 3600) return Math.floor(s / 60) + 'm';          // <1h, no · status
  return Math.floor(s / 3600) + 'h ' + (s % 3600 ? Math.floor((s % 3600) / 60) + 'm' : '');
};
```

For the status suffix, the template change above already conditionally
appends ` · <status>` only when the status is informative (not `'--'`).

---

## 9. main.go Wiring

Unchanged. No new fields, no new constructor args.

---

## 10. Error Handling

| Failure | Behavior |
|---|---|
| `db.PrevSnapshot` returns no rows (cold start) | `isResetTransition` returns false; no reset notification |
| `db.PrevSnapshot` returns DB error | log warn, treat as no-reset; continue threshold check |
| `db.GetAlertState` returns DB error inside reset path | log warn; reset notification still sends with `WindowMaxConsumed = nil` (UI shows `—`) |
| Reset send fails | log warn, do NOT clear `alert_state`; subsequent ticks may re-fire reset once PrevSnapshot still indicates the transition (acceptable; reset is rare) |
| Threshold send fails | unchanged — log warn, do not advance state |
| Missing `IntervalEndAt` on either prev or cur snapshot | `isResetTransition` returns false (cannot verify window roll-over) |

---

## 11. Testing Strategy

| File | New / Edited Tests |
|---|---|
| `internal/storage/snapshot_test.go` | `PrevSnapshot_ReturnsLatestBefore`; `PrevSnapshot_NoRows`; `PrevSnapshot_DifferentModel` |
| `internal/notify/alert_engine_test.go` | EDIT: `TestAlertEngine_CrossingThreshold_OneCall` now uses `snap("general", 20)` (consumed=80, threshold=80) and asserts `c.Used == 80`; EDIT `TestAlertEngine_DropBy1_NewCallEachTime` uses `[20,19,18,17]`. NEW: `TestAlertEngine_ResetTransition_FiresReset` (prev consumed>0, cur=0, end_at advanced → 1 call with `Kind=="reset"`, `Severity==SevInfo`); NEW `TestAlertEngine_ResetTransition_NoUsage_DoesNotFire` (prev remaining=100, cur=100, end_at advanced → 0 calls); NEW `TestAlertEngine_ResetTransition_SameWindow_DoesNotFire` (end_at unchanged → 0 calls); NEW `TestAlertEngine_ResetAfterThreshold_ClearsThenRefires` (consumed=80 → notify; reset transition; consumed=80 again → notify); NEW `TestAlertEngine_ResetCard_WindowMaxConsumed` |
| `internal/notify/feishu_test.go` | NEW `TestBuildCardPayload_AlertCard_EmphasizesUsed` (asserts "**消耗**" field exists and appears before "剩余"); NEW `TestBuildCardPayload_ResetCard_TitleAndFields` (asserts title contains "🔄 配额重置", template is "blue", field "本周期最高消耗" present); EDIT existing assertion in `TestFeishuClient_Send_Success` to construct a `Kind:"reset"` notification and confirm the card is well-formed |
| `internal/notify/notify_test.go` | NEW `TestFormatResetRemain_*` unchanged; NEW `TestBuildResetNotification_*` (verify `Kind`, `Used=0`, `Remaining=100`, optional fields populated from snapshot) |

Test command: `go test ./...` from repo root.

---

## 12. Acceptance Criteria

1. With `alert.threshold = 80` and a model whose consumption climbs
   79% → 80% → 81% → 82% over four ticks, exactly 3 alert cards fire
   (at 80, 81, 82) and the card body shows **消耗 80%** (bold) as the
   prominent value.
2. With consumption holding steady at 80% for N ticks, exactly 1 card
   fires (dedup).
3. When the interval window rolls over and the previous tick had
   consumed > 0 (e.g. consumed = 87%), one reset card fires with title
   `🔄 配额重置 · <model>`, severity `SevInfo` (blue), and field
   "本周期最高消耗: 87%". No threshold alert fires on that tick
   (consumed = 0 < threshold).
4. After the reset card fires, the next threshold-crossing tick (e.g.
   consumed = 80 again) fires a fresh alert card.
5. When consumption rolls over from 0 → 0 across a window boundary
   (no real usage), no reset card fires.
6. The dashboard progress bar's fill color and the large percentage
   number are cyan/purple when consumption < 80%, and transition
   linearly to red as consumption rises from 80% to 100%. At
   consumption = 90% the rendered color is mid-way between accent and
   `#ef4444`.
7. The dashboard's "区间" meta line shows `33m` (no `· --`) when
   remaining-time < 1h and status is unknown; shows `1h 30m · 活跃`
   when remaining-time ≥ 1h and status is known.
8. The settings modal label reads `阈值 (消耗%)` and the help text
   reads `消耗达到该值时告警;每再消耗 1% 再告警一次`.
9. `go test ./...` passes.

---

## 13. Risks

| Risk | Mitigation |
|---|---|
| Threshold meaning flip confuses users who had set a v2 value of e.g. 80 meaning "remaining ≤ 80%" | README will be updated in the same commit to clarify the new semantics; the alert section copy is rewritten accordingly |
| `PrevSnapshot` may return a snapshot from a different 5-minute window if tick interval > reset window | Tick interval (10s) is far smaller than the 5-min window, so the immediately-previous snapshot is always in the same window; the `interval_end_at` strict-greater check still catches the boundary |
| Reset notification fires on a no-usage boundary because some other metric (e.g. weekly) caused `interval_end_at` to advance | The API returns `interval_end_at` independently of weekly metrics; verified to advance only on the 5-min roll-over. If weekly coupled behaviour ever appears, an additional check (weekly_end_at unchanged) could be added — YAGNI for now |
| `WindowMaxConsumed` may be inaccurate if `notified_pcts` was cleared but the user kept consuming after the last alert | Use `100 - *prev.IntervalRemainingPct` (the most recent consumption reading before reset) as the upper-bound source of truth; `notified_pcts` adds only the alerts that fired. The implementation plan computes max(notified_consumed, prev_consumed) |
| Browser repaint cost from per-card `barColor` call on every WS message | Each card runs 1 hex parse + 1 lerp; negligible. No memoization needed for ≤4 models |
| `formatRangeMs` returning `"33m"` (no `· --`) when status IS meaningful but < 1h loses information | Current API path shows `iStatus` as `活跃` (status==1) only when remaining-time > 0; for <1h to <5min remaining the status often flips to `3 (未活动)`. Per the product spec the user explicitly chose to drop status below 1h — accepted trade-off |