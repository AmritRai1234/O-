# O+ v0.1 — Staff Architect Draft

**Persona:** Staff Architect  
**Approach name:** "Boring Bazaar"  
**Date:** 2026-08-25  
**Status:** Draft for Security + Performance review  

---

## Executive Summary

O+ is a Bun-class Go developer toolchain: one static binary providing `o+ run` (hot-reload), `o+ build`, `o+ test`, `o+ new` (scaffold), and `o+ bundle` (deferred). This draft prioritizes Quality #1, Safety #2, and Simplicity #4 over clever or novel internals. Every deviation from "what Go already gives us" must earn its complexity by solving a real developer-friction problem — not a hypothetical one.

---

## 1. Module Layout

```
o+/
├── main.go                        # CLI entry: cobra dispatch
├── o+.yaml                        # Project manifest (optional, per-project)
├── internal/
│   ├── cli/                       # Cobra commands (run, build, test, new)
│   │   ├── run.go
│   │   ├── build.go
│   │   ├── test.go
│   │   └── new.go
│   ├── watcher/                   # fsnotify file-watcher abstraction
│   │   └── watcher.go
│   ├── runner/                    # Process lifecycle (spawn/kill/restart)
│   │   └── runner.go
│   ├── builder/                   # go build orchestration + cache layer
│   │   ├── builder.go
│   │   └── cache.go
│   ├── tester/                    # go test UX wrapper
│   │   └── tester.go
│   ├── manifest/                  # o+.yaml parser (yaml.v3, strict mode)
│   │   └── manifest.go
│   ├── scaffold/                  # `o+ new` template engine
│   │   ├── scaffold.go
│   │   └── templates/
│   └── version/                   # --version, build info injection
│       └── version.go
├── go.mod
├── Makefile                       # Dev-only: build, lint, test O+ itself
└── README.md
```

**Key design decisions:**
- Everything under `internal/` — no public API surface. O+ ships as a single binary, not a library. This eliminates semver coupling to consumers.
- `cobra` for CLI dispatch. It is boring, proven, battle-tested in the Go ecosystem (Hugo, Docker Machine, etcd). 0 CLI framework risk.
- `yaml.v3` (gopkg.in/yaml.v3) for manifest parsing. YAML is the lingua franca of Go tooling (Docker Compose, GitHub Actions, Kubernetes) — the lowest-friction choice. Strict decoding, size limits, no anchor resolution by default to prevent YAML bomb attacks.
- No plugin system. No WASM. No Lua scripting. O+ is a single static binary with zero runtime extension points. This is a deliberate simplicity bound.

---

## 2. Hot-Reload Mechanism: fsnotify + exec (Not Long-Running Server)

**Choice: fsnotify + subprocess exec**

After evaluating both options against Quality #1, Safety #2, and Simplicity #4, the fsnotify+exec model wins decisively.

### 2a. Why NOT long-running server

| Concern | Assessment |
|---|---|
| Process management | O+ becomes a process supervisor: stdin/stdout plumbing, TTY forwarding, graceful shutdown, orphan cleanup, port-contention detection, zombie reaping. Each is a subtle bug farm |
| State leaks | User binary holds sockets, file handles, goroutine state across reloads? No — each exec is a clean process boundary. Server model requires process restart anyway or gets state corruption |
| Cross-platform | Windows job objects, Linux cgroups, macOS dispatch — three different process-group APIs to manage. exec + process group kill is portable |
| Testing complexity | Server model: integration tests must coach signal delivery, startup races, racey-graceful shutdown. exec model: start binary, send SIGTERM, check exit code |
| Security surface | Long-running daemon holding user credentials in memory across reloads is a credential-stale attack surface. Fresh exec = fresh process = no memory carryover |

**Verdict:** Long-running server is clever but fragile. It hurts us in 6 months when we ship Windows support or hit a zombie-process bug in production. Vetoed.

### 2b. fsnotify + exec design

```
User edits file → fsnotify event coalesced (100ms debounce) → 
  SIGTERM to previous child process group → 
  wait for process exit (3s timeout → SIGKILL) → 
  `go build -o /tmp/o+run-XXXX` (incremental via Go cache) → 
  exec new binary (same pwd, same env, fresh PGID)
```

**Boring technology stack:**
- `github.com/fsnotify/fsnotify` — the defacto standard, 12+ years old, 10k+ GitHub stars, maintained by the Go project. Not clever.
- `os/exec` — stdlib. No wrapper library.
- `os.Process.Signal` + `syscall.Kill(-pgid, syscall.SIGTERM)` — stdlib.

**Safety mechanisms:**
1. **Process group isolation** — child starts with `Setpgid: true` in `os.ProcAttr`. O+ sends `SIGTERM` to the *group*, not the PID. This kills the entire process tree of the user's app, not just the root process (a common pitfall).
2. **Kill timeout** — 3s grace after SIGTERM, then SIGKILL. Configurable via manifest. Prevents orphan processes.
3. **Build-before-kill** — compile the new binary first, then kill old. If compile fails, old process keeps running. Developer gets a compile error, not a dead process. This is critical for DX.
4. **Port contention** — O+ does not manage ports. User app manages its own port binding. If the old process hasn't released the port by the time the new one starts, the OS `SO_REUSEADDR` behavior handles it on Linux; on conflict, the new binary gets `EADDRINUSE` and the developer sees the error — obvious, debuggable.

**Watch defaults:**
- Recursive watch with fsnotify, auto-exclude `.git`, `vendor`, `dist`, `node_modules`, `*.test`, `_test.go` (tests don't trigger reload).
- Linux inotify limit detection: on startup, check `/proc/sys/fs/inotify/max_user_watches`. If current count > 80% of limit, print a clear warning with the fix command.

---

## 3. Build Caching

**Strategy: Delegate to Go, layer minimal metadata on top.**

Go already has a sophisticated build cache at `$GOCACHE` (since Go 1.10, constantly improved through 1.24). Our cache layer is **thin metadata only**:

1. **Manifest hash** — `o+.yaml` + any `//go:embed` directives produce a deterministic cache key that `o+ build` checks. No need to re-invent content-addressed storage.
2. **Output caching** — `o+ build` stores the final binary at `.o+/build/<manifest-hash>/o+`. If manifest hash matches, we skip the `go build` call entirely and just symlink/copy the cached binary. This is a developer-latency optimization, not a correctness optimization.
3. **Incremental rebuilds** — when manifest changes but source doesn't, `go build` hits its own cache and completes in <500ms. We don't intervene.

**Cache invalidation:**
- Manifest hash changes → full rebuild
- Source changes → Go cache handles it incrementally
- `go.mod` / `go.sum` changes → Go cache handles it
- `go version` upgrade → `$GOCACHE` is versioned, automatically invalidated

**Cache directory:** `$XDG_CACHE_HOME/o+/` or `~/.cache/o+/`. Single directory, no subproject fragmentation. Stores last 10 build artifacts.

**Deliberate non-goal:** O+ does NOT implement its own content-addressed compilation cache a la Bazel/ccache. That is a 6-month-from-now problem. Today, Go's cache is good enough. If profiling proves otherwise in v0.2, we add a thin ccache-like layer over `go tool compile` — but that's a premature optimization today.

---

## 4. Test UX (`o+ test`)

**Principle:** Wrapping `go test` for better UX, not reimplementing test infrastructure.

Go test already has:
- Caching (passing tests don't re-run on unchanged code)
- JSON output (`-json` flag since Go 1.10)
- Race detection (`-race`)
- Coverage (`-coverprofile`)
- Fuzzing (`-fuzz`)
- Benchmarks (`-bench`)

What we add:
1. **Colorized per-test output** — parse `go test -json` output in real time. Print `✓` (green) / `✗` (red) per test case, not per package. Group by package. Show elapsed per test on failure.
2. **Watch mode** (`o+ test --watch`) — re-run tests on file change using the same fsnotify watcher. Pass/fail summary updates in-place (terminal escape codes). First run is full; subsequent runs are incremental (Go cache).
3. **Compact mode** (`o+ test --compact`) — for 200+ test suites, show a progress bar per package with failure-summary at end. No line per test.
4. **`--pattern` / `--skip`** — forwarded to `go test -run` / `go test -skip` with friendly names.
5. **`--bench`** — forwarded to `go test -bench=.`.
6. **Graceful interrupt** — SIGINT during `o+ test` kills `go test` cleanly and prints partial results.

**What we DON'T do:**
- No test UI / browser dashboard. (v0.2+)
- No custom assertion library. (Use `go-cmp` / `stretchr/testify` as the user chooses)
- No concurrency tuning — `go test -parallel` is already user-configurable.
- No flaky-test retry logic. (v0.2 feature request)

**Implementation:** Run `go test -json` via `exec.Command`, pipe stdout to a JSON decoder goroutine, feed to a terminal renderer using `github.com/fatih/color`. The renderer is the only third-party dep beyond cobra, fsnotify, yaml.v3.

---

## 5. Manifest Format (`o+.yaml`)

**Chosen format:** YAML via `gopkg.in/yaml.v3` with **strict decoding** (unknown fields rejected).

```yaml
# o+.yaml — O+ project manifest
name: my-service
version: "0.1.0"

# project type drives defaults for build tags, output name
type: app          # app | lib | tool

build:
  # output path (default: ./dist/<name>)
  output: ./dist/my-service
  # extra ldflags (default: none)
  ldflags:
    - -X main.Version={{.Version}}
  # build tags (default: none)
  tags: []
  # static linking (default: true)
  static: true
  # compress binary (default: false, v0.2 candidate)
  compress: false

run:
  # glob patterns to watch (default: ["./**/*.go"])
  watch:
    - ./**/*.go
    - ./**/*.yaml
    - ./**/*.html
  # paths to exclude (always excludes .git, vendor, dist, node_modules)
  exclude:
    - ./**/*_test.go
    - ./tmp
  # pre-run hook (default: none). exits if fails.
  pre_run: []

# test configuration (optional)
test:
  # default tags for test runs
  tags: []
  # timeout per test suite (default: 10m)
  timeout: 10m
```

**Decoding rules:**
- Strict mode: unknown keys in `o+.yaml` cause a hard error, not silent ignore. Prevents "typo silently defaults to wrong behavior" — a genuine Bun DX antipathy I want to avoid.
- No environment variable interpolation in the manifest. `env`-based substitution is a footgun (escaping, platform-differences). Use `--flag` overrides instead.
- No anchors/aliases (`&anchor`, `*alias`) — disabled in YAML decoder for safety.
- Max file size: 1MB. Prevents billion-laughs-style resource exhaustion.
- Default manifest is 0 lines: if no `o+.yaml` exists, O+ falls back to sensible defaults (name = directory basename, type = app, watch all `*.go`, output `./dist/<name>`). A file isn't required.

---

## 6. What Defer / Reject

### Deferred to v0.2 (+)

| Feature | Rationale |
|---|---|
| `o+ bundle` | Asset bundling scope is unclear. CEO brief says "war room decides." This draft says: defer. Ship v0.1 without it. The `bundle` command stub prints "coming in v0.2." |
| Compressed binary output | UPX/pack. Adds 200ms to every build. Defer until someone asks. |
| Test UI / browser dashboard | Developer builds it themselves with existing Go tooling. |
| Flaky-test retry | Encourages test rot. Not a v0.1 feature. |
| Pre-built binary downloads | `curl | sh` is explicitly against Safety #2. Ship via GitHub releases with checksums only. |

### Rejected for O+ (indefinitely)

| Feature | Why |
|---|---|
| JS engine (goja, v8 CGO, etc.) | CEO hard block. If hosting JS: embed goja behind a thin interface. War room decision. |
| Plugin system | O+ is a single binary. Extension points add runtime complexity, supply-chain risk, and API stability contracts. Hard no. |
| Custom package manager | Go modules work. Replacing them is a 2-year project. |
| npm/Cargo-style registry | Not what O+ is. |
| Web GUI | Not what O+ is. |
| Daemon mode / background service | O+ is a CLI tool. If `o+ run` needs to stay alive, it already does. |
| Telemetry / crash reporting | Privacy headache, infrastructure cost, no user asked for it. Ship without. Add only by opt-in. |

---

## 7. Security Risks (What Security Reviewer Will Attack)

These are the attack angles I expect the Security reviewer to flag; I preempt them here with mitigations, but they remain real risks.

### Risk A: Process-Supervisor Attack Surface (HIGH)

`o+ run` acts as a process supervisor. If O+ itself is tricked — via a malicious `o+.yaml` in a repo the developer `cd`s into — it will exec arbitrary binaries with `pre_run` hooks or `run` targets.

- **Attack vector:** `pre_run: ["./malicious.sh"]` in a project manifest. Developer runs `o+ run` in untrusted repo.
- **Mitigation:** O+ warns "executing pre_run from untrusted project" and requires `--trust` flag to proceed on first run in a directory without a trusted fingerprint. Stored fingerprint: `sha256(o+.yaml + go.mod)`. Subsequent runs check fingerprint match.
- **Residual risk:** Developer clones malicious repo, runs `o+ run --trust` blindly. Same class as `curl | sh`. Acceptable because: (a) developer opted in explicitly, (b) same risk exists with `make`, `npm run`, `cargo run` — this is an ecosystem-wide trust problem, not O+-specific.

### Risk B: YAML Bomb / Resource Exhaustion (MEDIUM)

- **Attack vector:** Billion laughs (`!<tag:yaml.org,2002:anchor`) or deeply nested structures in `o+.yaml`.
- **Mitigation:** (1) Disable anchor/alias resolution in yaml.v3 decoder. (2) Hard max document size: 1MB. (3) Max nesting depth: 64. (4) CPU timeout on decode: 100ms, abort if exceeded.
- **Residual risk:** A giant flat YAML with 100k keys wastes memory but O+ is a short-lived CLI process — that's a DoS against the developer's own machine, not a meaningful attack.

### Risk C: Watch-Path Traversal (LOW)

- **Attack vector:** `o+.yaml` specifies `watch: ["../../etc"]`, causing O+ to watch system directories.
- **Mitigation:** Resolve all watch paths relative to project root. Reject paths with `..` after resolution. Warn if watching outside project directory.
- **Residual risk:** Symlink traversal is possible but fsnotify follows symlinks — a developer could symlink `/etc` into their project dir. This is self-inflicted.

### Risk D: Build Cache Poisoning (LOW)

- **Attack vector:** Shared `~/.cache/o+/` on multi-user system. User A's cached artifact replaced by User B with a trojan binary.
- **Mitigation:** Cache manifest includes `sha256` of every cached artifact. On cache hit, O+ re-hashes the binary and compares. Mismatch → reject cache, rebuild. Also: `$XDG_CACHE_HOME` defaults to `~/.cache`, which is per-user on any sane configuration.
- **Residual risk:** If `$XDG_CACHE_HOME` points to a world-writable directory, a race-condition attack between hash verification and binary exec is theoretically possible (TOCTOU). Mitigation: verify hash again immediately before exec. Acceptable for v0.1; a dedicated daemon/service would eliminate it but adds complexity (see section 2a).

---

## 8. Performance Risks (What Performance Skeptic Will Attack)

### Risk E: Compile Latency on Every Save (HIGH)

**The numbers:**
- Small project (<10 packages): `go build` incremental → ~200-500ms
- Medium project (50-200 packages): incremental → ~800-2000ms
- Large monorepo (500+ packages): incremental → ~2-5s

A 2-second delay between saving a file and seeing the app restart is the kind of "stutter" that makes a developer reach for `make test` instead of `o+ run`. This is the single biggest performance risk for the hot-reload experience.

**Mitigation plan:**
1. **Build-in-background:** New binary compiles concurrently with old binary still running. When compile finishes and old binary is still healthy, kill old → exec new. If compile takes 3s, the developer doesn't wait 3s — they see the old app continue until the new one is ready.
2. **Cache-only passes:** `o+ run --fast` does `go build -o /dev/null` (discard binary) to get Go cache warming + compile error detection; only does the full build+exec when the dev presses Enter or after N consecutive clean compiles. Tradeoff: the hot-reloaded binary may lag a save behind. This is acceptable for development iteration.
3. **Lazy reload:** After compile error, O+ stays on the running old binary. Developer fixes the typo, saves → recompile. No restart needed for error recovery.

**Residual risk:** For 500+ package monorepos, even fast build-in-background won't beat Bun's JS non-compile model. O+ can't compile faster than Go compiles. This is a fundamental material constraint, not a design failure. O+ must document this honestly: "O+ hot-reload latency scales with your project size. For large monorepos, consider `make run` or a dedicated dev server."

### Risk F: inotify Watch Exhaustion (MEDIUM)

**The numbers:**
- Default `max_user_watches`: 8192 (most distros) or 65536 (Fedora/Ubuntu defaults)
- Each watched directory = 1 inotify watch
- A 50-package Go project with vendor = ~500-2000 directories
- A monorepo with generated protobuf, node_modules, vendor = ~10k+ directories

**Mitigation:**
1. Auto-detect inotify limit at startup. Print clear actionable error: "O+ needs more inotify watches. Run: `sudo sysctl -w fs.inotify.max_user_watches=524288 && echo fs.inotify.max_user_watches=524288 | sudo tee -a /etc/sysctl.conf`"
2. Default exclusion list is aggressive: `.git`, `vendor/`, `dist/`, `node_modules/`, `*.test`, `_test.go`, `.cache/`, `.o+/`.
3. Watch overflow fallback: when watch count > 80% of limit, O+ switches to polling (500ms interval, file modtime check). Warns the developer. Polling is slower but doesn't fail silently.
4. Single-inotify-watch strategy: on Linux ≥ 4.1, fsnotify supports `INotifyWithQueue` (queued watch for directory + subdirs via one watch + fanotify? Actually no — fsnotify does per-directory watches. The real mitigation is polling fallback).

### Risk G: YAML Parsing Overhead on Every CLI Invocation (LOW)

**The numbers:**
- Fresh parse of a 50-line `o+.yaml`: ~100µs
- With `RecheckManifestOnChange` (watch for manifest change during `o+ run`): zero additional cost — modtime check is lstat, ~5µs
- Cold start of `o+ run` with YAML parse included: ~5-10ms total startup overhead (cobra, fsnotify setup, manifest parse). Negligible.

**Verdict:** Not a real risk. The numbers are too small to matter for a CLI tool. Performance skeptic may mention "why not TOML/JSON for speed" — answer: YAML is the default for Go tooling; developer preference trumps 100µs. If we need to optimize later we switch to `gopkg.in/yaml.v3` to `encoding/json` with a `o+.json` alternative, not a breaking change.

### Risk H: Concurrent Build + Test Cache Stampede (MEDIUM)

**The numbers:**
- Running `o+ test --watch` while `o+ run` is active means two `go build` processes may contend for the same Go cache.
- Go 1.24's cache is safe for concurrent access (file-level locking in `cmd/go/internal/cache`), but contention adds latency: head-of-line blocking on `GOCACHE` writes.

**Mitigation:**
- Single O+ instance tracks whether a build is in progress. If `o+ test` triggers a build while `o+ run` is building, the test process queues: "waiting for build lock..." then reuses the already-built test binary.
- Per-process `GOCACHE` isolation for builds: `o+ build` uses `GOCACHE=$XDG_CACHE_HOME/o+/default`, `o+ test` uses `GOCACHE=$XDG_CACHE_HOME/o+/test`. Separate write paths, reads can still share content from the system `$GOCACHE`.
- Document that concurrent `o+ run` + `o+ test` in the same terminal is not recommended for large projects.

---

## 9. Verified Risks (Inescapable)

These are risks I cannot design around. They must be accepted or addressed outside the architecture:

1. **Go compilation latency is the bottleneck.** No caching strategy, concurrency pattern, or design trick makes `go build` instant for a 500-package monorepo. O+ can hide latency (build-in-background), mitigate it (cache manifests), or document it — but cannot solve it. If "instant reload" is the dealbreaker requirement, Go is the wrong language for O+.
2. **Cross-platform process management is bug-prone.** SIGTERM → wait → SIGKILL works on Linux; on macOS, `kill(-pgid, SIGTERM)` works but `Setpgid` has different semantics; on Windows there are no signals. O+ v0.1 ships Linux-only, then macOS, then Windows. This is realistic shipping.
3. **Trust model for `pre_run` hooks.** The `--trust` fingerprint model is a speed bump, not a wall. A malicious actor who controls a repository O+ trusts (via `git push --force` on a previously-trusted repo) can execute arbitrary code. Same class of risk as `npm install`, `pip install`, `cargo build`. Ecosystem-wide, not O+-specific.

---

## 10. Decision Matrix Summary

| Decision | Choice | Rejected Alternative | Rationale |
|---|---|---|---|
| Hot-reload mechanism | fsnotify + exec | Long-running server | Safety #2, Simplicity #4. Clean process boundaries beat clever daemon plumbing |
| Build cache | Thin manifest layer over Go cache | Custom content-addressed cache | Premature optimization. Go cache is proven. Add ccache layer only when profiling proves need |
| Test UX | `go test -json` wrapper | Custom test runner | Quality #1: Go's test infra is battle-tested. Wrapping it is lower-risk than reimplementing |
| Manifest format | YAML (strict, bomb-safe) | TOML / JSON / HCL | Default for Go ecosystem. Developer familiarity > 100µs parse time |
| Plugin system | Rejected | WASM / Lua / dynamic | Simplicity #4, Safety #2. No extension points = no extension-point bugs |
| `o+ bundle` | Deferred to v0.2 | Include in v0.1 | Scope unclear. Ship without it |
| Trust model | Manifest fingerprint + `--trust` flag | Blind trust | Safety #2. Same model as SSH host key verification |
| Windows support | Deferred to v0.2 / v0.3 | Ship v0.1 cross-platform | Signal handling, fsnotify, and process groups are different on Windows. Ship Linux first |

---

## 11. Open Questions for Grumpy Principal

1. **Build-in-background complexity:** is the concurrent-build-then-kill architecture worth the complexity for v0.1, or should v0.1 ship simpler (kill-then-build, accept the latency) and add concurrency in v0.2?
2. **Manifest required vs optional:** this draft says optional. Builder's draft may say always-required-for-predictability. Which wins?
3. **`o+ bundle` scope:** defer or define minimally? Deferring means v0.1 ships without it. Defining minimally means adding an asset-embedding `copy` command. Which is correct?