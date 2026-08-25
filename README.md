# o- — a Bun-class developer toolchain for Go

One static binary. Run with hot reload, build, test, scaffold. Boring proven
internals, zero plugins, no telemetry. Designed by the Spine war room; the
CEO's rules are permanent: **ship quality or nothing — safety and quality
first.**

## Commands

    o- run          run with hot reload (build-before-kill: a broken save
                    never kills your running app)
    o- build        static binary out (thin sha256-verified cache over $GOCACHE)
    o- test         friendly UI over `go test -json`, --watch mode
    o- new          scaffold (templates: minimal, web-server, cli)

## Quickstart

    o- new myapp --template web-server
    cd myapp
    o- run          # edit code, watch it restart in ~250ms
    o- build        # ./dist/myapp

## Manifest (o-.yaml)

Optional — zero config works. Strict YAML: unknown keys error, anchors/aliases
forbidden, 1MB cap, depth limit 64. See internal/scaffold templates for the
shape. `run.pre_run` hooks execute via direct exec (never a shell) and require
`o- run --trust` on first use; the project fingerprint (sha256 of o-.yaml +
go.mod + go.sum) is stored in ~/.cache/o-/trust.json.

## Safety model

- Build artifacts and trust store live in $XDG_CACHE_HOME/o- (mode 0700),
  never /tmp.
- Every cached binary is sha256-verified immediately before exec.
- Child processes get a fresh process group, sanitized env, no inherited FDs.
- `o- new` refuses to write outside home or /tmp unless --force.
- No curl|sh installs. Ship via releases with checksums.

## Known items (war-room ledger)

- v0.1 is Linux-first; macOS/Windows deferred (signals/process groups differ).
- `o- bundle` deferred to v0.2 (`go:embed` already covers asset embedding).
- Dependency advisory GO-2026-5024 (x/sys < 0.44.0, integer overflow in
  x/sys/windows) does not affect Linux-only builds — the windows subpackage is
  not compiled for linux targets. HARD TRIGGER: bump x/sys >= 0.44.0 (and the
  go toolchain) the moment Windows targets are added.
- Hot-reload latency scales with project size (Go compiles); build-in-background
  hides it, it cannot be eliminated. Large monorepos: use `o- build` + restart.

## Development

    make build      # -> bin/o-
    make test
    make vet
