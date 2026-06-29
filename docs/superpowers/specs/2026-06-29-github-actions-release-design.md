# GitHub Actions Release Workflow — Design Spec

**Date**: 2026-06-29
**Status**: Draft (pending user review)
**Target**: A GitHub Actions workflow that triggers on `v*.*.*` tag pushes,
runs the test suite, cross-compiles 4 platform binaries, generates a
SHA256SUMS file, and publishes everything to a GitHub Release marked
`latest`.

---

## 1. Goals & Non-Goals

### 1.1 Goals
- Auto-build 4 platform binaries on tag push: `linux/amd64`, `linux/arm64`,
  `darwin/arm64`, `windows/amd64`.
- Inject the tag version (e.g. `1.2.3`) into the binary at build time so
  `slog.Info("starting", "version", ...)` reports the real version.
- Run `go test ./...` as a gate before any release is published.
- Publish a GitHub Release tagged `latest` with the 4 binaries plus a
  `SHA256SUMS` file.
- Auto-generate release notes from merged PRs since the previous tag.

### 1.2 Non-Goals
- Draft / prerelease workflow — every release is `latest`. (The release
  action is configured to leave both flags unset, which is equivalent to
  `prerelease: false, draft: false`.)
- Additional platforms (linux/386, darwin/amd64, *BSD) — out of scope; can
  be added by extending the matrix later.
- Source code archives, SBOM, or build provenance attestations — not
  requested.
- Branch-based "nightly" builds — out of scope; tag-only.
- Reproducible build verification / cosign signing — out of scope.

---

## 2. Trigger

```yaml
on:
  push:
    tags:
      - 'v*'
```

GitHub's tag filter uses fnmatch glob syntax (not regex), so a
`vX.Y.Z`-shaped pattern is expressed as `'v*'`. The format is then
validated in the `test` job's first step (see §4.2), which exits cleanly
with a notice if the tag is e.g. `vscratch` or `v` alone.

Accepted forms (case-sensitive): `v1.2.3`, `v2.0.0-rc.1`,
`v1.0.0-beta.2`. Anything else is skipped silently.

The tag ref name (`${GITHUB_REF_NAME}`, e.g. `v1.2.3`) is the single
source of truth for the release name, release tag, and embedded version
(stripped of the `v` prefix).

---

## 3. Version Injection

### 3.1 New file: `internal/version/version.go`

```go
// Package version exposes the build-time version string.
package version

// Version is set at build time via:
//   go build -ldflags "-X minimax-monitor/internal/version.Version=1.2.3"
var Version = "dev"
```

Default `"dev"` distinguishes local builds (`go run`, `make build`) from
CI builds.

### 3.2 Changes to `cmd/minimax-monitor/main.go`

- Add `"minimax-monitor/internal/version"` to the import block.
- After `flag.Parse()` and before `config.Load()`, log the version:
  ```go
  slog.Info("starting", "version", version.Version, "port", *port)
  ```

### 3.3 ldflags in workflow

```
-X minimax-monitor/internal/version.Version=${GITHUB_REF_NAME#v}
```

(Go's `env#pattern` strips a leading `v` from the tag, e.g. `v1.2.3` →
`1.2.3`.)

---

## 4. Build Matrix

A single `ubuntu-latest` runner cross-compiles all 4 targets (CGO=0
makes this trivial). One `test` job gates the `release` job. The test
job also enforces the `vX.Y.Z` tag format so non-semver tags like
`vscratch` don't trigger a real run.

### 4.1 Job definitions

```yaml
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

      # ---------- 仅最后一个切片跑汇总 + Release ----------
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

### 4.2 Why matrix slices on the same runner

All 4 matrix entries run on `ubuntu-latest` — they are separate runners,
so their filesystems are isolated. The "collect + sign + release" steps
must therefore be hosted in a specific slice (we pick
`windows/amd64`) that downloads all 4 artifacts and then runs the
release action.

---

## 5. Permissions & Repo Prerequisites

```yaml
permissions:
  contents: write
```

**Repository-side prerequisite (not in the workflow file)**: in
GitHub → Settings → Actions → General → Workflow permissions, select
"Read and write permissions". The default "Read repository contents
and packages permissions" will silently fail to create the release.

No secrets are required — `GITHUB_TOKEN` is auto-provisioned.

---

## 6. End-to-End Flow

```
git push origin v1.2.3
   │
   ▼
test job (ubuntu-latest)
   ├─ checkout
   ├─ setup-go (1.25 from go.mod)
   ├─ validate tag format (vX.Y.Z)  ── invalid? skip=true, exit 0
   └─ go test ./...   (skipped if skip=true)
   │         │
   │         └─ pass (and skip!=true) ──► release job
   ▼
release job (ubuntu-latest, matrix 4x)
   ├─ linux/amd64   ─► build ─► upload-artifact
   ├─ linux/arm64   ─► build ─► upload-artifact
   ├─ darwin/arm64  ─► build ─► upload-artifact
   └─ windows/amd64 ─► build ─► upload-artifact
                                 ↓
                       (windows slice only)
                                 ↓
                       download-artifact (all 4)
                                 ↓
                       sha256sum → SHA256SUMS
                                 ↓
                       softprops/action-gh-release@v2
                                 ↓
                       Release v1.2.3 (latest) on GitHub
```

---

## 7. Failure Modes

| Failure | Behavior |
|---|---|
| Tag doesn't match `v*` glob | Workflow never triggers. |
| Tag matches `v*` but not `vX.Y.Z` (e.g. `vscratch`, `v1.2`) | `validate` step sets `skip=true`, exits 0. Test step is skipped; release job's `if: needs.test.outputs.skip != 'true'` skips the whole job. |
| `go test` fails | `test` job fails, `release` job never starts (needs: test). Tag stays as-is; fix and re-push a new tag. |
| One matrix slice fails (e.g. darwin/arm64 build error) | `fail-fast: false` lets the other 3 finish; the windows slice's release step has `fail_on_unmatched_files` and the binaries are missing, so the release is not published. |
| Release already exists for this tag | `softprops/action-gh-release@v2` updates the existing release by default; assets are overwritten. To prevent re-tagging, validate locally before pushing. |

---

## 8. Future Extensions (Not Built Now)

- `prerelease: ${{ contains(github.ref_name, '-') }}` to mark `v2.0.0-rc.1`
  style tags as pre-release automatically.
- `linux/386` and `darwin/amd64` matrix additions.
- SBOM generation (`anchore/sbom-action`) and SLSA provenance
  attestation.
- A `nightly` cron job that pushes a rolling `nightly` tag and
  publishes a pre-release.

---

## 9. Files Changed / Created

| Path | Change |
|---|---|
| `.github/workflows/release.yml` | **created** — full workflow |
| `internal/version/version.go` | **created** — `var Version = "dev"` |
| `cmd/minimax-monitor/main.go` | **edit** — add import + 1 startup log line |

Total: 1 new Go file (~7 lines), 1 new workflow YAML file (~80
lines), 2-line edit to existing main.go.
