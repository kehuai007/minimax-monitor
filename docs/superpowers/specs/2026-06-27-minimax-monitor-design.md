# MiniMax Token Plan Monitor — Design Spec

**Date**: 2026-06-27
**Status**: Draft (pending user review)
**Target**: Single-binary, cross-platform dashboard that polls the MiniMax
`/v1/token_plan/remains` API, persists 31 days of history, and visualizes it on
a local web dashboard.

---

## 1. Goals & Non-Goals

### 1.1 Goals
- Periodically (every 10s) fetch the MiniMax token-plan quota API.
- Persist snapshots for 31 days in embedded SQLite.
- Serve a local web dashboard (dark theme) with:
  - Per-model live cards (interval + weekly remaining %).
  - Per-model historical trend charts (1h / 6h / 24h / 7d / 31d).
  - Settings panel for managing the API key.
- Ship as a single static binary with web assets embedded; cross-platform
  (linux/amd64, linux/arm64, darwin/arm64, windows/amd64) with **CGO disabled**.
- Listen on `0.0.0.0:13337` by default; override with `-p <port>`.

### 1.2 Non-Goals (v1)
- Multi-account / multi-key support.
- Alerting (webhook, email, etc.) — deferred to v2.
- Authentication for the dashboard — listener binding is the only access guard.
- Long-term metrics / TimescaleDB / external storage.

---

## 2. Tech Stack

| Concern | Choice | Notes |
|---|---|---|
| Language | Go 1.22+ | stdlib + minimal deps |
| HTTP framework | `gin-gonic/gin` | per user request |
| WebSocket | `coder/websocket` | clean API, no gorilla baggage |
| SQLite driver | `modernc.org/sqlite` | pure-Go → CGO_ENABLED=0 |
| Keyring | `zalando/go-keyring` | cross-platform (Win/macOS/Linux) |
| Config | `os.Getenv` + `flag` stdlib | no external dep |
| Logging | `log/slog` (stdlib) | structured |
| Static assets | `//go:embed` (stdlib) | embedded FS |
| Frontend | Vanilla HTML/CSS/JS + ECharts 5 | no build step, vendor ECharts offline |

**Build constraints**:
- `CGO_ENABLED=0` for all targets.
- `go build -trimpath -ldflags="-s -w"`.
- Output directory: `dist/`.

---

## 3. Directory Structure

```
minimax-monitor/
├── cmd/
│   └── minimax-monitor/
│       └── main.go                  # entry: flag parsing, wiring, lifecycle
├── internal/
│   ├── config/
│   │   └── config.go                # Load() from env
│   ├── apiclient/
│   │   ├── client.go                # GET /v1/token_plan/remains
│   │   └── client_test.go           # httptest mock
│   ├── keyring/
│   │   └── keyring.go               # Get/Set/Delete wrapper
│   ├── storage/
│   │   ├── sqlite.go                # Open + PRAGMA + schema
│   │   ├── snapshot.go              # Insert / Latest / History / Prune
│   │   └── storage_test.go
│   ├── scheduler/
│   │   └── scheduler.go             # 10s tick + 24h prune
│   ├── server/
│   │   ├── server.go                # gin engine + routes
│   │   ├── handlers_api.go          # /api/status, /api/models, /api/history
│   │   ├── handlers_settings.go     # /api/settings/key
│   │   ├── handlers_ws.go           # /api/ws
│   │   └── embed.go                 # //go:embed all:web
│   └── model/
│       └── types.go                 # request/response/struct
├── web/
│   ├── index.html                   # SPA shell
│   ├── app.js                       # WS client, charts, settings modal
│   ├── style.css                    # dark theme tokens
│   └── vendor/
│       ├── echarts.min.js           # vendored, ~1MB
│       ├── inter-latin.woff2        # optional
│       └── jetbrains-mono-latin.woff2
├── dist/                            # build output (gitignored)
├── docs/
│   └── superpowers/
│       └── specs/
│           └── 2026-06-27-minimax-monitor-design.md
├── go.mod
├── go.sum
├── build.bat                        # Windows: build current platform
├── build-cross.bat                  # Windows: cross-compile all targets
├── Makefile                         # Linux/macOS convenience
├── .env.example
├── .gitignore
└── README.md
```

---

## 4. Data Model

### 4.1 API response (Go struct)
```go
// internal/model/types.go
package model

type BaseResp struct {
    StatusCode int    `json:"status_code"`
    StatusMsg  string `json:"status_msg"`
}

type ModelRemains struct {
    ModelName                  string `json:"model_name"`
    StartTime                  int64  `json:"start_time"`
    EndTime                    int64  `json:"end_time"`
    RemainsTime                int64  `json:"remains_time"`
    CurrentIntervalTotalCount  int64  `json:"current_interval_total_count"`
    CurrentIntervalUsageCount  int64  `json:"current_interval_usage_count"`
    CurrentIntervalRemainingPct int   `json:"current_interval_remaining_percent"`
    CurrentIntervalStatus      int    `json:"current_interval_status"`
    CurrentWeeklyTotalCount    int64  `json:"current_weekly_total_count"`
    CurrentWeeklyUsageCount    int64  `json:"current_weekly_usage_count"`
    WeeklyStartTime            int64  `json:"weekly_start_time"`
    WeeklyEndTime              int64  `json:"weekly_end_time"`
    WeeklyRemainsTime          int64  `json:"weekly_remains_time"`
    CurrentWeeklyStatus        int    `json:"current_weekly_status"`
    CurrentWeeklyRemainingPct  int    `json:"current_weekly_remaining_percent"`
}

type APIResponse struct {
    ModelRemains []ModelRemains `json:"model_remains"`
    BaseResp     BaseResp       `json:"base_resp"`
}
```

### 4.2 SQLite schema
```sql
CREATE TABLE IF NOT EXISTS snapshot (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    fetched_at                  INTEGER NOT NULL,   -- unix ms
    model_name                  TEXT    NOT NULL,
    interval_remaining_pct      INTEGER,
    interval_status             INTEGER,
    interval_total_count        INTEGER,
    interval_usage_count        INTEGER,
    interval_end_at             INTEGER,
    interval_remains_ms         INTEGER,
    weekly_remaining_pct        INTEGER,
    weekly_status               INTEGER,
    weekly_total_count          INTEGER,
    weekly_usage_count          INTEGER,
    weekly_end_at               INTEGER,
    weekly_remains_ms           INTEGER,
    raw_json                    TEXT
);

CREATE INDEX IF NOT EXISTS idx_snap_model_time
    ON snapshot(model_name, fetched_at DESC);
```

### 4.3 PRAGMAs (on open)
- `journal_mode = WAL`
- `synchronous = NORMAL`
- `temp_store = MEMORY`
- `cache_size = -64000` (64MB)
- `foreign_keys = ON`

### 4.4 Data volume estimate
- 10s × 8640 = 86,400 rows/day/model
- × 2 models × 31 days ≈ 5.36M rows max
- With WAL + index, single-row insert is sub-ms; bulk insert 1 tx per fetch
  keeps the scheduler loop < 1ms overhead.

---

## 5. Data Flow

### 5.1 Scheduler loop (10s tick)
```
on startup:
  if keyring missing API key:
    log warn "API key not configured, awaiting /api/settings/key"
    return (do not start tick)
  else:
    start tick loop

every 10s:
  data, err := apiclient.Fetch(ctx)
  if err: log + continue
  storage.Insert(data, t)
  wsHub.Broadcast(snapshot)
```

### 5.2 Prune loop (24h tick)
```
DELETE FROM snapshot WHERE fetched_at < (now_ms - 31*24*3600*1000)
```

### 5.3 WS hub
- In-memory map of active connections.
- On Insert, marshal the latest row per model and broadcast JSON to all
  connected clients.
- On client disconnect, remove from map; do not error the broadcast.

---

## 6. API Design

### 6.1 REST endpoints

| Method | Path | Body / Query | Response |
|---|---|---|---|
| GET | `/api/healthz` | — | `200 ok` |
| GET | `/api/status` | — | `200 {keyring_configured, last_fetch_at, last_error, poll_interval, db_size_mb}` |
| GET | `/api/models` | — | `200 [{model_name, latest: {...}}]` |
| GET | `/api/history` | `?model=general&range=24h&bucket=auto` | `200 {model, range, bucket_sec, points: [{t, min, max, avg}]}` |
| POST | `/api/settings/key` | `{"api_key": "sk-cp-..."}` | `200 {ok:true}` / `400 {error}` |
| DELETE | `/api/settings/key` | — | `204` |
| GET | `/api/ws` | upgrade | ws |
| GET | `/*` | — | static (embed.FS) |

### 6.2 `bucket=auto` thresholds
| Range | Bucket | Coverage |
|---|---|---|
| `1h` | 30s | 120 points |
| `6h` | 2m | 180 points |
| `24h` | 5m | 288 points |
| `7d` | 30m | 336 points |
| `31d` | 2h | 372 points |

Each bucket returns `min`, `max`, `avg` of `interval_remaining_pct` and
`weekly_remaining_pct`.

### 6.3 WebSocket protocol
Server → client message envelope:
```json
{ "type": "snapshot", "data": { "fetched_at": 1782543600000, "models": [...] } }
{ "type": "status",   "data": { "connected": true, "last_fetch_at": ..., "last_error": null } }
{ "type": "error",    "data": { "msg": "...", "at": ... } }
```
Client → server: none in v1 (read-only stream).

### 6.4 Settings API key flow
```
POST /api/settings/key { api_key }
  1. keyring.Set(service, user, api_key)
  2. apiclient.Fetch(api_key)  // immediate validation
     ├─ status_code == 0 → 200 {ok:true}
     └─ non-zero        → keyring.Delete(...) → 400 {error: "<status_msg>"}
```

---

## 7. Frontend Design (Dark Dashboard)

### 7.1 Layout (three-section vertical)
```
┌──────────────────────────────────────────────────────┐
│ Header: ●Monitor  [1h][6h][24h][7d][31d]  [⚙]  18:42│
├──────────────────────────────────────────────────────┤
│ Cards: ┌──────────────┐  ┌──────────────┐            │
│        │ general 96%  │  │ video 100%   │            │
│        │ 区间: 52m    │  │ 区间: 4h59m  │            │
│        │ 本周: 100%   │  │ 本周: 100%   │            │
│        └──────────────┘  └──────────────┘            │
├──────────────────────────────────────────────────────┤
│ general 趋势图 (24h)                                  │
│ ╱╲    ╱─╲                                            │
│ ╱  ╲──╱   ╲─────                                     │
│ video 趋势图 (24h)                                    │
│ ════════════ (平稳)                                  │
├──────────────────────────────────────────────────────┤
│ Status: WS ●  抓取 3s 前  错误 0  保留 31d  v1.0     │
└──────────────────────────────────────────────────────┘
```

### 7.2 Design tokens (CSS variables)
```css
:root {
  --bg-base:        #0a0e27;
  --bg-card:        #131836;
  --bg-card-hi:     #1a2046;
  --border:         #1f2547;
  --text-primary:   #e6e9f5;
  --text-muted:     #6b7390;
  --accent-general: #00d4ff;
  --accent-video:   #a855f7;
  --status-good:    #10b981;
  --status-warn:    #f59e0b;
  --status-bad:     #ef4444;
  --shadow-card:    0 4px 24px rgba(0,0,0,.4);
  --radius:         12px;
  --font-ui:        'Inter', system-ui, sans-serif;
  --font-mono:      'JetBrains Mono', monospace;
}
```

### 7.3 Charts
- One ECharts instance per model.
- Type: `line` with `smooth: true`, `symbol: 'none'`, gradient area fill.
- `animationDuration: 600`, `animationEasing: 'cubicOut'`.
- Y axis fixed `[0, 100]`, suffix `%`.
- X axis time, formatter `HH:mm:ss` (1h/6h) or `MM-DD HH:mm` (≥24h).
- Bucket data points use min/max band (light) + avg line (solid).

### 7.4 Settings modal
```
┌─ 设置 ─────────────────────────┐
│ API Key 状态: ● 未配置         │
│                                │
│ ┌──────────────────────────┐   │
│ │ sk-cp-.................. │   │
│ └──────────────────────────┘   │
│                                │
│ [保存并验证]    [取消]         │
│                                │
│ (验证中: spinner + 文字)       │
│ (已配置: ✓  + [更换] 按钮)    │
└────────────────────────────────┘
```
- Backdrop blur 8px.
- z-index 1000, fade+scale 200ms.
- ESC closes; click backdrop closes (only when idle).

### 7.5 Smoothness tactics
- **WS auto-reconnect**: exp backoff 1s/2s/4s/8s/16s/30s (max).
- **Skeleton loaders**: 3 gray blocks with pulse animation; first WS frame
  fades them out.
- **Incremental chart updates**: `chart.appendData({seriesIndex:0, data:[[t,v]]})`
  rather than full `setOption` on each tick.
- **Number tween**: small util that lerps display value over 400ms when underlying
  data changes (count-up effect).
- **`resize` debounce**: 200ms before calling `chart.resize()`.
- **All CSS transitions** `200ms ease` (hover, active, focus).
- **`prefers-reduced-motion`**: respect user OS setting, disable tweens.

### 7.6 Responsive breakpoints
- `>= 1024px`: full three-section.
- `768–1023px`: cards single column; charts full width.
- `< 768px`: time buttons collapse to icon+tooltip; status bar wraps.

### 7.7 Empty / loading / error states
- **No API key**: dashboard shows centered "请先配置 API Key" + [前往设置] button.
- **No history yet**: charts show "等待数据…"; cards show "—" for percent.
- **WS disconnected**: header status dot turns red, toast "连接已断开，正在重试…".
- **Fetch errors**: status bar shows "错误 N (上次: <msg>)", no crash.

---

## 8. Configuration & CLI

### 8.1 CLI flags
```
minimax-monitor -p 13337
```
| Flag | Default | Description |
|---|---|---|
| `-p` | `13337` | TCP port; binds `0.0.0.0:<port>` |

### 8.2 Environment variables
| Var | Default | Description |
|---|---|---|
| `POLL_INTERVAL` | `10s` | time.Duration string |
| `DB_PATH` | `./data/monitor.db` | SQLite file path |
| `RETENTION_DAYS` | `31` | int |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `KEYRING_SERVICE` | `minimax-monitor` | OS keyring service name |
| `KEYRING_USER` | `default` | OS keyring user/account name |

### 8.3 `.env.example`
```bash
POLL_INTERVAL=10s
DB_PATH=./data/monitor.db
RETENTION_DAYS=31
LOG_LEVEL=info
KEYRING_SERVICE=minimax-monitor
KEYRING_USER=default
```

---

## 9. Build & Deploy

### 9.1 `build.bat` (Windows, current platform)
```bat
@echo off
setlocal
if not exist dist mkdir dist
go build -trimpath -ldflags="-s -w" -o dist\minimax-monitor.exe .\cmd\minimax-monitor
if errorlevel 1 (
  echo [build] FAILED
  exit /b 1
)
echo [build] OK -^> dist\minimax-monitor.exe
endlocal
```

### 9.2 `build-cross.bat` (Windows, all targets)
```bat
@echo off
setlocal enabledelayedexpansion
if not exist dist mkdir dist
set "TARGETS=linux-amd64 linux-arm64 darwin-arm64 windows-amd64"
for %%T in (%TARGETS%) do (
  for /f "tokens=1,2 delims=-" %%A in ("%%T") do (
    set "GOOS=%%A"
    set "GOARCH=%%B"
    set "EXT="
    if "%%A"=="windows" set "EXT=.exe"
    echo [build] %%T ...
    set "CGO_ENABLED=0"
    go build -trimpath -ldflags="-s -w" -o dist\minimax-monitor-%%T!EXT! .\cmd\minimax-monitor || exit /b 1
  )
)
echo [build] all OK
endlocal
```

### 9.3 `Makefile` (Linux/macOS)
```makefile
BIN := dist/minimax-monitor
LDFLAGS := -s -w
PKG := ./cmd/minimax-monitor

.PHONY: build build-all run clean

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

build-all:
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-linux-amd64    $(PKG)
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-linux-arm64    $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-darwin-arm64   $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/minimax-monitor-windows-amd64.exe $(PKG)

run: build
	./$(BIN)

clean:
	rm -rf dist
```

### 9.4 Run
```bat
dist\minimax-monitor.exe              :: default :13337
dist\minimax-monitor.exe -p 18080     :: custom port
```

---

## 10. Security

- API key **never** logged, never embedded in URLs, never sent to client JS.
- Logger redacts `Authorization: Bearer ***` if it ever appears.
- All static resources served same-origin; `CORS` middleware returns no headers.
- `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;` — no inline scripts, all assets bundled.
- Binds `0.0.0.0` so dashboard is reachable on the LAN; **no auth** is the deliberate trade-off (single-user, trusted network).
- OS keyring access errors are surfaced to the UI ("未检测到系统钥匙串") so the user knows to fix the environment.

---

## 11. Error Handling

| Failure | Behavior |
|---|---|
| Network 4xx/5xx | log warn, increment `consecutive_errors`; status bar shows "错误 N" |
| Auth failure (401/403) | log error, keep retrying; recommend user re-check API key in status bar |
| Rate limit (429) | exp backoff 2^n seconds, capped at 60s |
| Keyring unavailable (Linux no desktop) | startup log error; UI shows clear message on settings modal |
| DB write failure | tx rollback, log error, scheduler continues |
| DB read failure | handler returns 500, log error |
| WS send to dead conn | drop conn silently |
| Static asset missing (404) | SPA fallback to `index.html` |

---

## 12. Testing Strategy

| Layer | Test |
|---|---|
| `apiclient` | `httptest.Server` returning canned JSON; verify headers, retry on 5xx, error on non-zero status |
| `keyring` | integration test with `zalando/go-keyring/test` helper (skip on CI without desktop) |
| `storage` | in-memory SQLite, table-driven: Insert, Latest, History (with bucket), Prune |
| `scheduler` | fake clock + fake fetcher; assert tick count, prune cadence |
| `server` | `httptest` + `gin.TestMode`; assert status codes, JSON shapes, WS upgrade |
| `web/` | smoke: page loads, no console errors; manual visual review |

Test command: `go test ./...`
Coverage target: ≥ 70% for `internal/{apiclient,keyring,storage,scheduler,server}`.

---

## 13. Open Questions / Risks

| Risk | Mitigation |
|---|---|
| `modernc.org/sqlite` slower than CGO version | benchmark at 5M rows; if > 5ms/insert, switch to mattn+CGO for builds that need it |
| ECharts bundle 1MB inflates binary | acceptable; vendor in `web/vendor/`; gzip is OS-level concern |
| Linux server without `gnome-keyring` | show explicit error in settings modal; document install steps in README |
| API status_code `1` vs `3` semantics not documented | treat any non-zero as error in v1; surface raw `status` int in card for debug |

---

## 14. Future Work (Out of Scope for v1)

- v2: alerting (webhook/email/ntfy) with thresholds.
- v2: multi-account / multi-key tabs.
- v2: CSV/JSON export of historical data.
- v2: dashboard basic auth (when public-internet exposure is needed).
- v2: downsample-on-write to keep DB size bounded for very long retention.

---

## 15. Acceptance Criteria

The v1 build is "done" when:
1. `build.bat` produces `dist/minimax-monitor.exe` from a clean checkout on Windows.
2. `build-cross.bat` produces 4 platform binaries.
3. Running with no API key shows "请先配置" page; settings modal accepts a key, validates it, persists to keyring, and reloads automatically.
4. With a valid key, dashboard shows 2 model cards updating every 10s.
5. Trend charts render for all 5 time ranges; switching range is smooth (< 200ms).
6. WS auto-reconnects after server restart within 30s.
7. After 31 days of operation, DB size stays bounded (~150MB) due to prune.
8. `go test ./...` passes; no P0 bugs.
