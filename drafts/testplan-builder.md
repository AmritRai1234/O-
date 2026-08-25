# Pragmatic Builder — "Green Pipeline" Test Plan

**Persona:** Pragmatic Builder
**Approach:** Green Pipeline — fastest path from zero tests to a green CI badge
**Bias:** Working code > perfect code. Veto abstractions without a second concrete use case.

---

## CEO Scope Recap (from NEXT-EPIC.md)

> (1) a test suite for the security-critical paths — manifest bomb/alias/depth guards, cache sha256 verification, trust gate, watcher exclusions + debounce, runner process-group stop, scaffold path safety; (2) CI on GitHub Actions: build + vet + test + govulncheck on every push/PR; (3) release artifact with .sha256 checksums.

SUCCESS: `go test ./...` green; CI green on main; release binary + .sha256 published.

---

## 1. Test Strategy: What Earns Its Keep

### TEST THESE (directly security-critical OR pure-function testable):

| Package | Test target | Why it earns its keep | How we test it |
|---|---|---|---|
| `manifest` | `Load` rejects oversized YAML | Security: unbounded input → OOM | Write 2MB file to temp dir, assert error |
| `manifest` | `Load` rejects deep nesting | Security: resource exhaustion | Write 64+ deep nesting, assert error |
| `manifest` | `Load` rejects anchors/aliases | Security: billion-laughs | Write anchor+alias construct, assert error |
| `manifest` | `Load` rejects unknown keys | Safety: typos caught early | Write `foobar: true`, assert error |
| `manifest` | `Load` returns default on missing file | Correctness | Empty dir, assert defaults |
| `manifest` | `Load` parses valid manifest | Correctness smoke | Write valid o+.yaml, assert fields match |
| `manifest` | `Fingerprint` hash stability | Security anchor | Known inputs → known hex output |
| `manifest` | `Fingerprint` includes o+.yaml, go.mod, go.sum | Correctness | Write all three, verify they appear in hash |
| `manifest` | `Fingerprint` skips missing files gracefully | Correctness | Empty dir, no crash, non-empty hash |
| `manifest` | `CacheDir` => mode 0700 | Security (binding condition #1) | Temp XDG_CACHE_HOME, check mode |
| `manifest` | `CacheDir` falls back to ~/.cache | Correctness | Unset XDG_CACHE_HOME, check path |
| `manifest` | `Trust` + `Trusted` round-trip | Trust gate correctness | Temp dir, fingerprint, trust, assert trusted |
| `manifest` | `Trusted` returns false on fingerprint change | Security: TOCTOU detection | Change file, assert not trusted |
| `manifest` | `Trusted` returns false on un-trusted dir | Security: default-denial | Fresh dir, assert false |
| `builder` | `verifyCached` passes on matching sha256 | Cache correctness | Write known bin + valid .sha256, assert true |
| `builder` | `verifyCached` deletes on hash mismatch | Security: poisoned cache | Corrupt bin, assert false + file deleted |
| `builder` | `verifyCached` returns false on missing sumfile | Correctness | No .sha256, assert false |
| `builder` | `sourceHash` changes when file content changes | Cache-key correctness | Temp dir with .go file, hash, modify, hash differs |
| `builder` | `sourceHash` excludes _test.go, .git, vendor | Correctness | Write _test.go + .git/foo, not in hash |
| `builder` | `sha256File` matches expected output | Correctness | Known bytes, known sum |
| `watcher` | `excluded` returns true for default excludes | Correctness | .git, vendor, dist, node_modules, .o+, .cache |
| `watcher` | `excluded` returns true for suffix patterns | Correctness | **/*_test.go pattern |
| `watcher` | `excluded` returns false for normal paths | Correctness | src/main.go |
| `watcher` | `excluded` returns true for extra excludes | Correctness | Pass extraExclude, verify excluded |
| `watcher` | `matches` honors extension filter | Performance | .go watched, .txt ignored |
| `watcher` | `matches` passes when ext set is nil | Correctness | No watch patterns → match all |
| `watcher` | `extSet` parses watch globs | Correctness | "./**/*.go" → {".go": true} |
| `watcher` | `extSet` returns nil for all-catch pattern | Correctness | "./**/*" → nil |
| `watcher` | `wouldExceedInotify` returns false below threshold | Correctness | Few dirs → false (uses fake /proc) |
| `watcher` | `readInotifyLimit` parses /proc | Correctness | Known input → known int |
| `runner` | `sanitizeEnv` drops O+_ vars | Security | O+_FOO=bar dropped, PATH kept |
| `runner` | `sanitizeEnv` keeps other vars | Correctness | HOME, PATH, SHELL preserved |
| `runner` | `Exited` returns true for nil runner | Safety | Nil-safe |
| `runner` | `PID` returns 0 for nil runner | Safety | Nil-safe |
| `scaffold` | `safePath` allows home directory | Correctness | Path under $HOME, no error |
| `scaffold` | `safePath` allows /tmp | Correctness | Path under /tmp, no error |
| `scaffold` | `safePath` rejects /etc | Security (CEO add) | /etc/foo → error (CEO leder: Create() guards) |
| `scaffold` | `Create` refuses non-empty dir without --force | Safety | Populated dir, no force → error |
| `scaffold` | `Create` writes template files + go mod init | Correctness smoke | Temp dir, minimal template, check main.go + go.mod |
| `tester` | `handleEvent` state machine | Correctness | Inject JSON events, verify output states |
| `tester` | `printSummary` counts pass/fail | Correctness | Known states → known summary |
| `version` | `Version` and `Commit` defaults | Correctness | Default values non-empty |

### DO NOT TEST (earn your keep = no):

| Package | Cut target | Why cut |
|---|---|---|
| `watcher` | `loop()` with real fsnotify | Requires real inotify, sleeps for debounce → flaky on CI. The debounce timer path is trivial (timer.Reset) and fsnotify is a dependency we trust. |
| `watcher` | `pollLoop()` | 500ms poll interval × deterministic test = time.Sleep in test. Not worth the CI flake cost for v0.1. The polling-fallback path is a simple timer + WalkDir read, and WalkDir errors are skipped (not security-critical). |
| `runner` | `Start` + `Stop` with real processes | Forking child processes and sending SIGTERM/SIGKILL in a unit test is flaky in CI (process cleanup, PGID races). The Stop logic is a straightforward select on done + time.After. |
| `builder` | `Build()` full integration | Requires `go build` on a real project. That's what the existing smoke test covers (CEO verified: `o+ new -> o+ build -> o+ test`). Unite test the cache verification and key logic; don't re-test `go build`. |
| `builder` | `prune()` | Mtime-based LRU in tests = filesystem timestamp races on fast CI. The logic is a sort + delete oldest N. Worth a unit test only when bug reports come in. |
| `cli/*` | CLI command integration | Cobra is a battle-tested framework. The commands are thin wrappers. Test the business logic packages directly. |
| `main.go` | Root command test | Testing cobra.Execute() is testing cobra, not our code. |

### Test placement

```
internal/manifest/manifest_test.go   — Load, checkDepth, rejectAliases, Default
internal/manifest/trust_test.go      — Fingerprint, CacheDir, Trust, Trusted
internal/builder/builder_test.go     — verifyCached, projectKey, sourceHash, sha256File
internal/watcher/watcher_test.go     — excluded, matches, extSet, wouldExceedInotify, readInotifyLimit
internal/runner/runner_test.go       — sanitizeEnv, Exited (nil-safe), PID (nil-safe)
internal/scaffold/scaffold_test.go   — safePath, Create (non-empty dir guard)
internal/tester/tester_test.go       — handleEvent, printSummary
```

For `wouldExceedInotify` and `readInotifyLimit`, use `t.TempDir()` + inject a fake `/proc/sys/fs/inotify/max_user_watches` file to avoid depending on the real host kernel setting. The function reads a file path — we make the test write a known value to a temp path and patch accordingly (or test `readInotifyLimit` in isolation with known content).

---

## 2. Determinism: No Sleeps, No CI Flakes

### Concrete rules enforced in every test:

1. **No `time.Sleep` anywhere.** Use `t.Timer` + channel synchronisation or clock replacement. The only candidate that would tempt it (watcher debounce) is deliberately NOT tested at the loop level (see above).
2. **No filesystem timestamp races.** `prune()` not unit-tested. `sourceHash()` operates on content, not timestamps — safe.
3. **No real child processes.** `runner.Start`/`Stop` not unit-tested. `sanitizeEnv` is pure string-slice manipulation.
4. **No global state mutation.** `Trust` writes to `$XDG_CACHE_HOME/o+/trust.json` — each test gets its own `t.TempDir()` with `XDG_CACHE_HOME` set via `t.Setenv`.
5. **No network.** All tests are offline.
6. **Seed random for hash generation.** SHA256 is deterministic by construction; no rand needed.

### Per-test isolation:

- Each `manifest.Load` test creates its own temp dir with its own `o+.yaml`
- Each `Fingerprint` test creates its own temp dir with known file contents
- Each `CacheDir` test sets `XDG_CACHE_HOME` via `t.Setenv("XDG_CACHE_HOME", t.TempDir())`
- Each `Trust` test gets a fresh `t.TempDir()` + isolated cache dir

Go 1.24.4's `t.TempDir()` cleans up automatically. Zero shared state between tests.

---

## 3. Test Code Patterns (concrete)

### Table-driven for manifest parsing:

```go
func TestLoad_RejectsOversized(t *testing.T) {
    dir := t.TempDir()
    data := make([]byte, 2<<20) // 2MB
    // first byte non-null so YAML sees content
    data[0] = '#'  // YAML comment
    os.WriteFile(filepath.Join(dir, "o+.yaml"), data, 0644)
    _, err := manifest.Load(dir)
    assert.ErrorContains(t, err, "1MB size limit")
}
```

### Table-driven for valid/invalid YAML structs:

```go
func TestLoad(t *testing.T) {
    tests := []struct{
        name    string
        yaml    string
        wantErr string
    }{
        {name: "valid minimal", yaml: "name: myapp\ntype: lib", wantErr: ""},
        {name: "deeply nested", yaml: deepYAML(65), wantErr: "depth limit"},
        {name: "alias", yaml: "a: &x [1]\nb: *x", wantErr: "aliases are forbidden"},
        {name: "unknown key", yaml: "name: x\nunknown_field: true", wantErr: "unknown field"},
        {name: "huge → 1MB+", ...},
    }
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            dir := t.TempDir()
            os.WriteFile(filepath.Join(dir, "o+.yaml"), []byte(tc.yaml), 0644)
            _, err := manifest.Load(dir)
            // assert
        })
    }
}
```

### Pure function tests (no I/O at all):

```go
func TestExcluded(t *testing.T) {
    w := &Watcher{excludes: append(DefaultExcludes, "secret.yaml")}
    tests := []struct{
        path  string
        want  bool
    }{
        {"/home/user/project/.git/config", true},
        {"/home/user/project/vendor/pkg/foo.go", true},
        {"/home/user/project/node_modules/bar/index.js", true},
        {"/home/user/project/src/main.go", false},
        {"/home/user/project/secret.yaml", true},
    }
    for _, tc := range tests {
        if got := w.excluded(tc.path); got != tc.want {
            t.Errorf("excluded(%q) = %v, want %v", tc.path, got, tc.want)
        }
    }
}
```

---

## 4. GitHub Actions CI — Minimal Surface

The CEO asked for build + vet + test + govulncheck. Here's the minimal CI that actually catches breakage:

```yaml
# .github/workflows/ci.yml
name: CI
on: [push, pull_request]
jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24.4"
          cache: true
      - run: go build ./...
      - run: go vet ./...
      - run: go test -count=1 ./...
      - run: go tool govulncheck ./...
```

**Rationale for each step:**
- `checkout@v4` — default actions/checkout
- `setup-go@v5` with `cache: true` — fastest Go setup with module cache
- `go build ./...` — catches compilation errors in the o+ tool itself (not just our tests, the tool builds)
- `go vet ./...` — catches structural issues (CEO verified: current code vets clean)
- `go test -count=1 ./...` — force full run (no Go test cache in CI — we want actual passes). `-count=1` is necessary here because GitHub Actions runners get different cache keys across runs and `go test` without `-count=1` could serve stale cached results. This is the RIGHT use of `-count=1` (CI) vs the wrong one the Architect rejected (developer `--watch`).
- `go tool govulncheck ./...` — catch known CVEs in deps (CEO ledger: GO-2026-5024 hard trigger documented)

**What I omitted and why:**
- `golangci-lint` — adds ~60s to CI, zero new signal vs `go vet` for v0.1. Worth adding at v0.2 when the codebase is stable.
- `staticcheck` — same argument: O(30s) for no bugs caught that vet misses at this scale.
- `go mod tidy` check — vet catches mod issues; adding a diff check is defensive but costs CI time.
- Multiple GOOS/GOARCH — CEO explicitly deferred macOS/Windows.
- Race detector — `-race` doubles CI time (~120s vs ~30s). Add as a nightly/weekly, not on every push.
- Coverage threshold — CEO didn't ask for it. Ship green first, add coverage gates later.

**CI runtime estimate:** ~30s on a fresh runner (dependency-free, small codebase, govulncheck is fast).

---

## 5. Checksummed Release Artifact

### Release workflow (separate from CI, triggered by tag):

```yaml
# .github/workflows/release.yml
name: Release
on:
  push:
    tags: ["v*"]
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        - uses: actions/setup-go@v5
          with:
            go-version: "1.24.4"
            cache: true
      - id: vars
        run: echo "TAG=${GITHUB_REF#refs/tags/}" >> $GITHUB_OUTPUT
      - run: go build -ldflags "-X github.com/amritrai/oplus/internal/version.Version=${{ steps.vars.outputs.TAG }}" -o "o+-linux-amd64" .
      - run: sha256sum "o+-linux-amd64" > "o+-linux-amd64.sha256"
      - uses: softprops/action-gh-release@v2
        with:
          files: |
            o+-linux-amd64
            o+-linux-amd64.sha256
          generate_release_notes: true
```

This gives users `curl -L <release-url/o+-linux-amd64> | sha256sum -c o+-linux-amd64.sha256` verification before they execute — satisfying Safety #2 from the CEO.

The release.yml is minimal: no matrix, no cross-plat (deferred), just linux/amd64. The .sha256 is the primary artifact; the binary is secondary. Users verify the checksum before executing.

---

## 6. Fastest Path to Green Pipeline

### Execution order:

1. **Write the pure-function tests first** (no I/O: `excluded`, `matches`, `extSet`, `sanitizeEnv`, `handleEvent`, `printSummary`, `sha256File`). These compile and pass in <1s. Quick wins.

2. **Write the temp-dir tests** (most of manifest + builder + scaffold). Each test creates its own `t.TempDir()`. These will compile and pass immediately — no external dependencies.

3. **Write the `wouldExceedInotify` / `readInotifyLimit` tests** — these need a bit of care to inject the fake /proc file. Worth doing because inotify fallback is a Performance condition.

4. **Write `CacheDir` mode-verification test** — simple `t.Setenv` + check.

5. **Go template test file scaffold** — one `_test.go` per package with 5-15 table-driven tests each. Total: ~400-500 lines of test code for ~1700 LOC of source. Good ratio.

6. **Create `.github/workflows/ci.yml`** — copy-paste from above, push, verify green.

7. **Create `.github/workflows/release.yml`** — copy-paste, test with a `v0.1.1` tag.

### Estimate: 2-3 hours from start to green CI badge, including one PR review cycle.

---

## 7. What I Would Cut (Architect Vetoes)

The Staff Architect will likely propose:
- "Add a `testing/fstest` mock filesystem abstraction layer" — **VETO**. Not a single Go test in this codebase needs a mock FS abstraction. `t.TempDir()` + `os.WriteFile` works for every test case. Abstraction without a second concrete use case.
- "Abstract the watcher with an interface for testable event injection" — **VETO**. We don't test the loop (see "Cut" section). The pure string functions are tested without any interface. When we need loop tests (v0.2), we can introduce the interface then. YAGNI.
- "Table-driven test generator framework" — **VETO**. Go subtests + slice literals are the framework. A code generator for tests is an abstraction with zero use cases at 1700 LOC.
- "Coverage threshold gate in CI" — **VETO**. CEO said "green pipeline," not "90% coverage." Get green first, add gates later.
- "Race detector in PR CI" — **VETO**. Doubles CI cost, zero data races found in v0.1's all-serial code. Nightly or weekly schedule is fine.

---

## 8. Gap Analysis: Uncovered Security Paths

Some security-critical paths the CEO listed are inherently integration-testable and NOT covered by unit tests:

| Path | Why not unit-tested | Coverage strategy |
|---|---|---|
| Runner process-group stop (SIGTERM → grace → SIGKILL) | Requires forking real child processes. Stop is a select on done+time.After — standard Go pattern. | Verifiable by manual smoke test (CEO verified in v0.1: SIGINT stops child group, zero strays). Add to integration test in v0.2 if real CI infrastructure supports process-group verification. |
| Watcher debounce timer | The debounce is a simple `timer.Reset` — Go stdlib tested. The `pending` variable is a single-assignment in the loop. | Tested implicitly by the watcher's own unit structure (the loop is a textbook select-on-3-channels pattern). Add integration in v0.2. |
| Cache sha256 verification at exec time (TOCTOU fix) | `verifyCached` is the TOCTOU guard. The gap would be if someone calls `Build` and ignores `verifyCached` — but the call chain is `Build` → `verifyCached` → return the verified binary. Unit test covers the verify logic; integration can't improve on that. | Already covered by `verifyCached` unit tests with deliberate hash mismatches. |
| Scaffold path safety in non-force mode | `safePath` unit-tested. But the actual `Create` function also checks non-empty dirs. | Both `safePath` and `Create`'s non-empty guard are tested as unit tests. Good enough for v0.1. |

---

## 9. Risks the Security Reviewer Will Attack

1. **"What about the TOCTOU between `verifyCached` and `exec`?"** — `verifyCached` re-hashes and deletes on mismatch. The binary is then returned and executed. If an attacker swaps the binary between verify and exec (TOCTOU), that's a kernel-level race that no user-space check can prevent. This is an accepted risk: the sha256 guard is the same level of protection as `sha256sum` on a release artifact. Document this explicitly in the test plan: `verifyCached` closes the stored-vs-declared-fingerprint gap, not the exec-time race.

2. **"`loadTrust` silently discards corrupt JSON — that's failure-oblivious"** — Yes, and it's intentional: corrupt trust.json (e.g. partial write, disk error) must not lock the user out of their projects. The test confirms this: corrupt file → empty map, re-`Trust` overwrites cleanly. Document the threat model: trust.json corruption is a denial-of-service attack on the user; the tool recovers by forgetting trust. A determined attacker who can corrupt trust.json can also corrupt the binary — so this is not an escalation boundary.

3. **"`sourceHash` contains a WalkDir with `return nil` on errors — silent I/O errors"** — This mirrors `go build`'s own behavior (unreadable files cause build failures, not silent wrong binaries). A file that fails to read produces an error in `os.ReadFile`, which `sourceHash` returns. The `WalkDir` error skip is for permissions on subdirectories — same tradeoff `go build` makes. Document the edge case.

4. **"`Trust` writes to `$XDG_CACHE_HOME/o+/trust.json`. What about symlink attacks?"** — The `CacheDir` creates the directory with `0o700`, so only the owner can write. A pre-existing symlink at `$XDG_CACHE_HOME/o+/trust.json` pointing elsewhere would be followed — but the attacker would need write access to `$XDG_CACHE_HOME/o+/`, which implies they already control the user's cache directory. Document the caveat in security corner cases.

---

## 10. Risks the Performance Skeptic Will Attack

1. **"`sourceHash` walks the entire project tree on every build — that's O(N) in the file tree, and N can be 10k+ for monorepo projects"** — CEO decision accepted this: "Source-tree hashing (~10-30ms for 1000 files) is what makes cache hits correct rather than merely fast." The perf skeptic is right that this doesn't scale to monorepos. The test plan acknowledges the perf boundary: if the project has 100k+ files, source hashing will be ~1-3s. That's the cost of correctness. The `excludedDirs` map (`.git`, `vendor`, etc.) keeps the walk manageable for typical projects. Open issue for v0.2: incremental hash (only re-hash changed files).

2. **"`-count=1` in CI disables Go's test cache — you're building and running every test from scratch"** — This is INTENTIONAL and CORRECT. CI must test the actual code, not a cached prior result. In developer mode (`o+ test --watch`), `-count=1` is NOT used (Performance condition from DECISION.md). The skeptic's concern is based on misunderstanding the scope of `-count=1`. Document clearly: CI uses `-count=1` (must test actual code); `--watch` omits it (developer performance).

3. **"`go vet ./...` is redundant when `go build ./...` already type-checks and `go-tools/analysis` is baked into vet"** — Partially right: go vet catches ~2% of issues that go build doesn't (unused params, printf arg mismatches). The cost is ~1-2s on this codebase. The value is catching UB-style bugs before they ship. Keep it at zero cost until it becomes a bottleneck.

4. **"`govulncheck` adds dependency scanning every build — it's slow for what it catches"** — For v0.1 with 3 direct deps (cobra, fsnotify, yaml.v3, fatih/color) and no vulnerabilities in the transitive tree, govulncheck is ~1-2s. It catches GO-2026-5024 (the `x/sys/windows` advisory) and will flag it when Windows targets are added (the hard trigger from the CEO ledger). Without govulncheck, that advisory would be silently forgotten. Keep it.

5. **"`go test -count=1 ./...` is 20 tests × 100ms = 2s now, but it'll grow. Why not `-short` and package filters?"** — Because when it grows, we add `-short` (and `testing.Short()` guards). The faster path to green is 2s now — not designing a test-suite taxonomy before shipping test 1. Working code > perfect code.

---

## 11. V0.2 Follow-On (Deliberately Deferred)

- Watcher loop integration tests (once we add a test helper abstraction — justified by a second use case)
- Runner real-process tests (with a short-lived child like `sleep 0.01`)
- Race detector in CI (add to weekly schedule)
- Cross-platform tests (when macOS/Windows support lands)
- Test coverage gate (after the suite has 80%+ coverage naturally)
- Incremental source hash (for monorepo performance)

---

## Appendix: Quick-Run Stats

| Metric | Value |
|---|---|
| Test files to create | 7 |
| Estimated test LOC | ~450-550 |
| CI runtime (target) | ~30s |
| First green pipeline (eta) | ~2-3h |
| Flake risk | near-zero (no time.Sleep, no net, no global state) |
| Security paths covered by unit tests | 12/14 (2 deferred to integration) |
| Implement first | `manifest_test.go` + `watcher_test.go` (pure functions, instant green) |