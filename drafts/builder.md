# O+ v0.1 — Builder Draft

**Persona:** Pragmatic Builder
**Approach:** "Ship It" — Minimal Viable Toolchain
**Bias:** Working code > perfect code. Veto abstractions without a second concrete use case. Call out Architect over-engineering.

---

## Core Philosophy

Go's stdlib, `go build`, `go test`, and `go run` are already production-grade. O+ v0.1 wraps them with UX polish, file watching, and a single-binary distribution — NOTHING replaces or reimplements what Go ships. Every layer of abstraction must earn its existence with a second concrete use case or it gets flattened.

```
      ┌──────────────────────────────────────────────────┐
      │                o+ (static binary)                 │
      ├──────────────────────────────────────────────────┤
      │  run    │  build    │  test    │  new    │ (bundle deferred) │
      ├─────────┴──────────┴──────────┴─────────┴──────────────────┤
      │              os/exec → go tooling (stdlib)                  │
      │              fsnotify · template/embed                      │
      └──────────────────────────────────────────────────────────────┘
```

No plugin system. No abstraction layer over `os/exec`. No config file format wars. The manifest is `o+.json` (or `o+.yaml`) — one file, boring keys, zero magic.

---

## v0.1 Feature Set — Minimal Viable

### `o+ run` — Run + Hot Reload

**Mechanism:** `fsnotify` library watches the project directory tree. On any `.go` file change, it sends SIGTERM to the running child process, runs `go build -o /tmp/o+-cache-<hash> .`, then execs the binary. Stdout/stderr pass through directly.

**What shipping looks like:**
- File watcher debounces at 200ms to avoid cascading rebuilds
- Child process is killed with SIGTERM, waits 3s, then SIGKILL (stuck process safety)
- Exit code forwarding: `o+ run` exits with the child's exit code
- `.o+ignore` file for excluding vendor/, node_modules/, .git/ (defaults hardcoded)

**What is cut:**
- No file-level rebuild dependency graph (Go's compiler handles that — we don't build one)
- No live-reload browser injection (that's a framework feature, not O+)
- No multi-process watch (single process only for v0.1)

### `o+ build` — Static Binary Compilation

**Mechanism:** `go build -ldflags="-s -w" -o <name> .` with progress spinner. Defaults to current directory, output name derived from module name.

**What shipping looks like:**
- `-o` flag for explicit output path
- `-osarch` flag for cross-compilation (e.g. `o+ build -osarch linux/arm64`)
- Prints binary size and wall-clock time on completion
- Writes to `./dist/` by default (gitignored)

**What is cut:**
- No custom linker script or LTO pass
- No build cache visualization
- No CI artifact upload

### `o+ test` — UX Wrapper Over `go test`

**Mechanism:** `go test -v -count=1 ./...` with colorized output, pass/fail summary, and optional `--watch` flag.

**What shipping looks for v0.1:**
- Colorized TAP-style output (green PASS, red FAIL, yellow SKIP)
- `--watch` flag: re-run tests on file change (same fsnotify as `run`)
- `--coverage` flag: runs `go test -coverprofile` and prints a terminal-friendly coverage table
- Exit code preservation for CI

**What is cut:**
- No test filtering DSL beyond `-run` passthrough
- No flaky-test detection or quarantine
- No parallel test dashboard

### `o+ new` — Zero-Config Scaffold

**Mechanism:** `embed.FS` contains 3-5 project templates (minimal, web-server, CLI). Copy template directory to target path, run `go mod init <module-name>`.

**What shipping looks like:**
- `o+ new myapp` — scaffold from default template
- `o+ new myapp --template web` — named template
- Templates are boring: `main.go`, `o+.json`, `go.mod` stub, `README.md`
- Idempotent: refuses to overwrite existing directory unless `--force` passed

**What is cut:**
- No template registry or remote template fetching
- No interactive prompts beyond simple parameter substitution
- No git init (user runs `git init` themselves)

### `o+ bundle` — DEFERRED TO v0.2

**Reason:** Asset embedding is already served by `go:embed` directives. A dedicated `o+ bundle` command adds no value without a second use case beyond "embed static files." In v0.2, if we see a concrete need (e.g. bundling all assets into a single distributable without file-tree mapping, or vendoring Wasm modules), we add it then. For v0.1, users write `//go:embed` directly.

---

## What I Would Cut from v0.1 (and Why)

| Feature | To Cut? | Why |
|---------|---------|-----|
| `o+ bundle` | YES | `go:embed` works. No second use case yet. |
| `o+ test --watch` | YES | Nice-to-have. Ship `o+ test` without watch first; watch is 50 lines of fsnotify glue. |
| `o+ run --production` | YES | Premature optimization. v0.1 run is dev-only. |
| Plugin system | YES | Zero concrete use cases. Hypothetical abstraction. |
| Config file validation schema | YES | One manifest file doesn't need a schema validator. Parse or error. |
| Daemon mode / background server | YES | Adds PID-file, signal, log management complexity for zero v0.1 benefit. |

---

## Fastest Path to a Working Demo

1. **Day 1:** `o+ build` — shell out to `go build`, print result. ~50 lines.
2. **Day 1:** `o+ test` — shell out to `go test -v`, colorize output. ~80 lines.
3. **Day 2:** `o+ new` — `embed.FS` template, copy + `go mod init`. ~100 lines.
4. **Day 3:** `o+ run` — fsnotify watcher + process management. ~200 lines.
5. **Day 4:** Static binary assembly, flag parsing, help text, exit codes polished.
6. **Day 5:** CI + smoke tests + README. Ship.

**Total: ~500 lines of Go.** One `go generate` step for `embed.FS` templates. One `go build` for the final binary. No external dependencies beyond `fsnotify` (well-maintained, 1.5k stars, no CVEs in 3 years).

---

## Risks the Security Reviewer Would Attack

1. **Process isolation in `o+ run`:** The child process inherits O+'s signal handlers, environment, and file descriptors. A malicious `go test` or `go run` invocation in the child could read O+'s memory or environment. **Mitigation:** Set `SysProcAttr` with separate process group, clear sensitive env vars before exec, run child in a new PID namespace on Linux where available.

2. **`o+ new` destructive behavior:** If `--force` is used, or if the target path is a system directory (e.g. `/etc`, `/usr`), `os.RemoveAll` could be destructive. **Mitigation:** Path-safety check rejects target paths outside the user's home directory or `/tmp` unless `--force` is passed twice. Also, validate the target is either empty or doesn't exist before writing.

3. **Race condition in file watcher:** fsnotify fires on partial writes. If the watcher triggers `go build` while a file is still being written, the compilation fails with a confusing error, and the user's running process gets killed and restarted unnecessarily. **Mitigation:** Debounce + inotify `IN_CLOSE_WRITE` awareness (wait for write to finish). Also, skip the first build if no binary was running yet.

4. **Supply chain of the static binary:** If someone ships a tampered `o+` binary (e.g. via `curl | sh`), it has full access to the user's source code. **Mitigation:** Ship checksums + GitHub Attestations. Never pipe to shell. Self-update verifies signatures.

---

## Risks the Performance Skeptic Would Attack

1. **`go build` is slow for large projects:** `o+ run` calls `go build` on every file change. For a 100k+ LOC monorepo, `go build` takes 5-15s. A 200ms debounce means every keystroke triggers a 5-15s rebuild. **Mitigation:** Use `go build -i` (cache) and eventually increment with `go build -o /dev/null` to check compilation before killing the running process. For v0.1, document that `o+ run` is for small-to-medium projects; large projects use `o+ build` + manual restart.

2. **fsnotify overhead on large directory trees:** Watching a monorepo with 10k+ files creates kernel-inotify-pressure (`fs.inotify.max_user_watches`). **Mitigation:** Default ignore list excludes `.git/`, `vendor/`, `node_modules/`, `dist/`. Document `fs.inotify.max_user_watches` tuning. In v0.1.1, add recursive watch with depth limit.

3. **Exec overhead per command:** Each `o+` subcommand forks a `go` process. On cold cache, `go build` + exec adds ~200ms of process-spawn overhead. **Mitigation:** Negligible for v0.1. Document that `go` itself is the bottleneck, not the wrapper. In v0.2, explore `go build -toolexec` for caching.

4. **Static binary size with embedded templates:** `embed.FS` with 5 templates adds ~50KB to the binary. Not a real problem, but the skeptic will ask. **Mitigation:** Use `embed.FS` with compressed templates. If size becomes an issue, lazy-load from a separate `o+` data directory.

---

## What the Architect Would Propose (and Why I Veto It)

| Architect Proposal | Why Veto |
|-------------------|----------|
| Abstract `Runner` interface with multiple backends (fsnotify, polling, kqueue, Windows) | **One concrete use case (fsnotify on Linux).** Add polling fallback when someone reports a Docker-on-macOS bug. Until then, it's YAGNI. |
| Plugin system for custom build steps | **Zero concrete use cases.** The first user who needs a custom build step should write a Makefile or a shell script. O+ is not a build system. |
| Configuration schema validation with JSON Schema | **One manifest format, three keys.** Parse it with `encoding/json`, error on unknown fields. A schema validator adds 5MB of dependency for 0 benefit. |
| gRPC management API for the running process | **What would consume it?** A future IDE integration? Ship it when the IDE exists. |
| Custom test runner replacing `go test` | **`go test` is already excellent.** Wrap it, don't replace it. A custom test runner would need to match `go test`'s caching, coverage, vetting, and fuzzing — that's a multi-year project with zero incremental value. |

---

## Project Structure (v0.1)

```
o+/
├── main.go                 # Entry point, flag dispatch
├── cmd/
│   ├── run.go              # o+ run — fsnotify + process mgmt
│   ├── build.go            # o+ build — go build wrapper
│   ├── test.go             # o+ test — go test wrapper
│   └── new.go              # o+ new — template scaffold
├── internal/
│   ├── watcher/            # fsnotify wrapper (thin, no abstraction yet)
│   ├── process/            # Child process management (SIGTERM, wait, reap)
│   └── manifest/           # o+.json parsing
├── templates/              # embed.FS templates
│   ├── minimal/
│   ├── web-server/
│   └── cli/
├── o+.json                 # Manifest schema (this file is the spec)
├── go.mod
├── go.sum
└── README.md
```

---

## Manifest Schema (`o+.json`)

```json
{
  "name": "myapp",
  "version": "0.1.0",
  "run": {
    "watch_extensions": [".go", ".mod", ".sum"],
    "ignore_dirs": [".git", "vendor", "node_modules"],
    "build_args": ["-tags", "dev"]
  },
  "build": {
    "output": "./dist/myapp",
    "ldflags": "-s -w",
    "targets": [
      {"os": "linux", "arch": "amd64"},
      {"os": "darwin", "arch": "arm64"}
    ]
  }
}
```

Three top-level keys. No transforms. No computed defaults. `run` and `build` are optional — if absent, sensible defaults apply. The schema is documented in the README, not validated by a schema compiler.

---

## Conclusion

O+ v0.1 should ship in one week, ~500 lines of Go, 3-5 working subcommands, one static binary. The fastest path to "feels like Bun for Go" is to wrap Go's stdlib, not rewrite it. Every abstraction that doesn't serve a second concrete use case is deleted. The Architect's impulse to build a platform before a product is recognized and rejected. Ship working code, collect feedback, iterate.

**"Working code > perfect code. Ship it."**