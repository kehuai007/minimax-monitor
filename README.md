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

## Alerting (Feishu / Lark)

The dashboard can post interactive-card notifications to a Feishu (or Lark)
custom bot when the configured interval-remaining threshold is crossed.

1. Click ⚙ in the header → expand "告警通知".
2. Paste your webhook URL (with optional `?secret=...` — signing is auto-detected).
3. Set the threshold (default 80). Alerts fire when remaining % drops at or
   below this value, then once more for every additional 1% drop until the
   5-minute window resets.
4. Click "保存", then "发送测试" to verify delivery.

Disable to clear all dedup state — the next enable starts a fresh window.

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
