# GitHub Actions Release Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a GitHub Actions workflow that, on `vX.Y.Z` tag push, runs the test suite, cross-compiles 4 platform binaries with the tag version embedded, generates a `SHA256SUMS` file, and publishes a `latest` GitHub Release.

**Architecture:** New `internal/version` package exposes an exported `Version` variable set at build time via `-ldflags -X`. `cmd/minimax-monitor/main.go` imports it and logs the resolved version on startup. A new `.github/workflows/release.yml` defines two jobs: `test` (gates the rest, also validates the tag format) and `release` (4-slice matrix on `ubuntu-latest` cross-compiling, with the final slice collecting all artifacts, computing SHA256, and calling `softprops/action-gh-release@v2`).

**Tech Stack:** Go 1.25, `actions/checkout@v4`, `actions/setup-go@v5`, `actions/upload-artifact@v4`, `actions/download-artifact@v4`, `softprops/action-gh-release@v2`, sha256sum, fnmatch (GitHub tag filter).

## File Structure

```
minimax-monitor/
├── .github/
│   └── workflows/
│       └── release.yml             # NEW — the workflow (≈80 lines)
├── internal/
│   └── version/
│       ├── version.go              # NEW — exported Version var
│       └── version_test.go         # NEW — default-value contract test
├── cmd/
│   └── minimax-monitor/
│       └── main.go                 # MODIFY — +1 import, +1 slog line
├── docs/
│   └── superpowers/
│       └── specs/
│           └── 2026-06-29-github-actions-release-design.md  # already exists
```

## Conventions

- TDD where it adds value: the `internal/version` package gets a default-value test first. Workflow YAML has no runtime test, so we validate syntax with `python -c "import yaml; ..."` and lint with `actionlint` if available.
- All commit messages follow `type(scope): subject` (matching the repo's existing log: `feat(alert)`, `fix(notify)`, `docs:`).
- Each task ends with a single commit. Never batch commits.
- Bash on Windows (`win32`): use `grep -a` or `findstr` against binaries as needed. The plan is written for the native shell of the executor — use forward slashes and `bash` semantics per the working environment.

---

## Task 1: Create the `internal/version` package with its default-value test

**Files:**
- Create: `internal/version/version.go`
- Create: `internal/version/version_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/version/version_test.go`:

```go
package version

import "testing"

func TestVersionDefault(t *testing.T) {
	if Version != "dev" {
		t.Errorf("default Version = %q, want %q", Version, "dev")
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run:
```bash
go test ./internal/version/...
```

Expected output: `FAIL` with `internal/version` — package not found.

- [ ] **Step 3: Implement the package**

Create `internal/version/version.go`:

```go
// Package version exposes the build-time version string for minimax-monitor.
package version

// Version is set at build time via:
//   go build -ldflags "-X minimax-monitor/internal/version.Version=1.2.3"
//
// Local builds (no ldflags) report "dev".
var Version = "dev"
```

- [ ] **Step 4: Run the test, expect pass**

Run:
```bash
go test ./internal/version/...
```

Expected output: `ok  minimax-monitor/internal/version` (one test, pass).

- [ ] **Step 5: Commit**

```bash
git add internal/version/version.go internal/version/version_test.go
git commit -m "feat(version): add internal/version package with Version var"
```

---

## Task 2: Wire the version into main.go's startup log

**Files:**
- Modify: `cmd/minimax-monitor/main.go:1-23` (import block)
- Modify: `cmd/minimax-monitor/main.go:25-27` (first statements after `flag.Parse()`)

- [ ] **Step 1: Add the import**

In `cmd/minimax-monitor/main.go`, the current import block is:

```go
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
	"minimax-monitor/internal/notify"
	"minimax-monitor/internal/scheduler"
	"minimax-monitor/internal/server"
	"minimax-monitor/internal/storage"
)
```

Add `"minimax-monitor/internal/version"` to the project-local imports group. The updated block:

```go
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
	"minimax-monitor/internal/notify"
	"minimax-monitor/internal/scheduler"
	"minimax-monitor/internal/server"
	"minimax-monitor/internal/storage"
	"minimax-monitor/internal/version"
)
```

- [ ] **Step 2: Add the startup log line**

Locate the `main()` body. The current opening is:

```go
func main() {
	port := flag.Int("p", 13337, "listen port")
	flag.Parse()
	cfg := config.Load()
	setupLogging(cfg.LogLevel)
```

Insert one new line between `flag.Parse()` and `cfg := config.Load()`:

```go
func main() {
	port := flag.Int("p", 13337, "listen port")
	flag.Parse()
	slog.Info("starting minimax-monitor", "version", version.Version, "port", *port)
	cfg := config.Load()
	setupLogging(cfg.LogLevel)
```

- [ ] **Step 3: Verify the package compiles and existing tests still pass**

Run:
```bash
go vet ./...
go build ./...
go test ./...
```

Expected:
- `go vet` — no output, exit 0
- `go build` — no output, exit 0
- `go test` — all packages `ok`, exit 0

If `go test` reports any failure unrelated to this change, STOP and investigate — do not commit a broken test suite.

- [ ] **Step 4: Commit**

```bash
git add cmd/minimax-monitor/main.go
git commit -m "feat(cmd): log resolved version on startup"
```

---

## Task 3: Verify version injection works via ldflags (smoke test)

**Files:** none (no source changes; this is a local-only verification)

- [ ] **Step 1: Build the Linux binary with a test version string**

Run:
```bash
mkdir -p dist
go build -trimpath \
  -ldflags="-s -w -X minimax-monitor/internal/version.Version=ci-smoke-1.2.3" \
  -o dist/minimax-monitor-ci-smoke \
  ./cmd/minimax-monitor
```

Expected: no output, exit 0. The binary `dist/minimax-monitor-ci-smoke` should exist.

- [ ] **Step 2: Confirm the version string is present in the binary**

Run (Linux/macOS):
```bash
grep -aF 'ci-smoke-1.2.3' dist/minimax-monitor-ci-smoke && echo "VERSION_STRING_PRESENT"
```

On Windows (if running `git bash`):
```bash
grep -aF 'ci-smoke-1.2.3' dist/minimax-monitor-ci-smoke >/dev/null && echo "VERSION_STRING_PRESENT"
```

Expected output line: `VERSION_STRING_PRESENT`.

If absent, the ldflag is being silently dropped — re-check the import path in the `-X` argument exactly matches `minimax-monitor/internal/version.Version` (case-sensitive, no trailing slash).

- [ ] **Step 3: Run the binary briefly and confirm the startup log line includes the version**

Run (with a short timeout so the server doesn't block):
```bash
cd dist
timeout 2 ./minimax-monitor-ci-smoke -p 13901 2>&1 | head -5
cd ..
```

Expected output (first few lines):
```
time=... msg="starting minimax-monitor" version=ci-smoke-1.2.3 port=13901
```

(The server may or may not print additional lines before `timeout` kills it — that's fine.)

- [ ] **Step 4: Clean up the smoke-test binary**

Run:
```bash
rm -f dist/minimax-monitor-ci-smoke
```

No commit — this task is verification only.

---

## Task 4: Create the release workflow file

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create the directory and file**

Create `.github/workflows/release.yml` with the following content (this is the entire file — copy verbatim, do not trim):

```yaml
name: release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write

jobs:
  test:
    runs-on: ubuntu-latest
    outputs:
      skip: ${{ steps.validate.outputs.skip }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Validate tag format
        id: validate
        shell: bash
        run: |
          if [[ ! "$GITHUB_REF_NAME" =~ ^v[0-9]+(\.[0-9]+){2}(-.+)?$ ]]; then
            echo "::notice::Tag '$GITHUB_REF_NAME' does not match vX.Y.Z; skipping release."
            echo "skip=true" >> "$GITHUB_OUTPUT"
            exit 0
          fi

      - name: Run tests
        if: steps.validate.outputs.skip != 'true'
        run: go test ./...

  release:
    needs: test
    if: needs.test.outputs.skip != 'true'
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { goos: linux,  goarch: amd64, ext: '',    artifact: binary-linux-amd64 }
          - { goos: linux,  goarch: arm64, ext: '',    artifact: binary-linux-arm64 }
          - { goos: darwin, goarch: arm64, ext: '',    artifact: binary-darwin-arm64 }
          - { goos: windows, goarch: amd64, ext: '.exe', artifact: binary-windows-amd64 }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Resolve version
        id: ver
        shell: bash
        run: echo "version=${GITHUB_REF_NAME#v}" >> "$GITHUB_OUTPUT"

      - name: Build ${{ matrix.goos }}/${{ matrix.goarch }}
        shell: bash
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
          CGO_ENABLED: 0
        run: |
          mkdir -p dist
          out="dist/minimax-monitor-${{ matrix.goos }}-${{ matrix.goarch }}${{ matrix.ext }}"
          go build -trimpath \
            -ldflags="-s -w -X minimax-monitor/internal/version.Version=${{ steps.ver.outputs.version }}" \
            -o "$out" ./cmd/minimax-monitor

      - name: Upload binary artifact
        uses: actions/upload-artifact@v4
        with:
          name: ${{ matrix.artifact }}
          path: dist/minimax-monitor-${{ matrix.goos }}-${{ matrix.goarch }}${{ matrix.ext }}
          if-no-files-found: error

      - name: Collect binaries
        if: matrix.goos == 'windows' && matrix.goarch == 'amd64'
        uses: actions/download-artifact@v4
        with:
          path: dist
          merge-multiple: true

      - name: Compute SHA256SUMS
        if: matrix.goos == 'windows' && matrix.goarch == 'amd64'
        shell: bash
        run: |
          cd dist
          sha256sum minimax-monitor-linux-amd64 \
                    minimax-monitor-linux-arm64 \
                    minimax-monitor-darwin-arm64 \
                    minimax-monitor-windows-amd64.exe \
            > SHA256SUMS
          cat SHA256SUMS

      - name: Create GitHub Release
        if: matrix.goos == 'windows' && matrix.goarch == 'amd64'
        uses: softprops/action-gh-release@v2
        with:
          tag_name: ${{ github.ref_name }}
          name: ${{ github.ref_name }}
          generate_release_notes: true
          fail_on_unmatched_files: true
          files: |
            dist/minimax-monitor-linux-amd64
            dist/minimax-monitor-linux-arm64
            dist/minimax-monitor-darwin-arm64
            dist/minimax-monitor-windows-amd64.exe
            dist/SHA256SUMS
```

- [ ] **Step 2: Validate YAML syntax**

Run:
```bash
python -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml')); print('YAML_OK')"
```

(If `python` is not on PATH, try `python3` or `py -3` — use whichever the executor provides.)

Expected output: `YAML_OK`.

If YAML parsing fails, the error message will point at the line/column. Re-open `.github/workflows/release.yml`, fix the syntax, and re-run.

- [ ] **Step 3: Optional — validate with `actionlint` if installed**

Run:
```bash
if command -v actionlint >/dev/null 2>&1; then
  actionlint .github/workflows/release.yml
else
  echo "actionlint not installed; skipping (YAML syntax check above is sufficient)"
fi
```

Expected: either no output (lint pass) or the explicit "skipping" message.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add release workflow triggered by vX.Y.Z tag pushes"
```

---

## Task 5: Update README with the release section

**Files:**
- Modify: `README.md` (insert a new section before `## License`)
- Modify: `README.zh-CN.md` (same insertion, translated)

- [ ] **Step 1: Add the section to README.md**

Locate the line `## License` in `README.md` and insert the following block immediately **above** it:

```markdown
## Releases

Push a `vX.Y.Z` tag (e.g. `v1.2.3` or `v2.0.0-rc.1`) to trigger the
release workflow in `.github/workflows/release.yml`. The workflow:

1. Runs `go test ./...` as a gate.
2. Cross-compiles four binaries (linux/amd64, linux/arm64,
   darwin/arm64, windows/amd64) with the tag version embedded.
3. Generates `SHA256SUMS` for the four binaries.
4. Publishes a `latest` GitHub Release with the binaries and the
   `SHA256SUMS` file.

Cut a release:

```bash
git tag v1.2.3
git push origin v1.2.3
```

The repository's "Read and write permissions" workflow setting must be
enabled (Settings → Actions → General → Workflow permissions) for
`GITHUB_TOKEN` to publish the release.

```

- [ ] **Step 2: Add the Chinese version to README.zh-CN.md**

Insert the equivalent section above `## License` in `README.zh-CN.md`:

```markdown
## 发布版本

推送形如 `vX.Y.Z`（例如 `v1.2.3` 或 `v2.0.0-rc.1`）的标签即可触发
`.github/workflows/release.yml` 发布工作流。流程：

1. 先运行 `go test ./...` 作为门控；
2. 交叉编译 4 个二进制（linux/amd64、linux/arm64、darwin/arm64、
   windows/amd64），并将版本号嵌入二进制；
3. 生成 4 个二进制的 `SHA256SUMS`；
4. 在 GitHub Release 页面发布为 `latest`，包含二进制与 `SHA256SUMS`。

发布命令：

```bash
git tag v1.2.3
git push origin v1.2.3
```

仓库需在 Settings → Actions → General → Workflow permissions 中开启
"Read and write permissions"，否则 `GITHUB_TOKEN` 无权发布 Release。

```

- [ ] **Step 3: Commit**

```bash
git add README.md README.zh-CN.md
git commit -m "docs: document tag-based release workflow in README"
```

---

## Task 6: Final end-to-end build verification (local)

**Files:** none (verification only)

- [ ] **Step 1: Run the existing cross-compile script to confirm no regressions**

Run:
```bash
make build-all
```

Expected: four binaries appear under `dist/`:
- `dist/minimax-monitor-linux-amd64`
- `dist/minimax-monitor-linux-arm64`
- `dist/minimax-monitor-darwin-arm64`
- `dist/minimax-monitor-windows-amd64.exe`

- [ ] **Step 2: Run the full test suite one more time**

Run:
```bash
go test ./...
```

Expected: all packages report `ok`, exit 0.

- [ ] **Step 3: Clean up the dist directory**

Run:
```bash
make clean
```

Expected: `dist/` is removed.

- [ ] **Step 4: No commit — this task is a final local sanity check.**

---

## Done Criteria

All of the following must be true before the work is considered complete:

- [x] `internal/version/version.go` exists with `var Version = "dev"`.
- [x] `internal/version/version_test.go` exists and passes.
- [x] `cmd/minimax-monitor/main.go` logs the resolved version on startup.
- [x] `go test ./...` passes.
- [x] `go vet ./...` passes.
- [x] Local cross-compile (Task 6) produces all four binaries.
- [x] `dist/minimax-monitor-<os>-<arch>` binaries contain the injected
      version string when built with `-ldflags "...version.Version=..."`.
- [x] `.github/workflows/release.yml` exists, is valid YAML, and is
      committed.
- [x] README.md and README.zh-CN.md document the release process.
- [x] `git log --oneline` shows the four commits from Tasks 1, 2, 4, 5.

## Post-Implementation: Manual GitHub-Side Verification

After the four commits land, perform one end-to-end smoke test on
GitHub. This cannot be done from a local shell — push a real tag and
inspect the Actions tab + Releases page.

1. Confirm the repo setting: Settings → Actions → General → Workflow
   permissions → "Read and write permissions". (Default is read-only;
   the release step would fail with a 403 otherwise.)
2. Push a test tag (the workflow will skip it because the regex won't
   match):

   ```bash
   git tag v-scratch-test
   git push origin v-scratch-test
   ```

   Expected: workflow runs, `validate` step logs the `::notice::`, the
   test step is skipped, the release job is skipped. Total green.
3. Push a valid semver tag:

   ```bash
   git tag v0.0.1-test
   git push origin v0.0.1-test
   ```

   Expected:
   - `test` job passes
   - `release` job's 4 matrix slices all green
   - A Release `v0.0.1-test` appears on the Releases page, marked
     `latest` (it'll be the only one)
   - Five assets attached: 4 binaries + `SHA256SUMS`
   - The `SHA256SUMS` content matches the binary hashes computed
     locally with `sha256sum dist/*`
4. Delete the test tag/release when satisfied:

   ```bash
   git push origin --delete v0.0.1-test
   gh release delete v0.0.1-test --yes   # if gh CLI is installed
   ```

## Out of Scope (Documented for Future Work)

- Auto-marking `v2.0.0-rc.1` style tags as `prerelease: true`. The
  current spec publishes all valid `v*.*.*` tags as `latest`.
- Additional platforms (linux/386, darwin/amd64, *BSD). Add to the
  matrix in Task 4 and to the SHA256SUMS list when needed.
- SBOM / SLSA provenance attestations.
- Nightly cron-based pre-release builds.
