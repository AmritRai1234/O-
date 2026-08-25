# O+ — CEO Epic Brief & Direction Ruling

Ruled by: CEO (Spine team personas) · 2026-08-25 · Venue: war room, project folder /home/amritrai/spine/O+

## User Directive (verbatim intent)

- New project in this folder: **O+**
- Language: **Go**
- Goal: build a **"compadiro" (competitor) to Bun**
- Hard constraint, user's own words: **"we will rather ship quality or nothing — safety and quality."**
- Project direction: **delegated to the CEO.** The team argues HOW; the CEO decides WHAT.

## Interpretation Ruling (CEO)

"Competitor to Bun" is read as: **a Bun-class developer toolchain, written in Go.**

Bun's essence is not "a JS engine" — it is the one-binary developer experience: run, watch/reload, bundle, test, zero config, fast. O+ is that experience for Go developers, shipped as one static Go binary:

- `o+ run` — run a Go app with instant hot reload on change (no external deps like air/reflex)
- `o+ build` — fast incremental production build (static binary out)
- `o+ test` — Bun-grade test runner UX on top of Go's native test tooling
- `o+ bundle` — asset embedding / vendoring ergonomics (exact scope: war room decides)
- `o+ new` — zero-config project scaffold (one manifest, boring defaults)

**Explicitly REJECTED — "port Bun to Go":** a JS/TypeScript engine written in Go. That violates Quality #1 (no Go JS engine approaches V8/JSC correctness or speed — we would ship a slow, subtly-wrong runtime) and Safety #2 (running untrusted JS on a young engine is a footgun). If we ever need to host JS, we embed a battle-tested engine (e.g. goja) behind a thin interface — that is a war-room decision, not mine.

## Which Values Dominate (user's explicit ordering)

1. **Quality** — the product IS "ship quality or nothing". No known-broken ships. Tests mandatory. A bug here ruins a developer's day.
2. **Safety** — dev tools run user code and touch user files: process isolation for `run`, no destructive fs operations, supply-chain-clean distribution (static binary, checksums, no curl|sh).
3. **Ease of use** — zero config, one manifest, feels like Bun feels.
4. **Simplicity** — one binary, boring proven internals, every feature earns its complexity.
5. **Performance** — for a Bun competitor this floor is set HIGH (must feel instant next to Node/tsc-style workflows) but it stays a floor: never traded against 1–4.

## What O+ is NOT (this epic)

- Not a JS engine, not "Bun rewritten in Go"
- Not an npm-compatible package manager
- Not a web framework — Spine remains the framework; O+ is tooling
- Not a GUI

## Brief for the War Room (Architect + Builder)

Draft COMPETING approaches for O+ v0.1 scope: run+reload, build, test, scaffold. Decide HOW within this brief. Security + Performance tear into both drafts. Grumpy Principal arbitrates if needed; strategy-level stalemates escalate back to me.

## Disagreement Ledger

(empty at ruling time — will be appended as the war room runs)
