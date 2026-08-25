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
