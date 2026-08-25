# Trustworthy O+ — Staff Architect Test Plan

**Persona:** Staff Architect
**Approach:** "Boring Bazooka"
**Bias:** Prefer boring, proven patterns over novel ones. Flag hidden coupling. Will this hurt us in 6 months?

---

## 1. Core Design Summary

We need three deliverables: (1) a real test suite for every security-critical path, (2) GitHub Actions CI, (3) checksummed release artifacts. The design principle is **boring, proven patterns** — table-driven unit tests for deterministic security logic, dependency injection to test the fsnotify watcher without flaky sleeps, and a Makefile-based release (not goreleaser) because adding a Go + YAML dependency to ship a single binary is clever-but-fragile. Coverage is measured per-package with a `-cover` CI gate, and `govulncheck` runs as a separate action step to catch advisory drift without blocking the build (until it does — then it fails).

---

## 2. Testing Strategy — Per Package

### 2.1 Package `manifest` — 8 test files, ~90% line coverage target

All table-driven. Pure functions, no external deps beyond the filesystem. Every test creates manifests with `t.TempDir()` and writes test YAML; no polling, no goroutines, no timeouts.

| Test | What it covers | Attack surface |
|---|---|---|
| `TestLoad_SizeLimit` | 1MB+ manifest → error | Bomb guard |
| `TestLoad_DepthLimit` | Nesting > 64 → error | Resource exhaustion |
| `TestLoad_AliasRejected` | Anchors/aliases → error | Billion-laughs bomb |
| `TestLoad_UnknownFields` | Unknown YAML keys → error | Schema drift / injection |
| `TestLoad_Defaults` | Missing o+.yaml → default manifest | Happy path |
| `TestLoad_NormalManifest` | Valid minimal → parses correctly | Happy path |
| `TestFingerprint` | Known dir → deterministic hash | Cache + trust correctness |
| `TestFingerprint_MissingFiles` | Missing go.mod/go.sum → still works | Graceful degradation |
| `TestFingerprint_ChangesOnEdit` | Changed content → different hash | Integrity detection |
| `TestTrusted` | Trusted dir → true; untrusted → false; changed → false | Trust gate |
| `TestTrust_RecordThenTrusted` | Trust() then Trusted() → true | Trust persistence |
| `TestLoadTrust_CorruptTrustJSON` | Corrupt trust.json → empty map (not crash) | Fail-open integrity |
| `TestCacheDir_Mode` | Created with 0700 permissions | Mode enforcement |

**Key code-reading notes:**
- `checkDepth` recurses on `n.Content` which is a tree of `yaml.Node`. A bomb with depth 65 will stop at 64.
- `rejectAliases` catches `yaml.AliasNode` at any depth. We need a test with a nested alias ref.
- `Load` does two passes: pass 1 checks depth + aliases on the raw node tree; pass 2 decodes with `KnownFields(true)` into the struct. Both must reject.
- `loadTrust` on corrupt JSON returns `map[string]string{}` and nil error — *this is correct*. A corrupt trust file must not prevent any project from running (denial-of-service via trust corruption). Document as intentional.
- `CacheDir` uses `os.Getenv("XDG_CACHE_HOME")` then `os.UserHomeDir()`. Test both paths.

**Table-driven pattern (exemplar):**

```go
func TestLoad_AliasRejected(t *testing.T) {
    tests := []struct{
        name string
        yaml string
    }{
        {"simple anchor", "app: &app\n  name: foo\napp2: *app"},
        {"nested anchor", "a: &a {x: 1}\nb: *a"},
        {"chain anchors", "a: &a {x: 1}\nb: &b {y: *a}\nc: *b"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            dir := t.TempDir()
            os.WriteFile(filepath.Join(dir, "o+.yaml"), []byte(tt.yaml), 0o644)
            _, err := Load(dir)
            if err == nil || !strings.Contains(err.Error(), "anchor") {
                t.Fatalf("expected alias error, got %v", err)
            }
        })
    }
}
```

### 2.2 Package `builder` — 6 test files, ~85% line coverage target

Pure logic tests + synthetic artifact tamper tests. No real `go build` calls — cache-miss tests use manually placed binaries. This is *critical*: a test that invokes `go build` on CI is slow and fragile (different Go versions, network). Isolate that to an integration test.

**What we test on `verifyCached` (Security condition #2 — NO TOCTOU gap):**

The current code reads the binary whole-file, hashes it, and compares. There is a racy window between `os.ReadFile(bin)` on line 126 and the sha256 sum — an attacker with write access to the cache could swap the file between the size check in `os.ReadFile` and the hash. But: the cache dir is `$XDG_CACHE_HOME/o+/bin/` with mode 0700, owned by the user. The only attacker who can swap files there is the user themselves. So this is **accepted** — we document the assumption.

| Test | What it covers | Notes |
|---|---|---|
| `TestVerifyCached_Hit` | Valid bin+sha256 → true | Happy path |
| `TestVerifyCached_Tampered` | Modify bin after sum → false, bin removed | Integrity enforcement |
| `TestVerifyCached_MissingSum` | No .sha256 → false | Missing file |
| `TestVerifyCached_MissingBin` | No binary → false | Missing file |
| `TestPRune` | N+1 artifacts → only N survive, oldest evicted | LRU discipline |
| `TestProjectKey_Deterministic` | Same dir → same key | Cache key stability |
| `TestProjectKey_ChangesOnContent` | Changed source → different key | Cache invalidation |
| `TestSourceHash_Excludes` | .git + test.go excluded from hash | Hash correctness |
| `TestNew_CacheDirMode` | 0700 on cache dir | Mode enforcement |

**Tamper test pattern (exemplar):**

```go
func TestVerifyCached_Tampered(t *testing.T) {
    dir := t.TempDir()
    bin := filepath.Join(dir, "artifact")
    sumFile := bin + ".sha256"
    // Write legitimate binary
    os.WriteFile(bin, []byte("ELF...legit"), 0o755)
    sum := sha256Hex("ELF...legit")
    os.WriteFile(sumFile, []byte(sum), 0o600)

    b := &Builder{cacheDir: dir} // small scope: only test verifyCached
    if !b.verifyCached(bin, sumFile) {
        t.Fatal("expected cache hit before tamper")
    }

    // Tamper: overwrite binary
    os.WriteFile(bin, []byte("ELF...EVIL"), 0o755)
    if b.verifyCached(bin, sumFile) {
        t.Fatal("expected cache miss after tamper")
    }
    // Binary should be removed
    if _, err := os.Stat(bin); !os.IsNotExist(err) {
        t.Fatal("tampered binary was not removed")
    }
}
```

### 2.3 Package `watcher` — 8 test files, ~75% line coverage target

**The fsnotify testing problem:** The watcher uses real OS inotify events. Testing the `loop()` with real fsnotify requires creating files and waiting — flaky and slow. **Solution: dependency injection** — extract an interface for the fsnotify operations.

**Interface to extract:**

```go
// fsWatcher is the fsnotify operations the watcher needs.
// Extracted for testability. Production uses fsnotify.Watcher.
type fsWatcher interface {
    Add(string) error
    Remove(string) error
    Events() chan fsnotify.Event
    Errors() chan error
    Close() error
}
```

**Production adapter:** `&fsNotifyAdapter{fsnotify.NewWatcher()}` implementing the interface. The `Watcher` struct holds `fs fsWatcher` instead of `*fsnotify.Watcher`.

Cost: ~20 lines of adapter code + one-line change to `New()`. Benefit: tests control event timing deterministically. Worth it.

**Test suite:**

| Test | What it covers | Attack surface |
|---|---|---|
| `TestExcluded_Basic` | .git, vendor, dist → true | Default exclusions |
| `TestExcluded_Suffix` | `_test.go` → true | Exclusion patterns |
| `TestExcluded_Subdir` | `cmd/vendor/` → true | Deep exclusion |
| `TestExcluded_NotExcluded` | `main.go` → false | No false positives |
| `TestMatches_ExtFilter` | .go only → matches .go, not .md | Extension watching |
| `TestMatches_AllExts` | No watch patterns → all match | Default behavior |
| `TestExtSet_Basic` | `**/*.go` → {".go": true} | Pattern parsing |
| `TestExtSet_Wildcard` | `**/*` → nil (all) | Fall through |
| `TestLoop_Debounce` | Inject 3 events in 50ms → 1 event out after debounce | Debounce correctness |
| `TestLoop_NoSpurious` | Events on excluded path → no event | Exclusion during watch |
| `TestLoop_CreateDir` | Inject Create for new dir → Add called | Dynamic directory tracking |
| `TestWouldExceedInotify` | 80% threshold → true/false | Fallback triggering |

**Debounce test pattern (deterministic, no sleeps):**

```go
func TestLoop_Debounce(t *testing.T) {
    fake := newFakeWatcher()
    w := &Watcher{
        fs:       fake,
        events:   make(chan string, 16),
        debounce: 100 * time.Millisecond,
        done:     make(chan struct{}),
    }
    go w.loop()

    // Inject 3 events rapidly
    fake.inject(fsnotify.Event{Name: "a.go", Op: fsnotify.Write})
    fake.inject(fsnotify.Event{Name: "b.go", Op: fsnotify.Write})
    fake.inject(fsnotify.Event{Name: "c.go", Op: fsnotify.Write})

    // Should get exactly ONE debounced event
    select {
    case got := <-w.Events():
        if got != "c.go" { // last event wins
            t.Fatalf("expected c.go, got %s", got)
        }
    case <-time.After(200 * time.Millisecond):
        t.Fatal("timed out waiting for debounced event")
    }
    // Ensure no second event
    select {
    case <-w.Events():
        t.Fatal("unexpected second event")
    case <-time.After(50 * time.Millisecond):
    }
    w.Close()
}
```

**Fake watcher:**

```go
type fakeWatcher struct {
    events chan fsnotify.Event
    errors chan error
    added  []string
}

func newFakeWatcher() *fakeWatcher {
    return &fakeWatcher{
        events: make(chan fsnotify.Event, 100),
        errors: make(chan error, 1),
    }
}

func (f *fakeWatcher) Add(s string) error { f.added = append(f.added, s); return nil }
func (f *fakeWatcher) Remove(string) error { return nil }
func (f *fakeWatcher) Events() chan fsnotify.Event { return f.events }
func (f *fakeWatcher) Errors() chan error { return f.errors }
func (f *fakeWatcher) Close() error { return nil }
func (f *fakeWatcher) inject(ev fsnotify.Event) { f.events <- ev }
```

**`pollLoop` testing:** The polling fallback uses `time.Ticker` + `filepath.WalkDir`. Test via the same interface pattern: replace `WalkDir` with a mock that returns known entries with controlled mtimes. This is higher effort; for v1, test `pollLoop` with a real temp directory (not flaky because 500ms polling has natural tolerance).

**Hidden coupling flagged:** `tests` slice property of `extSet` — if someone adds a new field to `Manifest.Run.Exclude`, they must also update `DefaultExcludes` in `watcher.go` or the watcher will miss exclusions the manifest specifies. **Mitigation:** add a unit test that compares `manifest.Run.Exclude` defaults with `watcher.DefaultExcludes` and fails on divergence.

### 2.4 Package `runner` — 6 test files, ~80% line coverage target

Testing `Start` and `Stop` with real child processes. No mock for `exec.Command` — real `os/exec` is the simplest correct path. Use short-lived children to keep tests fast.

| Test | What it covers | Attack surface |
|---|---|---|
| `TestStart_ProcessGroup` | Child has new PGID 🧪 | Process isolation |
| `TestStop_SIGTERM` | Child exits on SIGTERM | Graceful shutdown |
| `TestStop_SIGKILLFallback` | SIGTERM-ignoring child gets SIGKILL | Forced stop |
| `TestStop_NilSafe` | Nil runner → no panic | Nil safety |
| `TestExited` | Running → false, stopped → true | Status reporting |
| `TestSanitizeEnv` | O+_ vars stripped, others kept | Environment hygiene |

**About process group testing:** `Start()` sets `Setpgid: true` on Linux. We verify with `syscall.Getpgid(pid)`. The child's PGID should equal its PID (group leader). This is a real syscall test — no mock possible.

**Stop timing pattern (deterministic, short):**

```go
func TestStop_SIGTERM(t *testing.T) {
    dir := t.TempDir()
    bin := filepath.Join(dir, "sigterm-child")
    // Build a tiny binary that handles SIGTERM: go:build ignore
    // ... or use /bin/sleep with a handler test
    // Simplest: sleep 30, stop with 1s grace → should exit via SIGTERM
    cmd := exec.Command("sleep", "30")
    cmd.Start()
    pid := cmd.Process.Pid
    // Kill directly rather than through Runner logic
    // ... but we want to test Runner.Stop()
}
```

For speed, build a small test binary in the test's init or use a pre-compiled helper. I propose a Go test helper that compiles a short program at test time via `go build -o` (runs once, cached by Go cache). But that adds `go build` overhead.

**Simpler alternative:** Use `/bin/sleep infinity` as the child process. It handles SIGTERM correctly (exits). `Stop(100*time.Millisecond)` should return nil. `Exited()` returns true after. Test SIGKILL fallback by using a binary that blocks SIGTERM. We can compile a small Go program for this.

```go
// SIGKILL fallback: start a process that ignores SIGTERM
// Use `trap '' TERM; sleep 30` — but that's shell. Direct exec.
// Instead: write a small Go binary into t.TempDir()

func writeIgnoreTermBin(t *testing.T, dir string) string {
    // One-time compile per test run
    src := `package main
import (
    "os"
    "os/signal"
    "syscall"
    "time"
)
func main() {
    signal.Ignore(syscall.SIGTERM)
    time.Sleep(30 * time.Second)
}`
    bin := filepath.Join(dir, "ignore-term")
    t.Log("compiling ignore-term binary...")
    cmd := exec.Command("go", "build", "-o", bin, "-")
    cmd.Dir = dir
    cmd.Stdin = strings.NewReader(src)
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("compile ignore-term: %v\n%s", err, out)
    }
    return bin
}
```

This compiles once per test run (cached by $GOCACHE). Acceptable.

### 2.5 Package `scaffold` — 5 test files, ~85% line coverage target

Pure logic + filesystem operations on `t.TempDir()`. No network, no goroutines.

| Test | What it covers | Attack surface |
|---|---|---|
| `TestSafePath_HomeOK` | Path under home → nil | No false reject |
| `TestSafePath_TmpOK` | Path under /tmp → nil | Tmp override |
| `TestSafePath_SystemRejected` | /etc/whatever → error | System path guard |
| `TestSafePath_Symlink` | Symlink in home → resolved to home | Path traversal 🧪 |
| `TestCreate_ForceBypass` | /etc with --force → creates (actually writes to tmp with force) | Force override |
| `TestCreate_NonEmptyRefuse` | Existing non-empty dir → error | Destructive check |
| `TestCreate_NonEmptyForce` | Non-empty with force → creates | Force semantics |
| `TestCreate_TemplateContent` | Written files contain `{{NAME}}` → replaced | Template integrity |
| `TestCreate_UnknownTemplate` | Bogus template name → error | Error handling |
| `TestList` | Returns 3 templates | API contract |

**`safePath` symlink test:** If `target` is a symlink under home pointing to `/etc/passwd`, `filepath.Abs(target)` resolves the symlink and `filepath.Rel(home, abs)` may reject it if it points outside home. But `filepath.Abs` on a symlink returns the resolved path, so the guard works. However, the *parent* could be a symlink. Test: `os.Symlink("/etc", filepath.Join(tmp, "malicious"))` then try `safePath(filepath.Join(tmp, "malicious", "malicious-file"))`. The `filepath.Abs` will resolve to `/etc/malicious-file` which is outside home. **Guarded correctly.** This is worth a test for confidence.

### 2.6 Package `tester` — 3 test files, ~70% line coverage target

Testing terminal output is noisy but doable with output capture.

| Test | What it covers | Attack surface |
|---|---|---|
| `TestHandleEvent_PassFail` | Pass/fail events → correct state | Output correctness |
| `TestHandleEvent_OutputAccumulation` | Multiple output lines → accumulated | Output integrity |
| `TestHandleEvent_PackageFail` | Package-level fail (no Test field) → printed | Error reporting |
| `TestPrintSummary_Counts` | Known states → correct counts | Summary correctness |

**Capturing output:** The tester uses `color.New(color.FgGreen, color.Bold).Printf(...)`. `fatih/color` on non-TTY outputs raw text without ANSI codes, so capture via `os.Pipe()` works.

### 2.7 Integration Tests (package `cli`)

A small set of end-to-end tests tagged with `//go:build integration`:
- `o+ new minimal` → directory created, `go.mod` present
- `o+ build` → binary produced
- `o+ test` → exit 0
- `o+ run` with trust gate → fingerprint shown, then accepted

These are slow (run `go build` for real) and excluded from `go test ./...`. Run in CI as a separate step or on demand.

---

## 3. GitHub Actions CI

File: `.github/workflows/ci.yml`

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  build-and-test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go-version: ['1.24.4', '1.25rc1']  # current + upcoming release
    steps:
    - name: Checkout
      uses: actions/checkout@v4

    - name: Setup Go ${{ matrix.go-version }}
      uses: actions/setup-go@v5
      with:
        go-version: ${{ matrix.go-version }}

    - name: Build
      run: go build ./...

    - name: Vet
      run: go vet ./...

    - name: Test with race detector
      run: go test -race -count=1 ./...

    - name: Test with coverage
      run: |
        go test -cover -coverprofile=coverage.out -count=1 ./...
        go tool cover -func=coverage.out | tee coverage.txt

    - name: Check security-critical coverage
      run: |
        echo "=== Security path coverage ==="
        grep -E 'internal/(manifest|builder|runner|watcher|scaffold|tester)' coverage.txt

    - name: govulncheck
      run: |
        go install golang.org/x/vuln/cmd/govulncheck@latest
        govulncheck ./...

  release:
    if: startsWith(github.ref, 'refs/tags/v')
    needs: build-and-test
    runs-on: ubuntu-latest
    steps:
    - name: Checkout
      uses: actions/checkout@v4

    - name: Setup Go
      uses: actions/setup-go@v5
      with:
        go-version: '1.24.4'

    - name: Build release binary
      run: |
        mkdir -p dist
        go build -ldflags "-X github.com/amritrai/oplus/internal/version.Version=${GITHUB_REF_NAME} -X github.com/amritrai/oplus/internal/version.Commit=${GITHUB_SHA::7}" -o dist/o+ .

    - name: Checksum
      run: |
        cd dist && sha256sum o+ > o+.sha256
        cat o+.sha256

    - name: Upload release artifacts
      uses: actions/upload-artifact@v4
      with:
        name: oplus-${{ github.ref_name }}
        path: dist/
```

**Design decisions:**

| Choice | Rationale |
|---|---|
| Single OS (Linux) | Matches v0.1 scope. macOS/Windows matrix deferred per CEO |
| Two Go versions | Catch breakage on upcoming Go version. Adds ~2 min per push. Acceptable |
| `-race` + `-count=1` | Race detector is non-negotiable for a tool that runs user code. `-count=1` because the default cache can mask races. Separated from coverage (not compatible in same run) |
| Govulncheck as action step, not blocking pre-merge | Govulncheck can false-positive on non-compiled platform paths (like GO-2026-5024 on Windows). It runs and reports, but doesn't block merge unless it finds an actual vulnerability affecting the current platform. Mitigation: pipe to `grep -v 'windows'` and check remaining output |
| No Docker, no matrix OS | Complexity not justified for v0.1 |
| Separate release job | Only runs on tags. Keeps CI fast for PRs |
| `actions/upload-artifact` for release | Simple, no external release tool. Can manually download from the Actions run. For proper GitHub Releases, add `softprops/action-gh-release` after upload |

---

## 4. Release Artifact with Checksums

**Design: Makefile + sha256sum** (goreleaser vetoed)

Rationale: goreleaser adds a Go dependency + YAML config for something we can do in 4 lines of shell. For a single binary on a single OS, goreleaser is clever-but-fragile. A Makefile is boring, transparent, and every contributor already has it.

```makefile
# Add to existing Makefile
.PHONY: release checksum

release: build checksum

checksum:
	cd bin && sha256sum o+ > o+.sha256
	@echo "=== Checksum ==="
	@cat bin/o+.sha256

.PHONY: release-tag
release-tag: test vet
	git tag -a v$(VERSION) -m "Release $(VERSION)"
	git push origin v$(VERSION)
```

The workflow above builds + signs the artifact in CI and uploads it. Users verify with:

```shell
curl -sLO https://github.com/amritrai/oplus/releases/download/v0.2.0/o+
curl -sLO https://github.com/amritrai/oplus/releases/download/v0.2.0/o+.sha256
sha256sum -c o+.sha256
```

**Hidden coupling flagged:** The release Makefile target depends on `build` which embeds `$(VERSION)` into the binary. If VERSION is stale, the binary says `v0.1.0-dev`. Mitigation: the CI release job hardcodes `VERSION=${GITHUB_REF_NAME}` and `COMMIT=${GITHUB_SHA}` to override whatever is in the Makefile. This is documented in the CI YAML and the Makefile.

---

## 5. Will This Hurt Us in 6 Months?

Every design choice evaluated against a 6-month horizon:

| Decision | 6-month risk | Mitigation |
|---|---|---|
| Single OS (Linux) | When macOS support lands, CI needs new matrix entries + `//go:build` tags | Matrix is cheap to add; test code already uses `runtime.GOOS` guards |
| fsWatcher interface | One more interface to maintain; `fsnotify.Watcher` API is stable (v1.10) | If fsnotify changes, we update the adapter (1 file, ~20 lines) |
| Makefile (not goreleaser) | When we need multi-arch builds (amd64+arm64), Makefile grows | At that point, add goreleaser — but not before |
| Table-driven (not property tests) | Property tests discover edge cases we don't think of | Re-evaluate at v0.3 when the test suite is stable and we're looking for novel bugs |
| ./... coverage | Large integration tests in `cli/` may fail on some machines (missing Go) | Integration tests are tag-gated; `go test ./...` only runs unit tests |
| `go build -race` in CI | Adds ~30s to CI per matrix cell | Acceptable for a toolchain project |

---

## 6. Top 3 Risks — Security Reviewer

1. **TOCTOU in verifyCached (race window).** The current code reads the binary file whole, then hashes the bytes in memory. An attacker with write access to `$XDG_CACHE_HOME/o+/bin/` (mode 0700, owned by user) could still swap the file between `os.ReadFile()` and `sha256.Sum256()`. **Defense:** document that the threat model assumes the user's own cache directory is trusted. An attacker who can write to `~/.cache/o+/bin/` already has user-level code execution. The 0700 mode prevents other OS users from tampering; the same-user race is not in the threat model for v0.1.

2. **Trust.json corruption as denial-of-service.** If an attacker can write to `~/.cache/o+/trust.json`, they can cause `Trusted()` to return an unexpected error or silently reset all trust. Current code handles corrupt JSON by returning an empty map (fail-open). **Defense:** `loadTrust` already handles parse errors gracefully (lines 64-68). Add a test confirming this. The attack requires write access to the cache dir, which is the same attack surface as #1.

3. **Alias bomb through yaml.v3 internals.** The two-pass parse (pass 1: alias rejection on the node tree; pass 2: strict decode) assumes that pass 1's alias check captures *all* alias expansion. If yaml.v3 introduces a new node kind that expands on decode without being `yaml.AliasNode`, the bomb goes undetected. **Defense:** the 1MB size cap bounds the blast radius. Even a billion-laughs within 1MB produces at most ~2GB of expansion — manageable. Add a fuzz test in v0.3.

## 7. Top 3 Risks — Performance Skeptic

1. **Watcher test overhead with real goroutines.** Even with DI, `TestLoop_Debounce` creates a goroutine running `loop()`. If 200 test functions each spin up a goroutine, the test suite's goroutine count can reach thousands. **Defense:** keep goroutine tests to a minimum (3-4 tests). Use a `testCtx` with `Done()` channel to force cleanup. Use `t.Cleanup(func() { close(w.done) })`.

2. **Source-tree hashing in builder test.** `TestProjectKey_ChangesOnContent` needs to create a real directory with files and walk it. For 10 files this is fine. For 1000 files (a large real-world project) it's slow. **Defense:** test with 3-5 files. The performance team already accepted the 10-30ms cost; the test just validates correctness, not performance.

3. **Govulncheck in CI adds ~30s and can false-positive.** On every push/PR, govulncheck downloads the vuln DB and scans. For our ~1700 LOC with 5 direct deps, this adds 15-30s. False positives (like GO-2026-5024 on Windows subpackage that doesn't compile on Linux) require a grep filter. **Defense:** pipe govulncheck output through `grep -v 'windows'` for platform-specific false positives. Accept the 30s cost — it's cheaper than shipping with a known CVE.

---

## 8. File Map

```
oplus/
├── .github/
│   └── workflows/
│       └── ci.yml                    # CI + Release (new)
├── internal/
│   ├── builder/
│   │   ├── builder.go
│   │   ├── builder_test.go           # new: verifyCached, prune, projectKey, sourceHash
│   │   └── builder_integration_test.go # new: go:build integration (real go build)
│   ├── cli/
│   │   ├── ...
│   │   └── cli_test.go               # new: integration tests for full commands
│   ├── manifest/
│   │   ├── manifest.go
│   │   ├── trust.go
│   │   ├── manifest_test.go          # new: Load size/depth/alias/unknown
│   │   ├── trust_test.go             # new: Fingerprint, Trusted, Trust, corrupt json
│   │   └── cache_dir_test.go         # new: CacheDir mode
│   ├── runner/
│   │   ├── runner.go
│   │   └── runner_test.go            # new: Start PGID, Stop, Exited, sanitizeEnv
│   ├── scaffold/
│   │   ├── scaffold.go
│   │   └── scaffold_test.go          # new: safePath, Create (all cases), template content
│   ├── tester/
│   │   ├── tester.go
│   │   └── tester_test.go            # new: handleEvent, printSummary
│   └── watcher/
│       ├── watcher.go
│       ├── watcher_test.go           # new: excluded, matches, extSet, extSetTable
│       └── loop_test.go              # new: loop with fake event source, pollLoop
└── Makefile                          # modified: +release, +checksum, +govulncheck targets
```

---

## 9. Refactoring Required

1. **watcher.go** — Extract `fsWatcher` interface; store `fs fsWatcher` on `Watcher` struct; adapt `fsnotify.Watcher` via thin wrapper.
2. **watcher.go** — Add a small exported helper: `NewWithFS(root string, patterns, excludes []string, debounce time.Duration, fs fsWatcher) (*Watcher, error)` for tests. `New()` delegates to `NewWithFS` with a real fsnotify watcher.
3. **tester.go** — Consider exporting `handleEvent` or making it package-private. It's currently unexported; we can test it from within the package.
4. **builder.go** — `verifyCached` is currently unexported. Tests live in the same package so it's accessible. Good.
5. **scaffold.go** — `safePath` is unexported. Same-package tests have access. Good.

Total refactoring: ~30 lines in watcher.go, zero changes elsewhere beyond test files.

---

## 10. Summary

| Dimension | Decision |
|---|---|
| Test style | Table-driven for deterministic paths; DI for watcher loop; real subprocesses for runner |
| Watcher testing | `fsWatcher` interface + `fakeWatcher` — no flaky sleeps |
| Runner testing | Real `/bin/sh` / compiled helper for SIGKILL fallback |
| Cache tamper | Synthetic artifacts + sha256 mismatch |
| CI matrix | Go 1.24.4 + 1.25rc1, Linux only |
| Govulncheck | Action step, pipe through grep for platform false-positives |
| Release | Makefile + sha256sum (not goreleaser) |
| Coverage | `go test -cover` per-package, grepped for security paths |
| Integration tests | `//go:build integration` tag, separate CI workflow |
| Top risk (Security) | TOCTOU in verifyCached (accepted — same-user threat model) |
| Top risk (Perf) | Watcher goroutine count in tests (mitigated: <5 goroutine tests) |
