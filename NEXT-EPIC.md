# NEXT-EPIC.md — CEO Loop Log, 2026-08-25 (2)

## STATUS REPORT (rename epic, from verification evidence)

- Renamed O+ -> O- end-to-end: binary (bin/o-), command (o- run/build/test/new), manifest (o-.yaml), cache dir (~/.cache/o-), module (github.com/amritrai/o-), watcher excludes, templates, CI/release artifacts.
- Verified: vet + tests green on Go 1.24.4 and 1.26.7 (race + integration); live smoke (scaffold o-.yaml, reload, graceful stop); CI green on 9183de1; release v0.1.1 with o- + o-.sha256 assets (verified via API).
- Ledger note: a sed footgun (o\+ quantifier) mangled every 'o' run; recovered via git checkout and redone with literal-sed. Historical docs keep the o+ name as the record of that era.

## CEO RULING — NEXT EPIC: "o- bundle"

- WHAT: `o- bundle` — the one-command asset story for single-binary Go apps. Declared assets (o-.yaml: `bundle.include` globs) become embedded in the binary at build time, so a developer ships one static binary with templates/images/config — no hand-written go:embed directives per file, no external files at runtime. Exact mechanism (generate a go:embed source file vs append a zip payload vs build-tag asset dir) is the war room's HOW decision.
- WHY: v0.1 deferred bundle because scope was unclear; it is now the single biggest DX gap vs "feels like Bun" — Bun bundles your assets for you, Go makes you wire go:embed by hand. Ease of use (#3) is the dominant value for this epic.
- VALUES: Ease of use (3) dominant; Simplicity (4) constrains scope (no plugin system, no remote asset fetching, no WASM); Quality (1) means the embedded asset set is hash-verified and deterministic.
- REJECTED for this epic: remote/template registries, asset minification, WASM, plugin loaders, anything beyond "assets in, static binary out".
- SUCCESS CRITERIA: `o- bundle` turns o-.yaml-declared assets into a working embedded build (served/readable at runtime); deterministic output (same inputs -> same binary); tests + CI green; documented in README.

Status: COMPLETED 2026-08-25. All success criteria met: o- bundle turns o-.yaml-declared assets into a working embedded build (e2e + live smoke verified: binary serves embedded asset, asset edit hot-reloads in 208ms); deterministic output (sorted walk, content-only hash, byte-identical regeneration); tests + CI green on Go 1.24/1.26; README documented. v0.2.0 released with checksums. Next epic decision belongs to the CEO loop (cron, daily 09:00; gateway must be running).
