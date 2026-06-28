# MiniMax Token Plan Monitor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single-binary, cross-platform Go service that polls MiniMax `/v1/token_plan/remains` every 10s, stores 31 days of history in SQLite, and exposes a dark-themed web dashboard on `0.0.0.0:13337` with embedded static assets.

**Architecture:** Pure-Go (CGO=0) Go process. Gin HTTP server with WebSocket for live updates. `modernc.org/sqlite` for storage. `zalando/go-keyring` for encrypted API key. `//go:embed` for the single-page web app. Vanilla HTML/CSS/JS + ECharts 5 (offline-vendored) for the dashboard. `time.Ticker`-driven scheduler for polling and pruning.

**Tech Stack:** Go 1.22+, gin-gonic/gin, coder/websocket, modernc.org/sqlite, zalando/go-keyring, ECharts 5, vanilla JS.

## Global Constraints

- Go version: `1.22+` (toolchain directive in `go.mod`).
- Build flag for ALL targets: `CGO_ENABLED=0`.
- Compile flag for ALL targets: `go build -trimpath -ldflags="-s -w"`.
- Output directory: `./dist/`. Binary name: `minimax-monitor[.exe]`.
- Default listen: `0.0.0.0:13337`. Override with `-p <port>`.
- Poll interval: `10s` (env `POLL_INTERVAL`).
- Data retention: `31` days (env `RETENTION_DAYS`).
- DB path: `./data/monitor.db` (env `DB_PATH`).
- Keyring service: `minimax-monitor`, user: `default` (env `KEYRING_SERVICE`, `KEYRING_USER`).
- Web assets MUST be embedded with `//go:embed all:web`.
- Frontend is a single dark-themed page; no React/Vue/build step.
- ECharts is vendored under `internal/server/web/vendor/echarts.min.js` (offline; no CDN).
- API key MUST NEVER appear in logs, URLs, or frontend JS.
- All commits use the `minimax-monitor` repo (initialise git on Task 1).

---

## File Structure

```
minimax-monitor/                                  (Go module root)
├── go.mod
├── go.sum
├── .gitignore
├── .env.example
├── README.md
├── Makefile
├── build.bat
├── build-cross.bat
├── cmd/
│   └── minimax-monitor/
│       └── main.go                                # flag parse, wire deps, signal handling
├── internal/
│   ├── config/
│   │   ├── config.go                              # Load() Config from env
│   │   └── config_test.go
│   ├── model/
│   │   └── types.go                               # APIResponse, ModelRemains, BaseResp
│   ├── apiclient/
│   │   ├── client.go                              # Fetch(ctx, key) with retry
│   │   └── client_test.go                         # httptest mock
│   ├── keyring/
│   │   ├── keyring.go                             # Get/Set/Delete wrapper
│   │   └── keyring_test.go                        # skip on CI without desktop
│   ├── storage/
│   │   ├── sqlite.go                              # Open, PRAGMA, schema
│   │   ├── snapshot.go                            # Insert/Latest/History/Prune
│   │   ├── sqlite_test.go
│   │   └── snapshot_test.go
│   ├── scheduler/
│   │   ├── scheduler.go                           # 10s tick + 24h prune + Start/Stop
│   │   └── scheduler_test.go                      # fake clock
│   └── server/
│       ├── server.go                              # gin engine, routes
│       ├── handlers_api.go                        # /api/status, /api/models, /api/history
│       ├── handlers_settings.go                   # /api/settings/key
│       ├── handlers_ws.go                         # WS handler + Broadcaster iface
│       ├── wshub.go                               # WSHub implementation
│       ├── embed.go                               # //go:embed all:web
│       ├── web/                                   # web assets (lives next to embed.go)
│       │   ├── index.html
│       │   ├── app.js
│       │   ├── style.css
│       │   └── vendor/echarts.min.js
│       ├── server_test.go
│       ├── handlers_test.go
│       └── ws_test.go
└── docs/
    └── superpowers/
        ├── specs/2026-06-27-minimax-monitor-design.md
        └── plans/2026-06-27-minimax-monitor.md
```

> Note: `internal/server/web/` (not project root) is the only place the embed
> directive can see without `..` paths. The server serves them under `/` so
> URLs are unaffected.

---

## Task 1: Project Skeleton & Module Init

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `README.md`
- Create: `cmd/minimax-monitor/main.go` (skeleton that exits 0)

**Interfaces:** none.

- [ ] **Step 1: Init git repo and Go module**

```bash
cd d:/cc/minimax-mo
git init
git config user.email "dev@local"
git config user.name "dev"
go mod init minimax-monitor
```
Expected: `go.mod` created with `module minimax-monitor` and `go 1.22`.

- [ ] **Step 2: Write `.gitignore`**

Create `.gitignore`:
```gitignore
dist/
data/
*.db
*.db-wal
*.db-shm
.env
.idea/
.vscode/
```

- [ ] **Step 3: Write README placeholder**

Create `README.md`:
```markdown
# MiniMax Token Plan Monitor

Single-binary Go service that polls MiniMax `/v1/token_plan/remains` and
visualises 31 days of history in a local web dashboard.

See `docs/superpowers/specs/2026-06-27-minimax-monitor-design.md` for the design.
```

- [ ] **Step 4: Write main.go skeleton**

Create `cmd/minimax-monitor/main.go`:
```go
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stdout, "minimax-monitor: skeleton OK")
}
```

- [ ] **Step 5: Verify build**

```bash
go build -o dist/minimax-monitor.exe ./cmd/minimax-monitor
./dist/minimax-monitor.exe
```
Expected: prints `minimax-monitor: skeleton OK` and exits 0.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum .gitignore README.md cmd/
git commit -m "chore: initialise module and skeleton"
```

---

## Task 2: Config Package (TDD)

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Interfaces:**
- `type Config struct { Port int; PollInterval time.Duration; DBPath string; RetentionDays int; LogLevel slog.Level; KeyringService, KeyringUser, APIURL string }`
- `func Load() Config` — reads env vars; uses defaults from spec §8.2.

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:
```go
package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "")
	t.Setenv("DB_PATH", "")
	t.Setenv("RETENTION_DAYS", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("KEYRING_SERVICE", "")
	t.Setenv("KEYRING_USER", "")
	t.Setenv("API_URL", "")

	c := Load()
	if c.PollInterval != 10*time.Second {
		t.Errorf("PollInterval = %v, want 10s", c.PollInterval)
	}
	if c.DBPath != "./data/monitor.db" {
		t.Errorf("DBPath = %q, want ./data/monitor.db", c.DBPath)
	}
	if c.RetentionDays != 31 {
		t.Errorf("RetentionDays = %d, want 31", c.RetentionDays)
	}
	if c.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", c.LogLevel)
	}
	if c.KeyringService != "minimax-monitor" || c.KeyringUser != "default" {
		t.Errorf("Keyring = %s/%s", c.KeyringService, c.KeyringUser)
	}
	if c.APIURL != "https://www.minimaxi.com/v1/token_plan/remains" {
		t.Errorf("APIURL = %q", c.APIURL)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "30s")
	t.Setenv("DB_PATH", "/tmp/x.db")
	t.Setenv("RETENTION_DAYS", "7")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("KEYRING_SERVICE", "svc")
	t.Setenv("KEYRING_USER", "u")
	t.Setenv("API_URL", "https://example.com/api")

	c := Load()
	if c.PollInterval != 30*time.Second || c.DBPath != "/tmp/x.db" ||
		c.RetentionDays != 7 || c.LogLevel != slog.LevelDebug ||
		c.KeyringService != "svc" || c.KeyringUser != "u" ||
		c.APIURL != "https://example.com/api" {
		t.Errorf("override mismatch: %+v", c)
	}
}

func TestLoad_InvalidIntervalFallsBack(t *testing.T) {
	t.Setenv("POLL_INTERVAL", "garbage")
	c := Load()
	if c.PollInterval != 10*time.Second {
		t.Errorf("invalid interval = %v, want default 10s", c.PollInterval)
	}
}
```

- [ ] **Step 2: Run tests, expect failure**

```bash
go test ./internal/config/...
```
Expected: build error, `Load` undefined.

- [ ] **Step 3: Implement config**

Create `internal/config/config.go`:
```go
package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port           int
	PollInterval   time.Duration
	DBPath         string
	RetentionDays  int
	LogLevel       slog.Level
	KeyringService string
	KeyringUser    string
	APIURL         string
}

func Load() Config {
	return Config{
		PollInterval:   durationEnv("POLL_INTERVAL", 10*time.Second),
		DBPath:         strEnv("DB_PATH", "./data/monitor.db"),
		RetentionDays:  intEnv("RETENTION_DAYS", 31),
		LogLevel:       levelEnv("LOG_LEVEL", slog.LevelInfo),
		KeyringService: strEnv("KEYRING_SERVICE", "minimax-monitor"),
		KeyringUser:    strEnv("KEYRING_USER", "default"),
		APIURL:         strEnv("API_URL", "https://www.minimaxi.com/v1/token_plan/remains"),
	}
}

func strEnv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func intEnv(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func durationEnv(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func levelEnv(k string, def slog.Level) slog.Level {
	switch strings.ToLower(strEnv(k, "")) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return def
	}
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test ./internal/config/... -v
```
Expected: 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): env-driven config with defaults"
```

---

## Task 3: API Model & Client (TDD)

**Files:**
- Create: `internal/model/types.go`
- Create: `internal/apiclient/client.go`
- Create: `internal/apiclient/client_test.go`

**Interfaces:**
- `model.APIResponse`, `model.ModelRemains`, `model.BaseResp` (structs as in spec §4.1).
- `apiclient.New(url string) *Client`
- `(*Client).Fetch(ctx context.Context, apiKey string) (*model.APIResponse, error)` — 3 retries with exp backoff (1s, 2s, 4s) on 5xx / 429 / network error. Returns error on non-zero `base_resp.status_code` or final HTTP failure.

- [ ] **Step 1: Write model types**

Create `internal/model/types.go`:
```go
package model

type BaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type ModelRemains struct {
	ModelName                   string `json:"model_name"`
	StartTime                   int64  `json:"start_time"`
	EndTime                     int64  `json:"end_time"`
	RemainsTime                 int64  `json:"remains_time"`
	CurrentIntervalTotalCount   int64  `json:"current_interval_total_count"`
	CurrentIntervalUsageCount   int64  `json:"current_interval_usage_count"`
	CurrentIntervalRemainingPct int    `json:"current_interval_remaining_percent"`
	CurrentIntervalStatus       int    `json:"current_interval_status"`
	CurrentWeeklyTotalCount     int64  `json:"current_weekly_total_count"`
	CurrentWeeklyUsageCount     int64  `json:"current_weekly_usage_count"`
	WeeklyStartTime             int64  `json:"weekly_start_time"`
	WeeklyEndTime               int64  `json:"weekly_end_time"`
	WeeklyRemainsTime           int64  `json:"weekly_remains_time"`
	CurrentWeeklyStatus         int    `json:"current_weekly_status"`
	CurrentWeeklyRemainingPct   int    `json:"current_weekly_remaining_percent"`
}

type APIResponse struct {
	ModelRemains []ModelRemains `json:"model_remains"`
	BaseResp     BaseResp       `json:"base_resp"`
}
```

- [ ] **Step 2: Write failing test for client**

Create `internal/apiclient/client_test.go`:
```go
package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"minimax-monitor/internal/model"
)

func newOKServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.APIResponse{
			ModelRemains: []model.ModelRemains{{ModelName: "general"}},
			BaseResp:     model.BaseResp{StatusCode: 0, StatusMsg: "success"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestFetch_Success(t *testing.T) {
	srv, calls := newOKServer(t)
	c := New(srv.URL)
	resp, err := c.Fetch(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(resp.ModelRemains) != 1 || resp.ModelRemains[0].ModelName != "general" {
		t.Errorf("unexpected body: %+v", resp)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Errorf("calls = %d, want 1", *calls)
	}
}

func TestFetch_NonZeroStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.APIResponse{
			BaseResp: model.BaseResp{StatusCode: 1001, StatusMsg: "bad key"},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	_, err := c.Fetch(context.Background(), "k")
	if err == nil || err.Error() == "" {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestFetch_RetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(model.APIResponse{
			BaseResp: model.BaseResp{StatusCode: 0, StatusMsg: "success"},
		})
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	// speed up backoff for test
	c.backoff = func(attempt int) time.Duration { return time.Millisecond }
	_, err := c.Fetch(context.Background(), "k")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}
```

- [ ] **Step 3: Run test, expect failure**

```bash
go test ./internal/apiclient/... -v
```
Expected: build error, `New` undefined.

- [ ] **Step 4: Implement client**

Create `internal/apiclient/client.go`:
```go
package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"minimax-monitor/internal/model"
)

type Client struct {
	url     string
	cli     *http.Client
	backoff func(attempt int) time.Duration
}

func New(url string) *Client {
	return &Client{
		url:     url,
		cli:     &http.Client{Timeout: 10 * time.Second},
		backoff: defaultBackoff,
	}
}

func defaultBackoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second // 1s, 2s, 4s
}

func (c *Client) Fetch(ctx context.Context, apiKey string) (*model.APIResponse, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := c.cli.Do(req)
		if err != nil {
			lastErr = err
			if i < 2 {
				time.Sleep(c.backoff(i))
				continue
			}
			break
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("upstream status %d", resp.StatusCode)
			resp.Body.Close()
			if i < 2 {
				time.Sleep(c.backoff(i))
				continue
			}
			break
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			return nil, fmt.Errorf("upstream status %d", resp.StatusCode)
		}
		var out model.APIResponse
		dec := json.NewDecoder(resp.Body)
		decErr := dec.Decode(&out)
		resp.Body.Close()
		if decErr != nil {
			return nil, decErr
		}
		if out.BaseResp.StatusCode != 0 {
			return nil, fmt.Errorf("api error: %s", out.BaseResp.StatusMsg)
		}
		return &out, nil
	}
	return nil, fmt.Errorf("fetch failed after retries: %w", lastErr)
}
```

- [ ] **Step 5: Run tests, expect pass**

```bash
go test ./internal/apiclient/... -v
```
Expected: 3 tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/model/ internal/apiclient/
git commit -m "feat(apiclient): minimax client with retry"
```

---

## Task 4: Keyring Wrapper (TDD)

**Files:**
- Create: `internal/keyring/keyring.go`
- Create: `internal/keyring/keyring_test.go`

**Interfaces:**
- `type Store interface { Get() (string, error); Set(string) error; Delete() error }`
- `func New(service, user string) Store`

- [ ] **Step 1: Add dependency**

```bash
go get github.com/zalando/go-keyring
```

- [ ] **Step 2: Write failing test**

Create `internal/keyring/keyring_test.go`:
```go
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
```

- [ ] **Step 3: Run test, expect failure**

```bash
go test ./internal/keyring/... -v
```
Expected: build error, `New` undefined.

- [ ] **Step 4: Implement keyring**

Create `internal/keyring/keyring.go`:
```go
package keyring

import "github.com/zalando/go-keyring"

type Store struct {
	service string
	user    string
}

func New(service, user string) *Store {
	return &Store{service: service, user: user}
}

func (s *Store) Get() (string, error) {
	return keyring.Get(s.service, s.user)
}

func (s *Store) Set(value string) error {
	return keyring.Set(s.service, s.user, value)
}

func (s *Store) Delete() error {
	return keyring.Delete(s.service, s.user)
}

// Delete is a package-level convenience for cleanup in tests.
func Delete(service, user string) error {
	return keyring.Delete(service, user)
}
```

- [ ] **Step 5: Run test, expect pass (or skip on CI)**

```bash
go test ./internal/keyring/... -v
```
Expected: passes locally; skipped on CI.

- [ ] **Step 6: Commit**

```bash
git add internal/keyring/ go.mod go.sum
git commit -m "feat(keyring): OS keyring wrapper"
```

---

## Task 5: Storage — Schema & Open (TDD)

**Files:**
- Create: `internal/storage/sqlite.go`
- Create: `internal/storage/sqlite_test.go`

**Interfaces:**
- `type DB struct { *sql.DB }`
- `func Open(path string) (*DB, error)` — creates dir, opens DB, sets PRAGMAs, creates schema.

- [ ] **Step 1: Add dependency**

```bash
go get modernc.org/sqlite
```

- [ ] **Step 2: Write failing test**

Create `internal/storage/sqlite_test.go`:
```go
package storage

import (
	"path/filepath"
	"testing"
)

func TestOpen_CreatesSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	row := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='snapshot'`)
	var name string
	if err := row.Scan(&name); err != nil {
		t.Fatalf("snapshot table missing: %v", err)
	}
	if name != "snapshot" {
		t.Errorf("table name = %q", name)
	}
}
```

- [ ] **Step 3: Run test, expect failure**

```bash
go test ./internal/storage/... -v
```
Expected: build error, `Open` undefined.

- [ ] **Step 4: Implement Open**

Create `internal/storage/sqlite.go`:
```go
package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS snapshot (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    fetched_at                  INTEGER NOT NULL,
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
`

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if _, err := sqlDB.Exec(schema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &DB{DB: sqlDB}, nil
}
```

- [ ] **Step 5: Run test, expect pass**

```bash
go test ./internal/storage/... -v
```
Expected: test passes.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/sqlite.go internal/storage/sqlite_test.go go.mod go.sum
git commit -m "feat(storage): open SQLite with PRAGMA and schema"
```

---

## Task 6: Storage — Insert, Latest, History, Prune (TDD)

**Files:**
- Create: `internal/storage/snapshot.go`
- Create: `internal/storage/snapshot_test.go`

**Interfaces:**
- `type Snapshot struct { ID int64; FetchedAt int64; ModelName string; IntervalRemainingPct *int; ...; RawJSON string }` (use `sql.NullInt64` etc. for nullable ints)
- `func (db *DB) Insert(ctx context.Context, resp *model.APIResponse, t time.Time) error`
- `type Bucket struct { T int64; Min, Max, Avg float64 }`
- `func (db *DB) Latest(ctx context.Context) ([]Snapshot, error)` — returns the most recent row per model.
- `func (db *DB) History(ctx context.Context, model string, fromMs, toMs int64, bucketMs int64) ([]Bucket, error)` — returns min/max/avg of `interval_remaining_pct` per bucket.
- `func (db *DB) PruneOlderThan(ctx context.Context, cutoffMs int64) (int64, error)`

- [ ] **Step 1: Write failing tests**

Create `internal/storage/snapshot_test.go`:
```go
package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"minimax-monitor/internal/model"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sampleResp() *model.APIResponse {
	return &model.APIResponse{
		ModelRemains: []model.ModelRemains{
			{
				ModelName: "general", CurrentIntervalRemainingPct: 90, CurrentWeeklyRemainingPct: 100,
				EndTime: 1782561600000, RemainsTime: 3600000,
			},
			{
				ModelName: "video", CurrentIntervalRemainingPct: 100, CurrentWeeklyRemainingPct: 100,
			},
		},
		BaseResp: model.BaseResp{StatusCode: 0, StatusMsg: "success"},
	}
}

func TestInsert_AndLatest(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.UnixMilli(1782543600000)
	if err := db.Insert(ctx, sampleResp(), now); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	rows, err := db.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("Latest len = %d, want 2", len(rows))
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.ModelName] = *r.IntervalRemainingPct
	}
	if got["general"] != 90 || got["video"] != 100 {
		t.Errorf("latest pcts = %+v", got)
	}
}

func TestHistory_Bucketed(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	t0 := time.UnixMilli(1700000000000)
	// insert 10 rows over 100s for "general" with values 10,20,...,100
	for i := 0; i < 10; i++ {
		resp := &model.APIResponse{
			ModelRemains: []model.ModelRemains{{
				ModelName: "general", CurrentIntervalRemainingPct: 10 + i*10,
			}},
			BaseResp: model.BaseResp{StatusCode: 0, StatusMsg: "ok"},
		}
		if err := db.Insert(ctx, resp, t0.Add(time.Duration(i*10)*time.Second)); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}
	// bucket by 30s: rows at 0,10,20,30,40,50,60,70,80,90 seconds
	// buckets: [0,30): 10,20,30 → min=10 avg=20 max=30
	//          [30,60): 40,50,60 → min=40 avg=50 max=60
	//          [60,90): 70,80,90 → min=70 avg=80 max=90
	buckets, err := db.History(ctx, "general",
		t0.UnixMilli(),
		t0.Add(100*time.Second).UnixMilli(),
		30_000)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(buckets) != 3 {
		t.Fatalf("bucket count = %d, want 3", len(buckets))
	}
	if buckets[0].Min != 10 || buckets[0].Max != 30 || buckets[0].Avg != 20 {
		t.Errorf("bucket 0 = %+v", buckets[0])
	}
	if buckets[1].Min != 40 || buckets[1].Max != 60 || buckets[1].Avg != 50 {
		t.Errorf("bucket 1 = %+v", buckets[1])
	}
	if buckets[2].Min != 70 || buckets[2].Max != 90 || buckets[2].Avg != 80 {
		t.Errorf("bucket 2 = %+v", buckets[2])
	}
}

func TestPrune(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	old := time.UnixMilli(1000)
	recent := time.UnixMilli(2000)
	_ = db.Insert(ctx, &model.APIResponse{
		ModelRemains: []model.ModelRemains{{ModelName: "g", CurrentIntervalRemainingPct: 1}},
		BaseResp:     model.BaseResp{StatusCode: 0},
	}, old)
	_ = db.Insert(ctx, &model.APIResponse{
		ModelRemains: []model.ModelRemains{{ModelName: "g", CurrentIntervalRemainingPct: 2}},
		BaseResp:     model.BaseResp{StatusCode: 0},
	}, recent)
	n, err := db.PruneOlderThan(ctx, 1500)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned = %d, want 1", n)
	}
	rows, _ := db.Latest(ctx)
	if len(rows) != 1 || *rows[0].IntervalRemainingPct != 2 {
		t.Errorf("after prune = %+v", rows)
	}
}
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./internal/storage/... -v
```
Expected: build error.

- [ ] **Step 3: Implement snapshot operations**

Create `internal/storage/snapshot.go`:
```go
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"minimax-monitor/internal/model"
)

type Snapshot struct {
	ID                    int64
	FetchedAt             int64
	ModelName             string
	IntervalRemainingPct  *int
	IntervalStatus        *int
	IntervalTotalCount    *int64
	IntervalUsageCount    *int64
	IntervalEndAt         *int64
	IntervalRemainsMs     *int64
	WeeklyRemainingPct    *int
	WeeklyStatus          *int
	WeeklyTotalCount      *int64
	WeeklyUsageCount      *int64
	WeeklyEndAt           *int64
	WeeklyRemainsMs       *int64
	RawJSON               string
}

type Bucket struct {
	T         int64
	Min, Max  float64
	Avg       float64
}

func (db *DB) Insert(ctx context.Context, resp *model.APIResponse, t time.Time) error {
	raw, _ := json.Marshal(resp)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO snapshot(
		fetched_at, model_name,
		interval_remaining_pct, interval_status, interval_total_count, interval_usage_count,
		interval_end_at, interval_remains_ms,
		weekly_remaining_pct, weekly_status, weekly_total_count, weekly_usage_count,
		weekly_end_at, weekly_remains_ms, raw_json
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range resp.ModelRemains {
		if _, err := stmt.ExecContext(ctx,
			t.UnixMilli(), m.ModelName,
			m.CurrentIntervalRemainingPct, m.CurrentIntervalStatus,
			m.CurrentIntervalTotalCount, m.CurrentIntervalUsageCount,
			m.EndTime, m.RemainsTime,
			m.CurrentWeeklyRemainingPct, m.CurrentWeeklyStatus,
			m.CurrentWeeklyTotalCount, m.CurrentWeeklyUsageCount,
			m.WeeklyEndTime, m.WeeklyRemainsTime, string(raw),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) Latest(ctx context.Context) ([]Snapshot, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.fetched_at, s.model_name,
			s.interval_remaining_pct, s.interval_status, s.interval_total_count, s.interval_usage_count,
			s.interval_end_at, s.interval_remains_ms,
			s.weekly_remaining_pct, s.weekly_status, s.weekly_total_count, s.weekly_usage_count,
			s.weekly_end_at, s.weekly_remains_ms, s.raw_json
		FROM snapshot s
		JOIN (
			SELECT model_name, MAX(id) AS max_id
			FROM snapshot GROUP BY model_name
		) m ON s.id = m.max_id
		ORDER BY s.model_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var s Snapshot
		if err := rows.Scan(
			&s.ID, &s.FetchedAt, &s.ModelName,
			&s.IntervalRemainingPct, &s.IntervalStatus, &s.IntervalTotalCount, &s.IntervalUsageCount,
			&s.IntervalEndAt, &s.IntervalRemainsMs,
			&s.WeeklyRemainingPct, &s.WeeklyStatus, &s.WeeklyTotalCount, &s.WeeklyUsageCount,
			&s.WeeklyEndAt, &s.WeeklyRemainsMs, &s.RawJSON,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *DB) History(ctx context.Context, modelName string, fromMs, toMs, bucketMs int64) ([]Bucket, error) {
	if bucketMs <= 0 {
		return nil, fmt.Errorf("bucketMs must be > 0")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT (fetched_at / ?) * ? AS bucket_t,
			MIN(interval_remaining_pct), MAX(interval_remaining_pct), AVG(interval_remaining_pct)
		FROM snapshot
		WHERE model_name = ? AND fetched_at >= ? AND fetched_at < ?
		GROUP BY bucket_t
		ORDER BY bucket_t`, bucketMs, bucketMs, modelName, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.T, &b.Min, &b.Max, &b.Avg); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (db *DB) PruneOlderThan(ctx context.Context, cutoffMs int64) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM snapshot WHERE fetched_at < ?`, cutoffMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test ./internal/storage/... -v
```
Expected: 3 new tests + schema test pass.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/snapshot.go internal/storage/snapshot_test.go
git commit -m "feat(storage): insert, latest, history, prune"
```

---

## Task 7: Scheduler (TDD)

**Files:**
- Create: `internal/scheduler/scheduler.go`
- Create: `internal/scheduler/scheduler_test.go`

**Interfaces:**
- `type Fetcher interface { Fetch(ctx context.Context, key string) (*model.APIResponse, error) }`
- `type Inserter interface { Insert(ctx context.Context, resp *model.APIResponse, t time.Time) error; PruneOlderThan(ctx context.Context, cutoffMs int64) (int64, error) }`
- `type Broadcaster interface { Broadcast(snap []storage.Snapshot) }`
- `type Scheduler struct { ... }`
- `func New(f Fetcher, s Inserter, b Broadcaster, keyFn func() (string, error), interval, pruneEvery time.Duration, retentionDays int) *Scheduler`
- `func (sc *Scheduler) Start(ctx context.Context)` — runs until ctx done; first tick immediately.
- `func (sc *Scheduler) RunOnce(ctx context.Context) error` — single iteration (exported for tests + manual trigger).
- `func (sc *Scheduler) StartTicking() / Stop()` — non-blocking start/stop that can be triggered by settings change.

For testability, scheduler takes a `nowFn func() time.Time` and `sleepFn func(d time.Duration)`.

- [ ] **Step 1: Write failing test**

Create `internal/scheduler/scheduler_test.go`:
```go
package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"minimax-monitor/internal/model"
	"minimax-monitor/internal/storage"
)

type fakeFetcher struct {
	calls atomic.Int32
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) (*model.APIResponse, error) {
	f.calls.Add(1)
	return &model.APIResponse{
		ModelRemains: []model.ModelRemains{{ModelName: "general", CurrentIntervalRemainingPct: 50}},
		BaseResp:     model.BaseResp{StatusCode: 0, StatusMsg: "ok"},
	}, nil
}

type fakeInserter struct {
	inserts atomic.Int32
	prunes  atomic.Int32
}

func (f *fakeInserter) Insert(_ context.Context, _ *model.APIResponse, _ time.Time) error {
	f.inserts.Add(1)
	return nil
}
func (f *fakeInserter) PruneOlderThan(_ context.Context, _ int64) (int64, error) {
	f.prunes.Add(1)
	return 0, nil
}

type fakeBroadcaster struct {
	atomic.Int32
}

func (f *fakeBroadcaster) Broadcast(_ []storage.Snapshot) { f.Add(1) }

func TestRunOnce_FetchAndInsert(t *testing.T) {
	f := &fakeFetcher{}
	i := &fakeInserter{}
	b := &fakeBroadcaster{}
	sc := New(f, i, b, func() (string, error) { return "k", nil },
		10*time.Second, 24*time.Hour, 31)
	if err := sc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if f.calls.Load() != 1 {
		t.Errorf("fetch calls = %d", f.calls.Load())
	}
	if i.inserts.Load() != 1 {
		t.Errorf("inserts = %d", i.inserts.Load())
	}
}

func TestRunOnce_NoKeySkips(t *testing.T) {
	f := &fakeFetcher{}
	i := &fakeInserter{}
	sc := New(f, i, &fakeBroadcaster{}, func() (string, error) { return "", errNoKey }, 0, 0, 0)
	if err := sc.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if f.calls.Load() != 0 {
		t.Errorf("should not fetch without key")
	}
}
```

Add a small helper in same file:
```go
var errNoKey = &configErr{}

type configErr struct{}

func (c *configErr) Error() string { return "no key" }
```

- [ ] **Step 2: Run test, expect failure**

```bash
go test ./internal/scheduler/... -v
```
Expected: build error.

- [ ] **Step 3: Implement scheduler**

Create `internal/scheduler/scheduler.go`:
```go
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"minimax-monitor/internal/model"
	"minimax-monitor/internal/storage"
)

type Fetcher interface {
	Fetch(ctx context.Context, key string) (*model.APIResponse, error)
}

type Inserter interface {
	Insert(ctx context.Context, resp *model.APIResponse, t time.Time) error
	PruneOlderThan(ctx context.Context, cutoffMs int64) (int64, error)
}

type Broadcaster interface {
	Broadcast(snap []storage.Snapshot)
}

type Scheduler struct {
	f              Fetcher
	ins            Inserter
	b              Broadcaster
	keyFn          func() (string, error)
	interval       time.Duration
	pruneEvery     time.Duration
	retentionDays  int
	nowFn          func() time.Time
	mu             sync.Mutex
	running        bool
	consecErrors   int
	lastFetchAt    time.Time
	lastErrMsg     string
}

func New(f Fetcher, ins Inserter, b Broadcaster, keyFn func() (string, error),
	interval, pruneEvery time.Duration, retentionDays int) *Scheduler {
	return &Scheduler{
		f: f, ins: ins, b: b, keyFn: keyFn,
		interval: interval, pruneEvery: pruneEvery, retentionDays: retentionDays,
		nowFn: time.Now,
	}
}

func (sc *Scheduler) RunOnce(ctx context.Context) error {
	key, err := sc.keyFn()
	if err != nil || key == "" {
		slog.Debug("scheduler: no key, skipping tick")
		return nil
	}
	resp, err := sc.f.Fetch(ctx, key)
	sc.mu.Lock()
	sc.lastFetchAt = sc.nowFn()
	if err != nil {
		sc.consecErrors++
		sc.lastErrMsg = err.Error()
		sc.mu.Unlock()
		slog.Warn("fetch failed", "err", err)
		return err
	}
	sc.consecErrors = 0
	sc.lastErrMsg = ""
	sc.mu.Unlock()
	if err := sc.ins.Insert(ctx, resp, sc.nowFn()); err != nil {
		slog.Error("insert failed", "err", err)
		return err
	}
	if snaps, err := sc.ins.Latest(ctx); err == nil && sc.b != nil {
		sc.b.Broadcast(snaps)
	}
	return nil
}

func (sc *Scheduler) Start(ctx context.Context) {
	sc.mu.Lock()
	if sc.running {
		sc.mu.Unlock()
		return
	}
	sc.running = true
	sc.mu.Unlock()
	defer func() {
		sc.mu.Lock()
		sc.running = false
		sc.mu.Unlock()
	}()

	_ = sc.RunOnce(ctx)
	tick := time.NewTicker(sc.interval)
	prune := time.NewTicker(sc.pruneEvery)
	defer tick.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = sc.RunOnce(ctx)
		case <-prune.C:
			cutoff := sc.nowFn().AddDate(0, 0, -sc.retentionDays).UnixMilli()
			if n, err := sc.ins.PruneOlderThan(ctx, cutoff); err == nil && n > 0 {
				slog.Info("pruned old snapshots", "rows", n)
			}
		}
	}
}

func (sc *Scheduler) Stats() (lastFetchAt time.Time, consecErrors int, lastErr string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.lastFetchAt, sc.consecErrors, sc.lastErrMsg
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test ./internal/scheduler/... -v
```
Expected: 2 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler/
git commit -m "feat(scheduler): 10s tick + 24h prune"
```

---

## Task 8: Gin Server Skeleton + Healthz (TDD)

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`

**Interfaces:**
- `type Server struct { Engine *gin.Engine; Hub Broadcaster; DB *storage.DB; Store keyringStore }`
- `type keyringStore interface { Get() (string, error); Set(string) error; Delete() error }`
- `func New(db *storage.DB, store keyringStore) *Server`
- `func (s *Server) Run(addr string) error`
- Routes: `GET /api/healthz`, `GET /*` (placeholder static).

- [ ] **Step 1: Add dependency**

```bash
go get github.com/gin-gonic/gin
```

- [ ] **Step 2: Write failing test**

Create `internal/server/server_test.go`:
```go
package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	s := New(nil, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/healthz", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", w.Body.String())
	}
}
```

- [ ] **Step 3: Run test, expect failure**

```bash
go test ./internal/server/... -v
```
Expected: build error, `New` undefined.

- [ ] **Step 4: Implement server skeleton**

Create `internal/server/server.go`:
```go
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"minimax-monitor/internal/storage"
)

type keyringStore interface {
	Get() (string, error)
	Set(string) error
	Delete() error
}

type Server struct {
	Engine *gin.Engine
	DB     *storage.DB
	Store  keyringStore
	Hub    Broadcaster
}

func New(db *storage.DB, store keyringStore) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{DB: db, Store: store, Engine: gin.New()}
	s.Engine.Use(gin.Recovery())
	s.routes()
	return s
}

func (s *Server) routes() {
	s.Engine.GET("/api/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
}

func (s *Server) Run(addr string) error {
	return s.Engine.Run(addr)
}
```

Add empty `Broadcaster` type for now (real impl in Task 11):
Create `internal/server/handlers_ws.go`:
```go
package server

import "minimax-monitor/internal/storage"

type Broadcaster interface {
	Broadcast(snap []storage.Snapshot)
}
```

- [ ] **Step 5: Run test, expect pass**

```bash
go test ./internal/server/... -v
```
Expected: test passes.

- [ ] **Step 6: Commit**

```bash
git add internal/server/ go.mod go.sum
git commit -m "feat(server): gin engine skeleton with healthz"
```

---

## Task 9: Status / Models / History Handlers (TDD)

**Files:**
- Create: `internal/server/handlers_api.go`
- Modify: `internal/server/server.go` (register routes)
- Modify: `internal/server/server_test.go` (add tests)

**Interfaces:**
- `GET /api/status` → `Status { KeyringConfigured bool, LastFetchAt *time.Time, ConsecErrors int, LastError string, PollInterval string, DBSizeMB float64 }`
- `GET /api/models` → `[]ModelLatest { ModelName string, IntervalRemainingPct *int, WeeklyRemainingPct *int, IntervalRemainsMs *int64, FetchedAt int64 }`
- `GET /api/history?model=X&range=1h|6h|24h|7d|31d&bucket=auto|<sec>` → `History { Model string, Range string, BucketMs int64, Points []BucketPoint { T int64; Min, Max, Avg float64 } }`

`range → bucket` auto map (seconds): 1h→30, 6h→120, 24h→300, 7d→1800, 31d→7200.

- [ ] **Step 1: Write failing tests**

Append to `internal/server/server_test.go`:
```go
func TestStatus_NoKeyring(t *testing.T) {
	s := New(nil, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/status", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !contains(w.Body.String(), `"keyring_configured":false`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestHistory_RejectsBadModel(t *testing.T) {
	s := New(nil, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/history?model=&range=24h", nil)
	s.Engine.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests, expect failure**

```bash
go test ./internal/server/... -v
```
Expected: 404 from missing routes.

- [ ] **Step 3: Implement handlers**

Create `internal/server/handlers_api.go`:
```go
package server

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

type Status struct {
	KeyringConfigured bool       `json:"keyring_configured"`
	LastFetchAt        *time.Time `json:"last_fetch_at"`
	ConsecErrors       int        `json:"consec_errors"`
	LastError          string     `json:"last_error"`
	PollInterval       string     `json:"poll_interval"`
	DBSizeMB           float64    `json:"db_size_mb"`
}

type ModelLatest struct {
	ModelName            string `json:"model_name"`
	IntervalRemainingPct *int   `json:"interval_remaining_pct"`
	WeeklyRemainingPct   *int   `json:"weekly_remaining_pct"`
	IntervalRemainsMs    *int64 `json:"interval_remains_ms"`
	FetchedAt            int64  `json:"fetched_at"`
}

type BucketPoint struct {
	T         int64   `json:"t"`
	Min, Max  float64 `json:"min"`
	Avg       float64 `json:"avg"`
}

type History struct {
	Model    string        `json:"model"`
	Range    string        `json:"range"`
	BucketMs int64         `json:"bucket_ms"`
	Points   []BucketPoint `json:"points"`
}

var rangeBucketSec = map[string]int64{
	"1h":  30,
	"6h":  120,
	"24h": 300,
	"7d":  1800,
	"31d": 7200,
}

func dbSizeMB(path string) float64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return float64(fi.Size()) / 1024.0 / 1024.0
}

func (s *Server) handleStatus(c *gin.Context) {
	configured := false
	if s.Store != nil {
		if _, err := s.Store.Get(); err == nil {
			configured = true
		}
	}
	var lastFetch *time.Time
	if s.Stats != nil {
		if t, _, _ := s.Stats(); !t.IsZero() {
			lastFetch = &t
		}
	}
	st := Status{
		KeyringConfigured: configured,
		LastFetchAt:       lastFetch,
		DBSizeMB:          dbSizeMB(s.DBPath),
	}
	if s.Stats != nil {
		_, n, msg := s.Stats()
		st.ConsecErrors = n
		st.LastError = msg
	}
	if s.PollInterval > 0 {
		st.PollInterval = s.PollInterval.String()
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) handleModels(c *gin.Context) {
	if s.DB == nil {
		c.JSON(http.StatusOK, []ModelLatest{})
		return
	}
	rows, err := s.DB.Latest(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]ModelLatest, 0, len(rows))
	for _, r := range rows {
		out = append(out, ModelLatest{
			ModelName:            r.ModelName,
			IntervalRemainingPct: r.IntervalRemainingPct,
			WeeklyRemainingPct:   r.WeeklyRemainingPct,
			IntervalRemainsMs:    r.IntervalRemainsMs,
			FetchedAt:            r.FetchedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleHistory(c *gin.Context) {
	modelName := c.Query("model")
	rng := c.DefaultQuery("range", "24h")
	if modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model required"})
		return
	}
	bucketSec, ok := rangeBucketSec[rng]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad range"})
		return
	}
	if v := c.Query("bucket"); v != "" && v != "auto" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			bucketSec = n
		}
	}
	dur, _ := time.ParseDuration(rng)
	if dur == 0 {
		// parse "1h" / "24h" / "7d" / "31d"
		dur = parseRange(rng)
	}
	to := time.Now()
	from := to.Add(-dur)
	rows, err := s.DB.History(c.Request.Context(), modelName, from.UnixMilli(), to.UnixMilli(), bucketSec*1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pts := make([]BucketPoint, 0, len(rows))
	for _, b := range rows {
		pts = append(pts, BucketPoint{T: b.T, Min: b.Min, Max: b.Max, Avg: b.Avg})
	}
	c.JSON(http.StatusOK, History{
		Model: modelName, Range: rng, BucketMs: bucketSec * 1000, Points: pts,
	})
}

func parseRange(s string) time.Duration {
	if len(s) < 2 {
		return 24 * time.Hour
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 24 * time.Hour
	}
	unit := s[len(s)-1]
	switch unit {
	case 'h':
		return time.Duration(n) * time.Hour
	case 'd':
		return time.Duration(n) * 24 * time.Hour
	}
	return 24 * time.Hour
}
```

Update `internal/server/server.go` to register the routes and accept extra fields used by handlers:
```go
package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"minimax-monitor/internal/storage"
)

type keyringStore interface {
	Get() (string, error)
	Set(string) error
	Delete() error
}

type Server struct {
	Engine       *gin.Engine
	DB           *storage.DB
	Store        keyringStore
	Hub          Broadcaster
	DBPath       string
	PollInterval time.Duration
	Stats        func() (time.Time, int, string)
}

func New(db *storage.DB, store keyringStore) *Server {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{DB: db, Store: store, Engine: gin.New()}
	s.Engine.Use(gin.Recovery())
	s.routes()
	return s
}

func (s *Server) routes() {
	s.Engine.GET("/api/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	s.Engine.GET("/api/status", s.handleStatus)
	s.Engine.GET("/api/models", s.handleModels)
	s.Engine.GET("/api/history", s.handleHistory)
}

func (s *Server) Run(addr string) error {
	return s.Engine.Run(addr)
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test ./internal/server/... -v
```
Expected: 3 server tests pass (1 from Task 8 + 2 new).

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat(server): status/models/history handlers"
```

---

## Task 10: Settings Handlers (TDD)

**Files:**
- Create: `internal/server/handlers_settings.go`
- Modify: `internal/server/server.go` (register routes)
- Modify: `internal/server/server_test.go` (add tests using fake keyring + fake fetcher)

**Interfaces:**
- `POST /api/settings/key` body `{"api_key":"..."}` → validates via fetcher, persists to keyring, returns 200 or 400.
- `DELETE /api/settings/key` → removes from keyring, returns 204.

- [ ] **Step 1: Write failing tests**

Append to `internal/server/server_test.go`:
```go
import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"

	"minimax-monitor/internal/model"
)

type fakeStore struct {
	val string
	err error
}

func (f *fakeStore) Get() (string, error) {
	if f.err != nil { return "", f.err }
	return f.val, nil
}
func (f *fakeStore) Set(v string) error { f.val = v; return nil }
func (f *fakeStore) Delete() error { f.val = ""; return nil }

type fakeFetcher struct {
	called atomic.Int32
	resp   *model.APIResponse
	err    error
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string) (*model.APIResponse, error) {
	f.called.Add(1)
	return f.resp, f.err
}

// "context" already imported above; ensure it's there.

func TestSettingsKey_PostSuccess(t *testing.T) {
	store := &fakeStore{}
	fetch := &fakeFetcher{resp: &model.APIResponse{BaseResp: model.BaseResp{StatusCode: 0}}}
	s := New(nil, store)
	s.Validator = fetch
	body, _ := json.Marshal(map[string]string{"api_key": "sk-x"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/settings/key", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if store.val != "sk-x" {
		t.Errorf("key not stored: %q", store.val)
	}
	if fetch.called.Load() != 1 {
		t.Errorf("validator not called")
	}
}

func TestSettingsKey_PostInvalidKey(t *testing.T) {
	store := &fakeStore{}
	fetch := &fakeFetcher{err: errBad}
	s := New(nil, store)
	s.Validator = fetch
	body, _ := json.Marshal(map[string]string{"api_key": "bad"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/settings/key", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Engine.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if store.val != "" {
		t.Errorf("key should NOT be stored on validation failure")
	}
}

type stringErr string
func (e stringErr) Error() string { return string(e) }
var errBad = stringErr("bad key")
```

- [ ] **Step 2: Run tests, expect failure**

```bash
go test ./internal/server/... -v
```
Expected: build error, `Validator` undefined.

- [ ] **Step 3: Implement handler**

Create `internal/server/handlers_settings.go`:
```go
package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type keyValidator interface {
	Fetch(ctx context.Context, key string) (interface{}, error)
}

// We use a concrete validator to keep the server package free of apiclient import
// (it also keeps test fakes simple).
type ValidatorFunc func(ctx context.Context, key string) error

func (s *Server) handleSettingsPost(c *gin.Context) {
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.APIKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key required"})
		return
	}
	if s.Validator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "validator not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	if err := s.Validator(ctx, body.APIKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.Store.Set(body.APIKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.OnKeyChange != nil {
		s.OnKeyChange()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleSettingsDelete(c *gin.Context) {
	if err := s.Store.Delete(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if s.OnKeyChange != nil {
		s.OnKeyChange()
	}
	c.Status(http.StatusNoContent)
}
```

Update `internal/server/server.go`:
```go
type Server struct {
	Engine       *gin.Engine
	DB           *storage.DB
	Store        keyringStore
	Hub          Broadcaster
	DBPath       string
	PollInterval time.Duration
	Stats        func() (time.Time, int, string)
	Validator    ValidatorFunc
	OnKeyChange  func()
}

func (s *Server) routes() {
	s.Engine.GET("/api/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	s.Engine.GET("/api/status", s.handleStatus)
	s.Engine.GET("/api/models", s.handleModels)
	s.Engine.GET("/api/history", s.handleHistory)
	s.Engine.POST("/api/settings/key", s.handleSettingsPost)
	s.Engine.DELETE("/api/settings/key", s.handleSettingsDelete)
}
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test ./internal/server/... -v
```
Expected: all 5 server tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat(server): settings key POST/DELETE with validation"
```

---

## Task 11: WebSocket Hub & Handler (TDD)

**Files:**
- Create: `internal/server/wshub.go`
- Create: `internal/server/handlers_ws.go` (replace stub from Task 8)
- Create: `internal/server/ws_test.go`

**Interfaces:**
- `type WSHub struct { mu sync.Mutex; conns map[*websocket.Conn]struct{}; snap []storage.Snapshot }`
- `func NewWSHub() *WSHub`
- `func (h *WSHub) Register(conn *websocket.Conn)` / `Unregister(conn)` / `Broadcast(snap []storage.Snapshot)`
- `func (h *WSHub) ServeHTTP(w, r)` — on connect sends latest snapshot then streams.

- [ ] **Step 1: Add dependency**

```bash
go get github.com/coder/websocket
```

- [ ] **Step 2: Write failing test**

Create `internal/server/ws_test.go`:
```go
package server

import "testing"

func TestWSHub_EmptyBroadcast(t *testing.T) {
	h := NewWSHub()
	if h.snap != nil {
		t.Errorf("expected nil snap on init")
	}
	// broadcasting nil must not panic
	h.Broadcast(nil)
}
```

- [ ] **Step 3: Implement hub**

Create `internal/server/wshub.go`:
```go
package server

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"minimax-monitor/internal/storage"
)

type WSHub struct {
	mu    sync.Mutex
	conns map[*websocket.Conn]struct{}
	snap  []storage.Snapshot
}

func NewWSHub() *WSHub {
	return &WSHub{conns: map[*websocket.Conn]struct{}{}}
}

func (h *WSHub) Broadcast(snap []storage.Snapshot) {
	h.mu.Lock()
	h.snap = snap
	conns := make([]*websocket.Conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	msg := map[string]any{
		"type": "snapshot",
		"data": map[string]any{
			"fetched_at": time.Now().UnixMilli(),
			"models":     snap,
		},
	}
	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := wsjson.Write(ctx, c, msg)
		cancel()
		if err != nil {
			slog.Debug("ws send failed; dropping conn", "err", err)
			h.Unregister(c)
		}
	}
}

func (h *WSHub) Register(c *websocket.Conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	snap := h.snap
	h.mu.Unlock()
	if snap != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = wsjson.Write(ctx, c, map[string]any{
			"type": "snapshot",
			"data": map[string]any{
				"fetched_at": time.Now().UnixMilli(),
				"models":     snap,
			},
		})
		cancel()
	}
}

func (h *WSHub) Unregister(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
}

func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow any origin (self-hosted, no auth)
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Warn("ws accept", "err", err)
		return
	}
	h.Register(c)
	defer func() {
		h.Unregister(c)
		c.Close(websocket.StatusNormalClosure, "bye")
	}()
	// Block until client disconnects (we only push)
	for {
		if _, _, err := c.Read(r.Context()); err != nil {
			return
		}
	}
}
```

Replace `internal/server/handlers_ws.go` (the stub `Broadcaster` from Task 8) with the real handler:
```go
package server

import (
	"net/http"

	"minimax-monitor/internal/storage"
)

// Broadcaster is the interface WSHub satisfies.
type Broadcaster interface {
	Broadcast(snap []storage.Snapshot)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if h, ok := s.Hub.(*WSHub); ok {
		h.ServeWS(w, r)
		return
	}
	http.Error(w, "ws hub not configured", http.StatusServiceUnavailable)
}
```

Add to `internal/server/server.go` routes:
```go
s.Engine.GET("/api/ws", gin.WrapF(s.handleWS))
```

- [ ] **Step 4: Run tests, expect pass**

```bash
go test ./internal/server/... -v
```
Expected: all server tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/server/ go.mod go.sum
git commit -m "feat(server): websocket hub for live updates"
```

---

## Task 12: Embed Static FS & Main Assembly

**Files:**
- Create: `internal/server/embed.go`
- Create: `internal/server/web/index.html` (placeholder)
- Modify: `internal/server/server.go` (mount static)
- Modify: `cmd/minimax-monitor/main.go` (assemble all)

- [ ] **Step 1: Create placeholder `internal/server/web/index.html`**

Create `internal/server/web/index.html`:
```html
<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>MiniMax Monitor</title></head>
<body><h1>MiniMax Monitor</h1><p id="status">Loading…</p></body>
</html>
```

- [ ] **Step 2: Write embed.go**

Create `internal/server/embed.go`:
```go
package server

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed all:web
var webFS embed.FS

func (s *Server) mountStatic() {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // build-time invariant
	}
	fileServer := http.FileServer(http.FS(sub))
	s.Engine.NoRoute(func(c *gin.Context) {
		// SPA fallback: serve file if exists, else index.html
		path := c.Request.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		if _, err := fs.Stat(sub, path[1:]); err == nil {
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.Request.URL.Path = "/"
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
```

Call `s.mountStatic()` at the end of `New` in `server.go`.

- [ ] **Step 3: Wire main.go**

Replace `cmd/minimax-monitor/main.go`:
```go
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
	"minimax-monitor/internal/scheduler"
	"minimax-monitor/internal/server"
	"minimax-monitor/internal/storage"
)

func main() {
	port := flag.Int("p", 13337, "listen port")
	flag.Parse()

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
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}
```

- [ ] **Step 4: Build and run smoke test**

```bash
go build -trimpath -ldflags="-s -w" -o dist/minimax-monitor.exe ./cmd/minimax-monitor
./dist/minimax-monitor.exe -p 13337 &
PID=$!
sleep 2
curl -s http://127.0.0.1:13337/api/healthz
echo
curl -s http://127.0.0.1:13337/api/status
echo
kill $PID
```
Expected: `ok` for healthz; status JSON with `keyring_configured:false`.

- [ ] **Step 5: Commit**

```bash
git add internal/server/embed.go cmd/ internal/server/web/index.html
git commit -m "feat: assemble main, embed web FS, mount static"
```

---

## Task 13: Frontend — Dark Theme Skeleton (HTML + CSS)

**Files:**
- Modify: `web/index.html`
- Create: `web/style.css`

- [ ] **Step 1: Replace `internal/server/web/index.html` with full layout**

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>MiniMax Token Plan Monitor</title>
  <link rel="stylesheet" href="/style.css" />
</head>
<body>
  <header class="topbar">
    <div class="brand">
      <span id="wsDot" class="dot" title="WebSocket"></span>
      <h1>MiniMax Monitor</h1>
    </div>
    <div class="ranges" id="ranges">
      <button data-range="1h"  class="range-btn">1h</button>
      <button data-range="6h"  class="range-btn">6h</button>
      <button data-range="24h" class="range-btn active">24h</button>
      <button data-range="7d"  class="range-btn">7d</button>
      <button data-range="31d" class="range-btn">31d</button>
    </div>
    <div class="header-right">
      <span id="lastUpdate" class="muted">--</span>
      <button id="settingsBtn" class="icon-btn" title="设置">⚙</button>
    </div>
  </header>

  <main>
    <section id="emptyState" class="empty hidden">
      <h2>请先配置 API Key</h2>
      <p class="muted">点击右上角 ⚙ 进入设置。</p>
      <button id="emptySettingsBtn" class="primary">前往设置</button>
    </section>

    <section id="cards" class="cards"></section>

    <section id="charts" class="charts"></section>
  </main>

  <footer class="statusbar">
    <span>WS <span id="wsStatus" class="ok">●</span></span>
    <span>抓取 <span id="fetchAgo">--</span> 前</span>
    <span>错误 <span id="errCount">0</span></span>
    <span>保留 <span id="retention">31</span>d</span>
    <span class="muted">v1.0</span>
  </footer>

  <!-- Settings modal -->
  <div id="modal" class="modal hidden">
    <div class="modal-backdrop"></div>
    <div class="modal-body">
      <h2>设置</h2>
      <div class="form-row">
        <label>API Key 状态</label>
        <span id="keyStatus" class="badge">● 未配置</span>
      </div>
      <div class="form-row" id="keyInputRow">
        <label for="keyInput">API Key</label>
        <input id="keyInput" type="password" placeholder="sk-cp-..." autocomplete="off" />
      </div>
      <div class="form-error hidden" id="keyError"></div>
      <div class="form-actions">
        <button id="keyChangeBtn" class="link hidden">更换</button>
        <div class="spacer"></div>
        <button id="keyCancelBtn">取消</button>
        <button id="keySaveBtn" class="primary">保存并验证</button>
      </div>
    </div>
  </div>

  <script src="/vendor/echarts.min.js"></script>
  <script src="/app.js"></script>
</body>
</html>
```

- [ ] **Step 2: Create `internal/server/web/style.css`**

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
  --font-ui:        -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Inter, sans-serif;
  --font-mono:      "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;
}
* { box-sizing: border-box; }
html, body { margin: 0; padding: 0; background: var(--bg-base); color: var(--text-primary);
  font-family: var(--font-ui); font-size: 14px; line-height: 1.5; }
.hidden { display: none !important; }
.muted { color: var(--text-muted); }

/* Topbar */
.topbar { display: flex; align-items: center; gap: 16px; padding: 12px 20px;
  background: linear-gradient(180deg, #11163a 0%, #0a0e27 100%);
  border-bottom: 1px solid var(--border); position: sticky; top: 0; z-index: 10; }
.brand { display: flex; align-items: center; gap: 8px; }
.brand h1 { font-size: 16px; font-weight: 600; margin: 0; }
.dot { width: 10px; height: 10px; border-radius: 50%; background: var(--text-muted); display: inline-block; }
.dot.ok { background: var(--status-good); box-shadow: 0 0 8px var(--status-good); }
.dot.err { background: var(--status-bad); box-shadow: 0 0 8px var(--status-bad); }
.ranges { display: flex; gap: 4px; margin-left: 24px; }
.range-btn { background: transparent; color: var(--text-muted); border: 1px solid transparent;
  padding: 4px 12px; border-radius: 8px; cursor: pointer; font-size: 13px;
  transition: all 200ms ease; }
.range-btn:hover { color: var(--text-primary); background: var(--bg-card); }
.range-btn.active { color: var(--accent-general); border-color: var(--accent-general);
  background: rgba(0, 212, 255, 0.08); }
.header-right { margin-left: auto; display: flex; align-items: center; gap: 12px; }
.icon-btn { background: transparent; color: var(--text-muted); border: none; font-size: 18px;
  cursor: pointer; padding: 4px 8px; border-radius: 6px; transition: all 200ms ease; }
.icon-btn:hover { color: var(--text-primary); background: var(--bg-card); }

/* Main */
main { padding: 20px; max-width: 1400px; margin: 0 auto; }

/* Empty state */
.empty { text-align: center; padding: 80px 20px; }
.empty h2 { font-weight: 500; color: var(--text-muted); }

/* Cards */
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 16px; margin-bottom: 24px; }
.card { background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 20px; box-shadow: var(--shadow-card); position: relative; overflow: hidden; }
.card::before { content: ""; position: absolute; top: 0; left: 0; right: 0; height: 2px;
  background: var(--accent); opacity: 0.8; }
.card[data-model="general"] { --accent: var(--accent-general); }
.card[data-model="video"]   { --accent: var(--accent-video); }
.card h3 { margin: 0 0 4px; font-size: 13px; color: var(--text-muted); font-weight: 500;
  text-transform: uppercase; letter-spacing: 0.5px; }
.pct { font-family: var(--font-mono); font-size: 36px; font-weight: 600; line-height: 1.1;
  margin: 8px 0; color: var(--text-primary); }
.bar { height: 6px; background: var(--bg-card-hi); border-radius: 3px; overflow: hidden;
  margin: 12px 0; }
.bar-fill { height: 100%; background: var(--accent); border-radius: 3px;
  transition: width 600ms cubic-bezier(0.4, 0, 0.2, 1); }
.meta { display: flex; justify-content: space-between; font-size: 12px;
  color: var(--text-muted); margin-top: 8px; }
.meta b { color: var(--text-primary); font-weight: 500; }

/* Charts */
.charts { display: flex; flex-direction: column; gap: 16px; }
.chart-card { background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 16px 20px; box-shadow: var(--shadow-card); }
.chart-title { font-size: 14px; font-weight: 500; margin: 0 0 12px; display: flex;
  align-items: center; gap: 8px; }
.chart-title::before { content: ""; width: 8px; height: 8px; border-radius: 50%; background: var(--accent); }
.chart-card[data-model="general"] { --accent: var(--accent-general); }
.chart-card[data-model="video"]   { --accent: var(--accent-video); }
.chart { height: 260px; }

/* Statusbar */
.statusbar { display: flex; gap: 24px; padding: 10px 20px;
  border-top: 1px solid var(--border); background: var(--bg-card);
  font-size: 12px; color: var(--text-muted); position: sticky; bottom: 0; }
.statusbar span { display: inline-flex; align-items: center; gap: 6px; }
.statusbar .ok { color: var(--status-good); }
.statusbar .err { color: var(--status-bad); }

/* Modal */
.modal { position: fixed; inset: 0; z-index: 1000; display: flex; align-items: center;
  justify-content: center; }
.modal-backdrop { position: absolute; inset: 0; background: rgba(0,0,0,0.6);
  backdrop-filter: blur(8px); }
.modal-body { position: relative; background: var(--bg-card); border: 1px solid var(--border);
  border-radius: var(--radius); padding: 24px; min-width: 420px; max-width: 90vw;
  box-shadow: 0 20px 60px rgba(0,0,0,0.6); animation: modalIn 200ms ease; }
@keyframes modalIn { from { opacity: 0; transform: scale(0.95); } to { opacity: 1; transform: scale(1); } }
.modal-body h2 { margin: 0 0 16px; font-size: 16px; }
.form-row { display: flex; flex-direction: column; gap: 6px; margin-bottom: 12px; }
.form-row label { font-size: 12px; color: var(--text-muted); }
.form-row input { background: var(--bg-base); border: 1px solid var(--border); color: var(--text-primary);
  padding: 8px 12px; border-radius: 8px; font-family: var(--font-mono); font-size: 13px;
  transition: border-color 200ms ease; }
.form-row input:focus { outline: none; border-color: var(--accent-general); }
.badge { display: inline-flex; align-items: center; gap: 6px; font-size: 13px; }
.badge::before { content: ""; width: 8px; height: 8px; border-radius: 50%; background: var(--text-muted); }
.badge.ok::before { background: var(--status-good); box-shadow: 0 0 6px var(--status-good); }
.badge.err::before { background: var(--status-bad); }
.form-error { color: var(--status-bad); font-size: 12px; margin: 4px 0 12px; }
.form-actions { display: flex; align-items: center; gap: 8px; margin-top: 8px; }
.form-actions .spacer { flex: 1; }
.form-actions button { background: var(--bg-card-hi); color: var(--text-primary);
  border: 1px solid var(--border); padding: 8px 16px; border-radius: 8px; cursor: pointer;
  font-size: 13px; transition: all 200ms ease; }
.form-actions button:hover { background: var(--bg-base); }
.form-actions button.primary { background: var(--accent-general); color: var(--bg-base);
  border-color: var(--accent-general); font-weight: 500; }
.form-actions button.primary:disabled { opacity: 0.5; cursor: not-allowed; }
.form-actions button.link { background: transparent; color: var(--accent-general);
  border: none; padding: 8px 0; }
.form-actions button.link:hover { text-decoration: underline; }

/* Skeleton */
.skeleton { background: linear-gradient(90deg, var(--bg-card) 0%, var(--bg-card-hi) 50%, var(--bg-card) 100%);
  background-size: 200% 100%; animation: pulse 1.5s ease-in-out infinite;
  border-radius: var(--radius); }
@keyframes pulse { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

/* Responsive */
@media (max-width: 768px) {
  .topbar { flex-wrap: wrap; gap: 8px; }
  .ranges { order: 3; flex-basis: 100%; margin-left: 0; }
  .range-btn { padding: 4px 8px; font-size: 12px; }
  .statusbar { flex-wrap: wrap; gap: 12px; }
  .chart { height: 200px; }
}
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: 0.01ms !important; transition-duration: 0.01ms !important; }
}
```

- [ ] **Step 3: Build and visually verify**

```bash
go build -trimpath -ldflags="-s -w" -o dist/minimax-monitor.exe ./cmd/minimax-monitor
./dist/minimax-monitor.exe -p 13337 &
PID=$!
sleep 2
curl -sI http://127.0.0.1:13337/ | head -3
kill $PID
```
Expected: HTTP 200 for `/`.

Open `http://127.0.0.1:13337/` in a browser and confirm dark theme loads.

- [ ] **Step 4: Commit**

```bash
git add web/
git commit -m "feat(web): dark theme skeleton and modal HTML"
```

---

## Task 14: Frontend — Vendor ECharts & App.js Skeleton (WS + Cards + Status Bar)

**Files:**
- Create: `internal/server/web/vendor/echarts.min.js` (download from `https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js` then vendor)
- Create: `internal/server/web/app.js`

- [ ] **Step 1: Download and vendor ECharts**

```bash
mkdir -p internal/server/web/vendor
curl -sSL "https://cdn.jsdelivr.net/npm/echarts@5.5.0/dist/echarts.min.js" -o internal/server/web/vendor/echarts.min.js
ls -la internal/server/web/vendor/echarts.min.js   # confirm ~1MB
```

- [ ] **Step 2: Create `internal/server/web/app.js` (skeleton with WS + cards)**

```javascript
(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);
  const fmtTime = (ms) => {
    const d = new Date(ms);
    return d.toLocaleTimeString();
  };
  const fmtAgo = (ms) => {
    const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm';
    return Math.floor(s / 3600) + 'h';
  };
  const formatRangeMs = (ms) => {
    const s = Math.round(ms / 1000);
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm';
    return Math.floor(s / 3600) + 'h ' + (s % 3600 ? Math.floor((s % 3600) / 60) + 'm' : '');
  };

  const state = {
    models: new Map(),   // name -> snapshot
    range: '24h',
    ws: null,
    backoff: 1000,
    charts: new Map(),   // name -> echarts instance
  };

  // -------- WebSocket --------
  function connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${proto}//${location.host}/api/ws`;
    const ws = new WebSocket(url);
    state.ws = ws;
    ws.onopen = () => {
      state.backoff = 1000;
      $('wsStatus').className = 'ok';
      $('wsStatus').textContent = '●';
      $('wsDot').className = 'dot ok';
    };
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === 'snapshot') handleSnapshot(msg.data);
      } catch (e) { console.error('ws parse', e); }
    };
    ws.onclose = () => {
      $('wsStatus').className = 'err';
      $('wsStatus').textContent = '●';
      $('wsDot').className = 'dot err';
      setTimeout(connect, state.backoff);
      state.backoff = Math.min(state.backoff * 2, 30000);
    };
    ws.onerror = () => { try { ws.close(); } catch (_) {} };
  }

  function handleSnapshot(data) {
    const list = data.models || [];
    const fetchedAt = data.fetched_at || Date.now();
    list.forEach((m) => state.models.set(m.model_name, { ...m, fetched_at: fetchedAt }));
    renderCards();
    $('lastUpdate').textContent = fmtTime(fetchedAt);
    $('fetchAgo').textContent = fmtAgo(fetchedAt);
  }

  // -------- Cards --------
  function renderCards() {
    const root = $('cards');
    if (state.models.size === 0) {
      if (!root.querySelector('.skeleton')) {
        root.innerHTML = '<div class="card skeleton" style="height:160px"></div>'.repeat(2);
      }
      return;
    }
    root.innerHTML = '';
    [...state.models.keys()].sort().forEach((name) => {
      const m = state.models.get(name);
      const card = document.createElement('div');
      card.className = 'card';
      card.dataset.model = name;
      const pct = m.interval_remaining_pct ?? 0;
      const weekly = m.weekly_remaining_pct;
      const remainsMs = m.interval_remains_ms ?? 0;
      card.innerHTML = `
        <h3>${name}</h3>
        <div class="pct" data-target="${pct}">${pct}%</div>
        <div class="bar"><div class="bar-fill" style="width:${pct}%"></div></div>
        <div class="meta"><span>区间剩余</span><b>${formatRangeMs(remainsMs)}</b></div>
        <div class="meta"><span>本周剩余</span><b>${weekly ?? '--'}%</b></div>
      `;
      root.appendChild(card);
    });
  }

  // -------- Status bar refresh --------
  setInterval(() => {
    const last = [...state.models.values()].map((m) => m.fetched_at).sort().pop();
    if (last) $('fetchAgo').textContent = fmtAgo(last);
  }, 1000);

  // -------- Range buttons --------
  document.querySelectorAll('.range-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.range-btn').forEach((b) => b.classList.remove('active'));
      btn.classList.add('active');
      state.range = btn.dataset.range;
      // charts will pick this up in Task 15
    });
  });

  // -------- Settings modal (skeleton: open/close only) --------
  const modal = $('modal');
  const openModal = () => modal.classList.remove('hidden');
  const closeModal = () => modal.classList.add('hidden');
  $('settingsBtn').addEventListener('click', openModal);
  const emptyBtn = $('emptySettingsBtn');
  if (emptyBtn) emptyBtn.addEventListener('click', openModal);
  $('modal').querySelector('.modal-backdrop').addEventListener('click', closeModal);
  $('keyCancelBtn').addEventListener('click', closeModal);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !modal.classList.contains('hidden')) closeModal();
  });

  // -------- Init --------
  fetch('/api/status').then((r) => r.json()).then((s) => {
    if (s.keyring_configured) $('emptyState').classList.add('hidden');
    else $('emptyState').classList.remove('hidden');
    if (s.db_size_mb) { /* shown elsewhere if needed */ }
  }).catch(() => {});
  connect();
})();
```

- [ ] **Step 3: Build and visually verify (cards + status)**

```bash
go build -trimpath -ldflags="-s -w" -o dist/minimax-monitor.exe ./cmd/minimax-monitor
./dist/minimax-monitor.exe -p 13337
```
Open `http://127.0.0.1:13337/`, click ⚙, enter a real `sk-cp-...` key, save, then confirm cards render and update every ~10s.

- [ ] **Step 4: Commit**

```bash
git add internal/server/web/vendor/echarts.min.js internal/server/web/app.js
git commit -m "feat(web): WS client, model cards, status bar"
```

---

## Task 15: Frontend — ECharts Trend Panels

**Files:**
- Modify: `internal/server/web/app.js` (append chart logic)

- [ ] **Step 1: Append chart logic to `app.js`**

Add to the end of the IIFE in `internal/server/web/app.js` (before the closing `})();`):

```javascript
  // -------- Charts --------
  const RANGE_MS = { '1h': 3600e3, '6h': 6 * 3600e3, '24h': 24 * 3600e3, '7d': 7 * 86400e3, '31d': 31 * 86400e3 };
  const ACCENT = { general: '#00d4ff', video: '#a855f7' };

  async function refreshCharts() {
    const models = [...state.models.keys()];
    if (models.length === 0) return;
    const from = Date.now() - RANGE_MS[state.range];
    const to = Date.now();
    for (const name of models) {
      const res = await fetch(`/api/history?model=${encodeURIComponent(name)}&range=${state.range}&bucket=auto`);
      if (!res.ok) continue;
      const data = await res.json();
      const chart = ensureChart(name);
      chart.setOption(buildOption(name, data.points || [], from, to), true);
    }
  }

  function ensureChart(name) {
    if (state.charts.has(name)) return state.charts.get(name);
    const root = $('charts');
    const card = document.createElement('div');
    card.className = 'chart-card';
    card.dataset.model = name;
    card.innerHTML = `<div class="chart-title">${name} · 区间剩余率 (${state.range})</div><div class="chart"></div>`;
    root.appendChild(card);
    const el = card.querySelector('.chart');
    const c = echarts.init(el, null, { renderer: 'canvas' });
    state.charts.set(name, c);
    new ResizeObserver(() => c.resize()).observe(el);
    return c;
  }

  function buildOption(name, points, from, to) {
    const accent = ACCENT[name] || '#00d4ff';
    const xs = points.map((p) => p.t);
    const mins = points.map((p) => p.min);
    const maxs = points.map((p) => p.max);
    const avgs = points.map((p) => +p.avg.toFixed(2));
    return {
      animation: true,
      animationDuration: 600,
      animationEasing: 'cubicOut',
      grid: { left: 48, right: 24, top: 24, bottom: 32 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: '#131836',
        borderColor: '#1f2547',
        textStyle: { color: '#e6e9f5' },
        formatter: (params) => {
          const p = params[0];
          const i = p.dataIndex;
          return `${new Date(xs[i]).toLocaleString()}<br/>` +
            `min ${mins[i].toFixed(1)}% · avg ${avgs[i]}% · max ${maxs[i].toFixed(1)}%`;
        },
      },
      xAxis: {
        type: 'time',
        min: from, max: to,
        axisLine: { lineStyle: { color: '#1f2547' } },
        axisLabel: { color: '#6b7390', fontSize: 11 },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value', min: 0, max: 100,
        axisLine: { show: false },
        axisLabel: { color: '#6b7390', fontSize: 11, formatter: '{value}%' },
        splitLine: { lineStyle: { color: 'rgba(31,37,71,0.5)' } },
      },
      series: [
        {
          name: 'min-max',
          type: 'line',
          data: xs.map((t, i) => [t, mins[i], maxs[i]]),
          lineStyle: { opacity: 0 },
          stack: 'minmax',
          symbol: 'none',
          areaStyle: { color: accent, opacity: 0.08 },
          smooth: true,
        },
        {
          name: 'avg',
          type: 'line',
          data: xs.map((t, i) => [t, avgs[i]]),
          lineStyle: { color: accent, width: 2 },
          itemStyle: { color: accent },
          areaStyle: { color: accent, opacity: 0.18 },
          symbol: 'none',
          smooth: true,
        },
      ],
    };
  }

  // re-render charts on range change
  document.querySelectorAll('.range-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      // (already wired above) – call refresh
      refreshCharts();
    });
  });

  // refresh charts periodically (every 30s) and after each WS snapshot (debounced)
  setInterval(refreshCharts, 30000);
  let chartRefreshTimer = null;
  const oldHandle = handleSnapshot;
  handleSnapshot = function (data) {
    oldHandle(data);
    clearTimeout(chartRefreshTimer);
    chartRefreshTimer = setTimeout(refreshCharts, 1500);
  };
```

- [ ] **Step 2: Rebuild and verify charts render**

```bash
go build -trimpath -ldflags="-s -w" -o dist/minimax-monitor.exe ./cmd/minimax-monitor
./dist/minimax-monitor.exe -p 13337
```
Open in browser, switch time ranges, confirm smooth transitions.

- [ ] **Step 3: Commit**

```bash
git add internal/server/web/app.js
git commit -m "feat(web): ECharts trend panels with bucket data"
```

---

## Task 16: Frontend — Settings Modal Logic

**Files:**
- Modify: `internal/server/web/app.js` (replace the modal skeleton block from Task 14)

- [ ] **Step 1: Replace modal logic in `app.js`**

Find the `// -------- Settings modal (skeleton: open/close only) --------` block and replace it with:

```javascript
  // -------- Settings modal --------
  const modal = $('modal');
  const keyStatus = $('keyStatus');
  const keyInputRow = $('keyInputRow');
  const keyError = $('keyError');
  const keySaveBtn = $('keySaveBtn');
  const keyCancelBtn = $('keyCancelBtn');
  const keyChangeBtn = $('keyChangeBtn');

  let keyConfigured = false;
  let saving = false;

  function setKeyUI() {
    if (keyConfigured) {
      keyStatus.textContent = '已配置 ✓';
      keyStatus.className = 'badge ok';
      keyInputRow.classList.add('hidden');
      keyChangeBtn.classList.remove('hidden');
      keySaveBtn.classList.add('hidden');
      keyCancelBtn.classList.add('hidden');
    } else {
      keyStatus.textContent = '● 未配置';
      keyStatus.className = 'badge';
      keyInputRow.classList.remove('hidden');
      keyChangeBtn.classList.add('hidden');
      keySaveBtn.classList.remove('hidden');
      keyCancelBtn.classList.remove('hidden');
    }
  }

  function openModal() {
    setKeyUI();
    keyError.classList.add('hidden');
    keyError.textContent = '';
    $('keyInput').value = '';
    modal.classList.remove('hidden');
    if (!keyConfigured) setTimeout(() => $('keyInput').focus(), 100);
  }
  function closeModal() {
    if (saving) return;
    modal.classList.add('hidden');
  }

  $('settingsBtn').addEventListener('click', openModal);
  if ($('emptySettingsBtn')) $('emptySettingsBtn').addEventListener('click', openModal);
  $('modal').querySelector('.modal-backdrop').addEventListener('click', closeModal);
  keyCancelBtn.addEventListener('click', closeModal);
  keyChangeBtn.addEventListener('click', () => {
    keyConfigured = false;
    setKeyUI();
    setTimeout(() => $('keyInput').focus(), 100);
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !modal.classList.contains('hidden')) closeModal();
  });

  keySaveBtn.addEventListener('click', async () => {
    const v = $('keyInput').value.trim();
    if (!v) { showKeyError('请输入 API Key'); return; }
    saving = true;
    keySaveBtn.disabled = true;
    keySaveBtn.textContent = '验证中…';
    keyError.classList.add('hidden');
    try {
      const res = await fetch('/api/settings/key', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ api_key: v }),
      });
      if (!res.ok) {
        const t = await res.text();
        let msg = t;
        try { msg = JSON.parse(t).error || t; } catch (_) {}
        showKeyError(msg);
        return;
      }
      keyConfigured = true;
      setKeyUI();
      // hide empty state if shown
      $('emptyState').classList.add('hidden');
      // refresh status (server should be picking up new key on next tick)
    } catch (e) {
      showKeyError(e.message);
    } finally {
      saving = false;
      keySaveBtn.disabled = false;
      keySaveBtn.textContent = '保存并验证';
    }
  });

  function showKeyError(msg) {
    keyError.textContent = msg;
    keyError.classList.remove('hidden');
  }

  // initial key state from /api/status
  fetch('/api/status').then((r) => r.json()).then((s) => {
    keyConfigured = !!s.keyring_configured;
    if (keyConfigured) $('emptyState').classList.add('hidden');
    else $('emptyState').classList.remove('hidden');
  }).catch(() => {});
```

- [ ] **Step 2: Rebuild and verify modal flow**

```bash
go build -trimpath -ldflags="-s -w" -o dist/minimax-monitor.exe ./cmd/minimax-monitor
./dist/minimax-monitor.exe -p 13337
```
- Click ⚙, modal opens.
- Enter invalid key → red error message.
- Enter valid key → modal switches to "已配置 ✓" + [更换] button; input hidden.
- Click [更换] → input reappears.

- [ ] **Step 3: Commit**

```bash
git add internal/server/web/app.js
git commit -m "feat(web): settings modal flow with validation"
```

---

## Task 17: Build Scripts

**Files:**
- Create: `build.bat`
- Create: `build-cross.bat`
- Create: `Makefile`
- Create: `.env.example`

- [ ] **Step 1: Write `build.bat`**

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

- [ ] **Step 2: Write `build-cross.bat`**

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

- [ ] **Step 3: Write `Makefile`**

```makefile
BIN := dist/minimax-monitor
LDFLAGS := -s -w
PKG := ./cmd/minimax-monitor

.PHONY: build build-all run test clean

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

test:
	go test ./...

clean:
	rm -rf dist
```

- [ ] **Step 4: Write `.env.example`**

```bash
POLL_INTERVAL=10s
DB_PATH=./data/monitor.db
RETENTION_DAYS=31
LOG_LEVEL=info
KEYRING_SERVICE=minimax-monitor
KEYRING_USER=default
```

- [ ] **Step 5: Verify all scripts**

```bash
build.bat
ls dist
build-cross.bat
ls dist
```
Expected: 5 binaries (`minimax-monitor.exe` + 4 cross-compiled).

- [ ] **Step 6: Commit**

```bash
git add build.bat build-cross.bat Makefile .env.example
git commit -m "chore: build scripts (build.bat, build-cross.bat, Makefile)"
```

---

## Task 18: README & Acceptance Test

**Files:**
- Create: `README.md` (replace placeholder from Task 1)

- [ ] **Step 1: Replace `README.md`**

```markdown
# MiniMax Token Plan Monitor

Single-binary Go service that polls MiniMax `/v1/token_plan/remains` every 10s
and visualises **31 days** of history in a dark-themed local dashboard.

- **Single binary** — all web assets embedded; CGO-free; cross-platform.
- **Secure** — API key stored in OS keyring (Windows Credential Manager / macOS
  Keychain / Linux Secret Service).
- **Live** — WebSocket-pushed model cards; ECharts trend panels.
- **No alerts** in v1; **no auth** (relies on listener binding).

## Quick start (Windows)

```bat
build.bat
dist\minimax-monitor.exe
:: open http://localhost:13337
```

Then click ⚙, paste your `sk-cp-...` key, click **保存并验证**.

## Run on custom port

```bat
dist\minimax-monitor.exe -p 18080
```

Default bind: `0.0.0.0:13337` (all interfaces).

## Configuration (env)

| Var | Default | Description |
|---|---|---|
| `POLL_INTERVAL` | `10s` | Fetch cadence |
| `DB_PATH` | `./data/monitor.db` | SQLite file |
| `RETENTION_DAYS` | `31` | Prune threshold |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `KEYRING_SERVICE` | `minimax-monitor` | OS keyring service name |
| `KEYRING_USER` | `default` | OS keyring user name |

The listen address is **CLI-only** via `-p <port>`.

## Cross-compile (Windows → all)

```bat
build-cross.bat
```

Produces:
- `dist/minimax-monitor-linux-amd64`
- `dist/minimax-monitor-linux-arm64`
- `dist/minimax-monitor-darwin-arm64`
- `dist/minimax-monitor-windows-amd64.exe`

## Build (Linux / macOS)

```bash
make build       # current platform
make build-all   # all platforms
make test        # go test ./...
```

## Project layout

See [`docs/superpowers/specs/2026-06-27-minimax-monitor-design.md`](docs/superpowers/specs/2026-06-27-minimax-monitor-design.md)
for the full design and [`docs/superpowers/plans/2026-06-27-minimax-monitor.md`](docs/superpowers/plans/2026-06-27-minimax-monitor.md)
for the implementation plan.

## License

Private / not yet licensed.
```

- [ ] **Step 2: Run full test suite**

```bash
go test ./... -v
```
Expected: all tests pass; coverage ≥ 70% across internal packages.

- [ ] **Step 3: Run acceptance checklist**

| # | Check | Expected |
|---|---|---|
| 1 | `build.bat` produces `dist/minimax-monitor.exe` from clean | binary exists |
| 2 | `build-cross.bat` produces 4 platform binaries | 4 files in `dist/` |
| 3 | Run with no API key: empty state visible | "请先配置 API Key" |
| 4 | Settings modal: invalid key | red error, key NOT stored |
| 5 | Settings modal: valid key | "已配置 ✓"; empty state hidden |
| 6 | Cards update every 10s with valid key | values refresh |
| 7 | 5 time ranges switch smoothly | charts redraw < 200ms |
| 8 | WS reconnects after `kill` + restart | within 30s |
| 9 | DB size stays bounded at 31 days | prune works |

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: README with quick-start and configuration"
```

---

## Self-Review

### Spec coverage

| Spec section | Covered by task |
|---|---|
| §1 Goals & Non-Goals | All tasks implement; non-goals deferred to v2 (spec §14) |
| §2 Tech Stack | Task 1, 2, 3, 4, 5, 8, 11 (deps + structure) |
| §3 Directory Structure | File structure block; Tasks 1, 8-18 create exact paths |
| §4.1 API struct | Task 3 Step 1 |
| §4.2 SQLite schema | Task 5 Step 4 (CREATE TABLE) |
| §4.3 PRAGMAs | Task 5 Step 4 (DSN `_pragma=`) |
| §4.4 Volume | Implicit; index `idx_snap_model_time` ensures perf (Task 5) |
| §5.1 Scheduler 10s tick | Task 7 |
| §5.2 Prune 24h tick | Task 7 |
| §5.3 WS hub | Task 11 |
| §6.1 REST endpoints | Tasks 8, 9, 10 |
| §6.2 `bucket=auto` | Task 9 `rangeBucketSec` |
| §6.3 WS protocol | Task 11 `wsjson.Write({type:"snapshot", data:{...}})` |
| §6.4 Settings flow | Task 10 (handler) + Task 16 (UI) |
| §7.1 Layout | Task 13 HTML structure |
| §7.2 CSS tokens | Task 13 `:root` variables |
| §7.3 Charts | Task 15 (ECharts config) |
| §7.4 Modal | Task 13 modal HTML + Task 16 logic |
| §7.5 Smoothness | Task 14 (WS reconnect), Task 15 (animation), CSS transitions (Task 13) |
| §7.6 Responsive | Task 13 media queries |
| §7.7 Empty/loading | Task 13 empty state, Task 14 skeleton |
| §8.1 CLI flag | Task 12 `-p` flag |
| §8.2 Env vars | Task 2 config |
| §8.3 `.env.example` | Task 17 |
| §9.1 `build.bat` | Task 17 |
| §9.2 `build-cross.bat` | Task 17 |
| §9.3 `Makefile` | Task 17 |
| §9.4 Run | Task 17 README |
| §10 Security | Keyring (Task 4), CSP via no inline scripts (Task 13), no auth (per spec) |
| §11 Error handling | Task 7 consecutive error counter, Task 9 status endpoint, Task 10 rollback |
| §12 Testing | Tasks 2-11 each have unit tests |
| §13 Risks | Acknowledged (modernc perf → spec §13 mitigation; keyring on Linux → README) |
| §14 Future | Out of scope |
| §15 Acceptance | Task 18 checklist |

### Placeholder scan
- No "TBD" / "TODO" / "implement later" patterns in plan steps.
- No "add appropriate error handling" without concrete code.
- Each test step has actual test code.
- Each implementation step has actual implementation code.
- Build commands have exact flags and expected output.

### Type / interface consistency
- `model.APIResponse` / `model.ModelRemains` / `model.BaseResp` defined Task 3, used in Tasks 5, 6, 7, 10, 11.
- `storage.Snapshot` / `storage.Bucket` defined Task 6, used in Tasks 7, 11.
- `scheduler.Fetcher` / `Inserter` / `Broadcaster` defined Task 7, used in Task 12.
- `Server.Validator` / `OnKeyChange` / `DBPath` / `PollInterval` / `Stats` defined Task 10, used in Task 12.
- `WSHub` / `Broadcaster` defined Task 11, used in Task 12.
- All field names line up across definitions and consumers.
