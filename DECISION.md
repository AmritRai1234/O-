# O+ v0.1 — War Room Decision

Ruled: 2026-08-25 · Venue: war room · Project: /home/amritrai/spine/O+

## Rounds

- Round 1: Staff Architect "Boring Bazaar" (drafts/architect.md) vs Pragmatic Builder "Ship It" (drafts/builder.md) — parallel drafts.
- Round 2: Paranoid Security Reviewer + Performance Skeptic reviewed BOTH drafts in parallel (reviews/security.md, reviews/performance.md).

## Verdicts

| Reviewer | Architect | Builder |
|---|---|---|
| Security | APPROVE-WITH-CONDITIONS | REJECT |
| Performance | APPROVE-WITH-CONDITIONS | REJECT |

Security and Performance independently agreed → no Principal arbitration needed (per war-room rule: friction resolved with evidence).

## RULING: Architect wins. "Boring Bazaar" is the v0.1 design.

## Binding Conditions (folded into implementation)

Security:
1. NO /tmp builds — private build dir at $XDG_CACHE_HOME/o+/, mode 0700. Prevents binary planting.
2. sha256 of every cached artifact verified IMMEDIATELY before exec (no TOCTOU gap).
3. pre_run hooks: direct exec only (no shell interpretation). First run in untrusted dir requires --trust; fingerprint = sha256(o+.yaml + go.mod) stored in $XDG_CACHE_HOME/o+/trust.json.
4. Child process: fresh PGID (Setpgid), sanitized env (drop O+ internal vars), no inherited FDs beyond stdin/out/err.

Performance:
5. Build artifact LRU >= 100 (multi-branch dev). Not 10.
6. Polling fallback at 80% inotify capacity (already in draft, confirmed).
7. Build-before-kill confirmed: compile new binary first; on compile error keep old app running.
8. Dual GOCACHE isolation for concurrent o+ run + o+ test (no cache stampede).

## What died with Builder's draft (ledger)

- kill-then-build reload (app downtime on every save) — REJECTED
- JSON manifest with no size cap (unbounded OOM) — REJECTED
- `go build -i` (removed from Go in 1.12) — dead command, REJECTED
- test --watch with -count=1 (disables Go cache, full rerun at scale) — REJECTED
- No trust boundary for manifests — REJECTED

## Locked v0.1 scope

- o+ run — fsnotify + exec hot reload, build-before-kill, --trust model, inotify limit detection + polling fallback
- o+ build — thin manifest-hash cache over $GOCACHE, sha256-verified, LRU 100
- o+ test — go test -json wrapper, colorized per-test, --watch (cache-preserving)
- o+ new — embed.FS templates (minimal, web-server, cli), go mod init
- o+ bundle — DEFERRED to v0.2 (stub prints "coming in v0.2")
- Manifest o+.yaml — strict YAML: KnownFields, 1MB cap, depth limit 64, no anchors
- Linux-first; macOS/Windows deferred
- No plugins, no JS engine, no daemon, no telemetry

## Disagreement Ledger

- [hot-reload mechanism] Architect said build-before-kill, Builder said kill-then-build -> resolved by Performance (zero-app-downtime window 500ms vs 12-42s) -> build-before-kill, tradeoff: +1 concurrent build process
- [manifest format] Architect said strict YAML, Builder said JSON -> resolved by Security (JSON unbounded OOM + no strictness) -> YAML strict, tradeoff: yaml.v3 dep + 100µs parse
- [trust boundary] Architect said --trust fingerprint, Builder said cut hooks entirely -> resolved by Security (no trust boundary = manifests can configure builds without consent) -> --trust model, tradeoff: one-time prompt on untrusted dirs
- [o+ test --watch] Architect kept, Builder cut -> resolved by Performance (cache-preserving watch is cheap) -> keep, tradeoff: -count=1 forbidden
- [build cache] Architect thin LRU layer, Builder none -> resolved by Performance (15-55ms cache hits vs full build) -> LRU 100 + sha256 verify, tradeoff: cache dir management

## Next

Implement per this decision. Verify: go build, go vet, smoke test (o+ new -> o+ run -> o+ test -> o+ build). Vision Inspector gates any rendered output; none in v0.1 core (CLI output is text, verified by smoke test).

## Ledger Addition (implementation round, 2026-08-25)

- [dependency advisory] GO-2026-5024 (x/sys < 0.44.0, integer overflow in NewNTUnicodeString) — NOT exploitable on v0.1 build targets: advisory is in the windows subpackage, our v0.1 is Linux-only, and Go does not compile x/sys/windows for linux targets. Fixed version requires go >= 1.25 (toolchain bump). DECISION: stay on go 1.24.4 + x/sys v0.30.0; HARD TRIGGER to bump x/sys >= 0.44.0 (and go toolchain) the moment Windows targets are added. Documented in README.
- [cache key] Architect's manifest-only cache key could serve a stale binary after source edits (Quality #1 violation). Fixed in implementation: cache key = sha256(manifest fingerprint + source-tree hash). Cost ~10-30ms walk; keeps 15-55ms cache hits correct.
- [scaffold safety] Builder's `o+ new --force` destructive-path concern adopted: Create() refuses paths outside home or /tmp unless --force.
- [language pivot, 2026-08-25] User proposed rewriting O+ in C++ ("might be a real product not just an experiment"). CEO REJECTED and ledgered: (1) language does not make a product real — tests/CI/checksums/users do; (2) hot-reload bottleneck is `go build`, not the wrapper (measured 261ms) — C++ changes nothing; (3) a memory-unsafe supervisor exec'ing untrusted repo hooks violates Safety #2; (4) "real Bun runtime in C++" = beating V8/JSC, research-team problem, same trap as the JS-engine-in-Go rejection; (5) months of rewrite, zero new features. O+ stays Go. If user wants a C++ product, CEO will frame a separate epic where C++ is the right tool.
- [Trustworthy O+ epic, 2026-08-25] War room: Architect "Boring Bazooka" won (Security + Performance both APPROVE-WITH-CONDITIONS vs Builder "Green Pipeline" REJECT). Security review of the TEST PLANS caught 3 REAL v0.1 bugs, all fixed with tests:
  1. safePath symlink bypass — `o+ new ~/link→/etc/x` wrote outside home. Fixed: resolve deepest existing ancestor + compare resolved paths (scaffold_test.go: TestSafePath_SymlinkBypassRejected).
  2. LD_PRELOAD / LD_LIBRARY_PATH inherited by child → shared-library injection into user's app. Fixed: sanitizeEnv drops them (runner_test.go).
  3. trust.json symlink → /dev/random = trust-gate DoS (ReadFile hangs forever). Fixed: Lstat rejects symlinks + 1MB cap (trust_test.go: TestTrust_SymlinkRejected).
  Tests also caught: bare "_test.go" exclude never matched (test files DID trigger reloads) — fixed in watcher.excluded with documented rule forms; runner.Exited() drained the same channel Stop() consumes — fixed with atomic flag.
  Shipped: 8 test files + helper (manifest/builder/watcher/runner/tester/scaffold), integration suite (graceful stop, SIGKILL escalation, grandchild-kill), CI (ci.yml: matrix 1.24.x+1.26.x, vet, test -race -tags integration, govulncheck; coverage job), release workflow (release.yml: tagged push → binary + .sha256), Makefile test/coverage targets.
  Verified: go vet clean; go test -race -tags integration ./... ALL GREEN; coverage: manifest 83.8%, scaffold 78.9%, builder 57.4%, watcher 42.9%, tester 43.5%, runner 23.8% unit (+integration covers process-group); smoke: reload 225ms, _test.go edit correctly does NOT reload. Commit: see git log.
- [v0.2 backlog, from Perf review] sourceHash O(N) walk ~2s at 100k files — incremental hash follow-on; pollLoop CPU spiral at 100k files — back-pressure needed.
- [rename O+ -> O-, 2026-08-25] Full rename: binary o-, command o-, manifest o-.yaml, cache ~/.cache/o-, module github.com/amritrai/o-, CI/release artifacts. v0.1.1 released. War story: a sed footgun (o\+ = "one or more o" in BRE) mangled every 'o' run; recovered via git checkout, redone with literal-sed, all verified. Historical docs keep the o+ name as the record of that era.
- [o- bundle epic, 2026-08-25] War room: Architect "Static Embed" won (Security APPROVE-WITH-CONDITIONS / Builder REJECT; Performance preferred Architect 4-5x recompile). Conditions folded in: mandatory secret excludes (.env, *.pem, *.key, *.p12, *.pfx, secrets/), filename sanitization (reject, don't mangle), symlink-escape + glob-traversal guards, size cap (50MB default), asset extensions added to default watcher set. IMPLEMENTATION DEVIATION (ledgered like the cache-key fix): generated file at PROJECT ROOT, not .o-/embed/ — go:embed patterns cannot contain ".." and embed ignores symlinks (the winning draft's symlink trick would have silently embedded nothing); root placement also feeds sourceHash + watcher for free. Verified: unit + integration green on Go 1.24/1.26; e2e (build binary serves embedded asset); live smoke (o- bundle --dry-run, exclude works, asset edit → rebundle → 208ms reload → new content served). v0.2.0 release.
- [benchmark, 2026-08-25] benchmarks/bench.py runs identical workflows in o- vs bun (scaffold, cold start, hot reload, build, artifact size, test); results JSON in benchmarks/results/. First run: o- wins scaffold (0.01s vs 1.04s) and artifact (5.5MB vs 94.6MB); bun wins cold start (0.05s vs 5.89s cold-cache) and hot reload (0.00s vs 0.36s) — the compile-step physics, documented since v0.1. `make bench`.
- [polyglot pivot, 2026-08-25] User proposed making o- work with Java and/or Ruby on Rails ("Java is fast, Rails is not"). CEO REJECTED and ledgered, same shape as the C++ ruling: (1) Simplicity #4 — each language means a second product (Maven vs Gradle, JUnit, JVM flags; Rake, Asset Pipeline, gems); (2) the one-binary promise breaks (JVM needs a JRE, Rails needs Ruby+gems — no 5MB static artifact); (3) the speed logic inverts — o- cannot make javac faster and JVM startup is slower than Go's; (4) identity — Bun is one tool one ecosystem, o- is "Bun for Go." o- stays Go-only. A Java/Rails analogue is a possible SIBLING project (same philosophy), not an o- feature; CEO will frame it as its own epic only if the user asks.
- [Rails runtime pivot, 2026-08-25] User proposed "o- is a Ruby on Rails runtime making it faster." CEO REJECTED and ledgered: (1) a fast Ruby runtime = writing a Ruby VM = TruffleRuby-scale (Oracle Labs, PhD team, years) — out-competing MRI is the same trap as out-competing V8; (2) Rails' slowness is the interpreter, not tooling — no CLI wrapper makes `ruby` faster; (3) identity — o- is "Bun for Go," a shipped tested product; pivoting it deletes what is real for what is not buildable; (4) the real answers already exist: TruffleRuby (3-10x Ruby speed) and Rails' own Spring reload. CEO noted the pattern: three moonshot pivots in one day (C++ rewrite, Java/Rails polyglot, Rails runtime), all rejected on the same values — ship quality or nothing.
- [ECOSYSTEM SELECTION — definitive, 2026-08-25] User asked the CEO to formally pick which ecosystem o- plugs into. RULING: GO — decided at the original interpretation ruling (2026-08-25) and defended by every rejection since. Reasoning: (1) Go's physics are o-'s two structural wins — the 5.5MB static binary and the single boring toolchain (go build/test/embed, one vendor); (2) Safety #2 — a process supervisor running untrusted hooks needs Go's memory safety; (3) the user's own ecosystem is Go (Spine is Go) — o- plugs into where the user lives; (4) alternatives rejected on the record: JS/TS (Bun's home turf), Java (no single binary, JVM startup), Ruby (no binary story), Rust (same compile physics, more friction — sibling tool, not replacement). NOT OPEN FOR REVISION. What IS open: how deep o- goes in Go — background compiler (cold-start gap), incremental sourceHash, pollLoop back-pressure, o- install, shell completions.
