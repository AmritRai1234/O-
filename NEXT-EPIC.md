# NEXT-EPIC.md — CEO Loop Log, 2026-08-25

## STATUS REPORT (v0.1, from verification evidence)

- Shipped: commit e32e8df, ~1,700 LOC Go, 4 commands (run/build/test/new)
- Verified:
  - go build OK, go vet OK
  - smoke: o+ new -> o+ build (5.3 MiB in 2.3s) -> o+ test (exit 0)
  - hot reload: edit served as v2 in 261ms restart
  - graceful shutdown: SIGINT stops child group, port freed, zero strays
  - trust gate: refused without --trust (fingerprint shown), recorded with --trust, trusted on re-run
- Ledger additions: GO-2026-5024 accepted with hard trigger (Windows), cache-key correctness fix, scaffold path safety
- OPEN GAP: zero tests for o+ itself. No CI. No checksummed releases.

## CEO RULING — NEXT EPIC: "Trustworthy O+"

- WHAT: (1) a test suite for the security-critical paths — manifest bomb/alias/depth guards, cache sha256 verification, trust gate, watcher exclusions + debounce, runner process-group stop, scaffold path safety; (2) CI on GitHub Actions: build + vet + test + govulncheck on every push/PR; (3) release artifact with .sha256 checksums.
- WHY: Quality #1 is "correct, tested, no known-broken ships." A toolchain that wraps `go test` but has no tests of its own is a gap, not a finish. Safety #2: distribution must be verifiable — no curl|sh.
- VALUES: Quality (1) and Safety (2) dominate; Simplicity (4) constrains scope.
- REJECTED for this epic: o+ bundle (stays v0.2), macOS/Windows, plugins, telemetry, daemon mode.
- SUCCESS CRITERIA: `go test ./...` green with the security paths covered; CI green on main; release binary + .sha256 published.

Status: ruling issued; war room may draft the HOW on user go-ahead.
