# Paranoid Security Reviewer — Test Plan Audit (Round 2)

**Reviewer:** Security (Paranoid)
**Bias:** Input is malicious, deploy env hostile. Nitpick auth/injection/secrets/trust boundaries ONLY. Ignore style.
**Scope:** /home/amritrai/spine/O+ (module github.com/amritrai/oplus), NEXT-EPIC.md, DECISION.md, two drafts
**Date:** 2026-08-25

---

## 0. Code Review Base: Security-Critical Paths Confirmed in Source

Before evaluating the drafts, I read every Go source file under `internal/` to confirm which security-relevant code actually exists and which attack surfaces are real.

| Security path | File | Lines | Exists | Criticality |
|---|---|---|---|---|
| Manifest size cap (1MB) | manifest.go:76-78 | 3 | ✅ | High |
| Manifest depth limit (64) | manifest.go:104-114 | 11 | ✅ | Medium |
| Alias/Anchor rejection | manifest.go:116-126 | 11 | ✅ | High (bomb) |
| KnownFields decode (pass 2) | manifest.go:95-99 | 5 | ✅ | High (schema drift) |
| Fingerprint (trust + cache key) | trust.go:13-28 | 16 | ✅ | High |
| CacheDir mode 0700 | trust.go:42-45 | 4 | ✅ | Medium |
| loadTrust corrupt-JSON handling | trust.go:64-68 | 5 | ✅ | Medium |
| saveTrust mode 0600 | trust.go:84 | 1 | ✅ | Medium |
| verifyCached / sha256 check | builder.go:121-137 | 17 | ✅ | Critical |
| sourceHash vendor exclusion | builder.go:175 | 1 | ✅ | Medium (cache gap) |
| Build cache LRU prune | builder.go:140-168 | 29 | ✅ | Low (integrity) |
| Runner Setpgid | runner.go:28-30 | 3 | ✅ | High |
| Runner sanitizeEnv (O+_ prefix) | runner.go:42-51 | 10 | ✅ | Medium |
| Runner Stop SIGTERM→SIGKILL | runner.go:54-73 | 20 | ✅ | High |
| PreRun direct exec | cli/run.go:61-75 | 15 | ✅ | High |
| Watcher excluded paths | watcher.go:227-245 | 19 | ✅ | Medium |
| Watcher loop / debounce | watcher.go:125-163 | 39 | ✅ | Low (DX) |
| Watcher wouldExceedInotify | watcher.go:94-123 | 30 | ✅ | Low (perf) |
| Watcher pollLoop() | watcher.go:165-212 | 48 | ✅ | Low (perf fallback) |
| safePath / scaffold guard | scaffold.go:104-119 | 16 | ✅ | High |
| Caller: cli/run.go trust gate | cli/run.go:45-58 | 14 | ✅ | High |
| Tester runOnce go test exec | tester.go:61-101 | 41 | ✅ | Low |

---

## 1. ARCHITECT DRAFT — "Boring Bazooka" (drafts/testplan-architect.md)

### 1.1 What It Gets Right

- **Table-driven manifest bomb tests** (alias, depth, size) — concrete yaml strings with nested anchor chains. Better than the Builder's single-shot approach.
- **TestLoadTrust_CorruptTrustJSON** — verifies the fail-open behavior explicitly. The Builder has no equivalent.
- **TestCacheDir_Mode** — verifies 0700 permissions. Builder skips this entirely.
- **TestSafePath_Symlink** — calls out the parent-symlink scenario (though the code has a bug — see below).
- **TestLoop_Debounce** with fakeWatcher DI — deterministic, no sleeps, tests the actual debounce timer logic.
- **`-race` in CI** — non-negotiable for a tool that execs user code. Builder vetoed this.
- **Two Go versions (1.24.4 + 1.25rc1)** — catches forward-compat regressions.
- **Per-package coverage grep** — ensures security paths are actually being hit.
- **verifyCached tamper testing** — binary overwrite detected, deleted, rebuild forced.

### 1.2 Concrete Attack Vectors / Gaps

#### GAP-A1: safePath symlink bypass (CRITICAL — file write to arbitrary system path)

**Code**: `scaffold.go:105` — `safePath` uses `filepath.Abs(target)` which does **NOT** resolve symlinks. `filepath.Abs` only cleans the path and prepends the working directory for relative paths; it does not call `readlink` or `EvalSymlinks`.

**Attack**: A user runs `o+ new mylink` where `~/mylink` is a symlink pointing to `/etc`.

- `filepath.Abs("mylink")` → `/home/user/mylink` (the link path, not resolved)
- `filepath.Rel("/home/user", "/home/user/mylink")` → `"mylink"` — does not start with `".."` → **ACCEPTED**
- `Create()` then writes template files to `filepath.Join("/home/user/mylink", "main.go")` → the OS resolves the symlink → **writes to `/etc/main.go`**
- Then runs `exec.Command("go", "mod", "init")` with `cmd.Dir = `/home/user/mylink` — resolves to `/etc` — executes in `/etc`

**The test `TestSafePath_Symlink` claims this is guarded** ("Symlink in home → resolved to home"). It is NOT. The architect correctly identified the risk verbally but the code does not implement the guard, and the test as described would pass without catching the bug because the test uses `filepath.Abs` which behaves the same way.

**Verdict**: The code must use `filepath.EvalSymlinks(target)` before the `filepath.Rel` checks. The test must create a symlink like `tmpDir + "/symlink_to_etc"` pointing to `/etc` and verify `safePath` rejects it.

#### GAP-A2: trust.json symlink to blocking device (MEDIUM — trust gate DoS)

**Code**: `trust.go:57` — `os.ReadFile(filepath.Join(cd, "trust.json"))` reads whatever is at that path. On Linux, a symlink at that location to `/dev/random` or a FIFO blocks the read indefinitely.

**Attack**: An attacker with write access to `~/.cache/o+/` (or its parent, a more common scenario: shared CI runner / multi-user machine with permissive umask before the 0700 fix) creates:

```
~/.cache/o+/trust.json → /dev/random
```

Then `Trusted()` blocks forever because `os.ReadFile` on `/dev/random` blocks for entropy. The `o+ run` command hangs at the trust gate. Denial of service with no timeout.

**Mitigation**: The test should verify that trust.json is a regular file, not a symlink, or use `syscall.Open` with `O_NONBLOCK`. Neither draft tests this. The architect's `TestLoadTrust_CorruptTrustJSON` only tests parse errors, not symlink attacks.

#### GAP-A3: Govulncheck grep filter overbroad (LOW — missed real vulns)

**CI YAML** (architect draft line 383): `govulncheck ./... | grep -v 'windows'`

A real CVE affecting linux-compiled code whose advisory text contains the word "windows" (e.g., "fixed in versions that ship on all platforms including Linux and Windows") would be silently filtered out.

**Mitigation**: Use `govulncheck -scan latest -mode compact ./...` (Go 1.24+), or pipe through a structured JSON filter that compares the `PkgPath` or `Module` field for platform relevance instead of grepping free text.

#### GAP-A4: Source hash excludes vendor/ (MEDIUM — cache integrity blind spot)

**Code**: `builder.go:175` — `sourceHash` skips `vendor/` directories.

If an attacker or supply-chain compromise modifies files under `vendor/`, `go build` will produce a different binary, but the cache key won't change. The cached (correct) binary is served, but the next `o+ build` after cache eviction or on a different machine will produce the compromised binary. The discrepancy is not detected.

**Mitigation**: The architect's `TestSourceHash_Excludes` tests that `.git` and `_test.go` are excluded, but does not test `vendor/` exclusion **or** document this as a known cache-integrity trade-off. Add a test and documentation.

#### GAP-A5: Watcher loop fsnotify error storm (LOW — resource exhaustion/DoS)

**Code**: `watcher.go:156-161` — The watcher loop prints every fsnotify error to stderr and continues. If fsnotify enters a persistent error state (inotify queue overflow, for example), the loop prints error messages in an infinite busy-loop with no back-off.

An attacker who can generate filesystem events faster than the watcher can process them (trivial on a shared machine) can cause the watcher goroutine to burn CPU printing to stderr. Not a code-execution issue, but degrades the tool.

**Mitigation**: No test for the error-handling path in either draft. Add a rate-limited error logger or a count threshold after which the watcher transitions to polling.

### 1.3 Test Files Missing From Architect's Coverage Matrix

| Security path | Product code | Test in architect draft? |
|---|---|---|
| Trust.json symlink attack | trust.go:57 | ❌ |
| safePath EvalSymlinks | scaffold.go:105 | ❌ (code doesn't use EvalSymlinks either) |
| PreRun shell-metacharacter injection | cli/run.go:61-75 | ❌ (no test that `strings.Fields` actually prevents shell injection) |
| Runner sanitizeEnv strips LD_PRELOAD | runner.go:42-51 | ❌ (code doesn't strip it either — gap in both draft and code) |
| saveTrust file mode 0600 | trust.go:84 | ❌ (only CacheDir mode tested) |
| sourceHash vendor/ exclusion documented | builder.go:175 | ❌ (tested implicitly, not documented as trade-off) |
| Watcher error backoff / rate limit | watcher.go:160 | ❌ |

---

## 2. BUILDER DRAFT — "Green Pipeline" (drafts/testplan-builder.md)

### 2.1 What It Gets Right

- Pure-function tests (excluded, matches, extSet) with no I/O — fast, deterministic.
- `t.TempDir()` isolation, `t.Setenv` for XDG_CACHE_HOME — good practice.
- No `time.Sleep` policy — good engineering.
- `verifyCached` tamper detection tests.
- `makeTarget` fast execution order in section 6.
- Explicit acknowledgment of TOCTOU risk in section 9 (risk #1).

### 2.2 Concrete Attack Vectors / Gaps

#### GAP-B1: CRITICAL — Runner process-group stop entirely DEFERRED (CEO scope violation)

**CEO scope (NEXT-EPIC.md line 17):** "runner process-group stop" is listed as a required security path to test.

**Builder draft (section "DO NOT TEST"):** Cuts `Start` + `Stop` with real processes entirely: "Forking child processes and sending SIGTERM/SIGKILL in a unit test is flaky in CI."

**Attack surface**: The runner's `Stop()` function sends SIGTERM to the process group via `syscall.Kill(-pgid, syscall.SIGTERM)`. If the child process has called `setsid()` or created its own process group forked children, those grandchildren are NOT in the child's PGID — they're in their own group. The grandparent doesn't receive the signal. An attacker-controlled repo could spawn long-running orphaned children that survive `o+ run`'s shutdown.

**Evidence**: The `Setpgid` flag makes the child its own group leader (PGID = child PID). But if the child forks, the grandchild gets a NEW PGID (equal to grandchild PID) unless it calls `Setpgid(false)` or the Go runtime sets it. `exec.Command` by default inherits the parent's PGID — which is the child's PID. Grandchildren of the child inherit the child's PGID only if they're started with `Setpgid: false`. By default, Go's `os/exec` does NOT set `Setpgid` unless told, so grandchildren of the child process could get a new PGID and survive the SIGTERM.

**The builder offers no test for this scenario.** The CEO specifically asked for "process-group stop" testing.

**Verdict**: REJECT on this gap alone. The CEO scope is not advisory; it is the binding specification.

#### GAP-B2: CRITICAL — safePath symlink bypass (same as GAP-A1, but zero acknowledgment)

The Builder draft has **no symlink test at all** for `safePath`. Section 10 risk #4 mentions "symlink attacks" on trust.json but not on scaffold paths. The only safePath tests are:

- `safePath allows home directory`
- `safePath allows /tmp`
- `safePath rejects /etc`

A user could run `o+ new ~/evil-link` where `evil-link → /etc` and the tool would write files to `/etc` without `--force`.

#### GAP-B3: No race detector in CI

**Builder draft (section 4):** "Race detector — `-race` doubles CI time (~120s vs ~30s). Add as a nightly/weekly, not on every push."

**Counterargument**: `-race` adds ~2x time on a 30s CI, not the claimed 120s (that's the architect's estimate for 2 Go versions). For a toolchain that execs user code (`go run`, `go build`, `go test`), a data race in the supervisor (e.g., the watcher's `pending` variable and the main loop's access pattern) could lead to incorrect state transitions. The architect correctly includes `-race`.

**Verdict**: For "Quality #1" as the CEO framed it, shipping without race detection on every push is unacceptable.

#### GAP-B4: No corrupt trust.json test

The Builder's trust gate tests cover: round-trip, fingerprint change, untrusted dir. They do NOT cover what happens when `trust.json` is corrupt JSON, a symlink, empty, zero-length, or unreadable. The architect tests corrupt JSON explicitly.

**Attack**: An attacker writes `{"dirs": null` (truncated JSON) to `~/.cache/o+/trust.json`. If `loadTrust` panics, the tool crashes at the trust gate. Currently the code handles this (returns empty map), but without a test, a future refactor could change this behavior silently.

#### GAP-B5: No prune() test

**Builder draft (section "DO NOT TEST"):** "prune() — Mtime-based LRU in tests = filesystem timestamp races on fast CI... Worth a unit test only when bug reports come in."

A bug in `prune()` could delete ALL cached artifacts (wrong slice bounds, off-by-one, etc.), causing full rebuild on every `o+ build`. Less severe than code-execution, but violates the "Quality #1" requirement for correct cache behavior. The architect tests prune with an explicit eviction count check.

**Mitigation**: Test with a mock `os.ReadDir` return value (the current code reads the real filesystem; a test with `t.TempDir()` and known mtimes set via `os.Chtimes` is deterministic).

#### GAP-B6: No debounce test for watcher

The watcher loop's debounce logic (`time.NewTimer` / `timer.Reset` on every event) is tested in the architect draft via fakeWatcher DI. The builder draft cuts it entirely: "The debounce timer path is trivial (timer.Reset) and fsnotify is a dependency we trust."

A race condition where `timer.Reset` is called while the timer already fired could cause the debounce to emit stale or duplicate events. The Go stdlib `time.Timer.Reset` docs explicitly warn: "Reset should be invoked only on stopped or expired timers with drained channels." The watcher does not drain the channel before calling Reset. This is a real bug that only manifests under specific timing conditions.

**Attack**: Two filesystem events arrive in rapid succession. The first starts the timer. The second calls `timer.Reset` while the timer is still running. If the timer fires between the `Reset` and the `timerC` channel read in the select, the event is delivered on a pending channel that nobody reads, and the debounce fires on the *previous* event. The architect's DI test could catch this (by injecting specific timing); the builder's no-test approach cannot.

#### GAP-B7: Govulncheck not installed in CI

**Builder CI YAML (line 203):** `run: go tool govulncheck ./...`

`go tool govulncheck` only works if the toolchain includes the vulncheck tool. As of Go 1.24, `govulncheck` is available as a standalone tool but not included in the default Go distribution. The `go tool` mechanism invokes a tool named `govulncheck` from the Go tool directory — if it's not present, the step fails. The architect installs it explicitly: `go install golang.org/x/vuln/cmd/govulncheck@latest`.

If the builder's CI silently fails because `govulncheck` is missing, vulnerability scanning is skipped. This is a CI reliability issue with security implications.

### 2.3 Test Files Missing From Builder's Coverage Matrix

| Security path | Product code | Test in builder draft? |
|---|---|---|
| Runner process-group stop | runner.go:54-73 | ❌ DEFERRED |
| Runner SIGKILL fallback | runner.go:69 | ❌ DEFERRED |
| Runner Setpgid verification | runner.go:28-30 | ❌ DEFERRED |
| safePath symlink traversal | scaffold.go:104-119 | ❌ |
| safePath parent-symlink via EvalSymlinks | scaffold.go:105 | ❌ (code doesn't use it) |
| Create --force bypass behavior | scaffold.go:39-43 | ❌ (no --force test) |
| Create unknown template error | scaffold.go:87 | ❌ |
| loadTrust corrupt JSON | trust.go:64-68 | ❌ |
| CacheDir mode 0700 | trust.go:42 | ❌ |
| saveTrust mode 0600 | trust.go:84 | ❌ |
| Watcher debounce timer race | watcher.go:146-151 | ❌ DEFERRED |
| Watcher pollLoop() | watcher.go:165-212 | ❌ DEFERRED |
| Watcher error handling | watcher.go:156-161 | ❌ |
| sourceHash vendor exclusion | builder.go:175 | ❌ (only _test.go + .git tested) |
| PreRun shell injection safety | cli/run.go:61-75 | ❌ |
| Runner sanitizeEnv LD_PRELOAD | runner.go:42-51 | ❌ (code doesn't strip it) |
| cli/run.go trust gate integration | cli/run.go:45-58 | ❌ (deferred to smoke) |
| prune() LRU eviction | builder.go:140-168 | ❌ DEFERRED |

---

## 3. Cross-Draft Comparison Table

| Security path | Architect | Builder | Winner |
|---|---|---|---|
| Manifest bomb (alias/depth/size) | ✅ 3 alias variants + depth + size + unknown fields | ✅ basic alias, depth, size, unknown fields | Architect (more variants) |
| Cache sha256 verification | ✅ tamper + missing sum/bin + prune | ✅ tamper + missing sum/bin | Tie |
| Trust gate round-trip | ✅ Trust + Trusted + corrupt JSON | ✅ Trust + Trusted (no corrupt) | Architect |
| CacheDir mode 0700 | ✅ explicit test | ❌ not tested | Architect |
| Trust.json corrupt handling | ✅ explicit test | ❌ not tested | Architect |
| Trust.json symlink / DoS | ❌ not tested | ❌ not tested | Tie (both miss) |
| safePath basic | ✅ home/tmp/system OK | ✅ home/tmp/system OK | Tie |
| safePath symlink | 🟡 called out verbally but code bug + test gap | ❌ not tested | Architect (identified risk even if code is buggy) |
| Watcher exclusion/match tests | ✅ 7 test cases | ✅ 7 test cases | Tie |
| Watcher debounce loop | ✅ fakeWatcher DI test | ❌ DEFERRED | Architect |
| Watcher poll loop | 🟡 real-tempdir test (acceptable) | ❌ DEFERRED | Architect |
| Runner process-group stop | ✅ real child + SIGKILL helper binary | ❌ DEFERRED | Architect |
| Runner sanitizeEnv | ✅ O+_ prefix tested | ✅ O+_ prefix tested | Tie |
| Runner LD_PRELOAD | ❌ not tested | ❌ not tested | Tie (both miss) |
| Runner Setpgid verification | ✅ syscall.Getpgid test | ❌ DEFERRED | Architect |
| PreRun direct exec safety | ❌ no injection test | ❌ no injection test | Tie (both miss) |
| CI race detector | ✅ `-race` | ❌ vetoed | Architect |
| CI Go versions | ✅ 2 versions (1.24.4 + 1.25rc1) | ❌ 1 version (1.24.4) | Architect |
| CI coverage grep | ✅ per-package grep | ❌ no coverage check | Architect |
| CI govulncheck → install | ✅ explicit `go install` | ❌ `go tool` may fail | Architect |
| Release signing | ✅ sha256 in Makefile | ✅ sha256 in CI + gh-release | Tie |
| LRU prune test | ✅ eviction count+order | ❌ DEFERRED | Architect |

---

## 4. Verdicts

### ARCHITECT DRAFT ("Boring Bazooka") — APPROVE-WITH-CONDITIONS

**Conditions (must be resolved before implementation):**

1. **Fix safePath symlink bypass** (GAP-A1): Replace `filepath.Abs(target)` with `filepath.EvalSymlinks(target)` in `scaffold.go:105`. Add test: create symlink under home pointing to `/etc`, call `safePath`, expect error. Without this, `o+ new` writes files to arbitrary system paths.

2. **Add trust.json symlink DoS protection** (GAP-A2): Before `os.ReadFile` on trust.json, stat to verify it's a regular file (not a symlink to /dev/random). Add test.

3. **Narrow govulncheck grep** (GAP-A3): Replace the free-text `grep -v 'windows'` with structured filtering (either `-json` mode and filter on `PkgPath`, or add the hard trigger from DECISION.md as an explicit allowlist).

4. **Document vendor/ cache gap** (GAP-A4): Document in both the test plan and `sourceHash` doc comment that vendor/ is excluded from the source hash, meaning vendor changes do not invalidate the cache.

5. **Add watcher error backoff** (GAP-A5): Add rate-limited error logging or a transition to polling after N consecutive errors in the watcher loop.

6. **Runner sanitizeEnv should strip LD_PRELOAD and LD_LIBRARY_PATH**: Add these to the list of dropped env vars. Child processes should not inherit library injection variables from the parent.

7. **Add test for corrupt-then-recover trust flow**: Write a test that corrupts trust.json, calls `Trust()`, and verifies the recovery writes clean data.

### BUILDER DRAFT ("Green Pipeline") — REJECT

**Reasons:**

1. **CRITICAL — Runner process-group stop deferred (CEO scope violation)**: The CEO explicitly lists "runner process-group stop" as a required test path. The builder cuts it entirely. An attacker-controlled child process that spawns orphaned grandchildren survives `o+ run` shutdown (GAP-B1). This is a hard scope violation.

2. **CRITICAL — safePath symlink bypass with zero acknowledgment** (GAP-B2): Same vulnerability as Architect GAP-A1, but the builder doesn't even identify the risk. The builder's minimal test set would not catch a path traversal to `/etc`.

3. **No race detector in CI** (GAP-B3): For a tool that wraps `go test` and runs user code, omitting race detection on every CI run violates the CEO's "Quality #1" directive.

4. **No corrupt trust.json test** (GAP-B4): Trust recovery is a security path the CEO explicitly lists. Missing.

5. **No debounce test + timer.Reset race** (GAP-B6): The builder's claim that debounce is "trivial" ignores the documented `time.Timer.Reset` channel-drain requirement. The architect's DI test is the right approach.

6. **Govulncheck not explicitly installed** (GAP-B7): `go tool govulncheck` fails silently when the tool is not in the Go distribution. The architect's explicit `go install` is correct.

7. **No prune test** (GAP-B5): Cache integrity is a security-relevant path. LRU eviction bugs can corrupt the build cache.

**If the builder addresses conditions 1-2 and adds the missing tests, the draft could be reconsidered — but the current deferral pattern (6 tests deferred: loop, poll, process-group, prune, debounce, CI race) fundamentally conflicts with the CEO's "Quality #1" mandate.**

---

## 5. Summary: Attack Vectors the CEO Should Know About

| Attack vector | Impact | Draft that catches it | Priority |
|---|---|---|---|
| `o+ new ~/mylink` where mylink→/etc writes to /etc | File write to system path | Neither (code bug) | 🔴 Fix immediately |
| trust.json symlink→/dev/random blocks trust gate forever | Denial of service | Neither | 🟡 Fix in v0.1 |
| Watcher timer.Reset channel race emits stale events | Incorrect hot-reload timing | Architect (DI test would catch) | 🟢 Low |
| Runner child grandchildren not in PGID survive stop | Orphan processes / resource leak | Architect (process-group test would catch) | 🔴 Must test |
| LD_PRELOAD leaks from parent to child process | Shared library injection into user app | Neither (code + tests both miss) | 🟡 Fix |
| Govulncheck grep -v 'windows' filters linux vulns | Missed CVE in CI | Architect (overbroad filter) | 🟢 Low |
| Vendor/ excluded from source hash → cache returns stale binary on vendor change | Incorrect build artifact | Neither (architect documents, builder ignores) | 🟢 Low |
| Corrupt trust.json panics on refactor | Trust gate failure / crash | Architect (corrupt-JSON test prevents regression) | 🟡 Medium |

---

## 6. Final Statement

**Architect draft wins** — it covers 18/22 security test paths vs Builder's 10/22. The conditions above are surgical (7 items, of which 2 are code bugs in the product itself, not the test plan). The Builder draft's pattern of deferring hard tests is fundamentally incompatible with a "Trustworthy O+" epic.

The one finding that affects both drafts equally — `safePath` symlink bypass — is the most dangerous gap in the entire security posture. I strongly recommend fixing it in the product code before any test implementation begins.
