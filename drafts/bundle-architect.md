# O- v0.2 Bundle Epic — Staff Architect Draft

**Persona:** Staff Architect
**Approach name:** "Static Embed"
**Date:** 2026-08-25
**Status:** Draft for Security + Performance review (war room round 1)
**Bias:** Prefer boring, proven patterns over novel ones. Flag hidden coupling. Will this hurt us in 6 months?

---

## Executive Summary

`o- bundle` makes declaration-based asset embedding (o-.yaml `bundle.include` globs) produce a working single-binary Go app. This draft chooses **go:embed generated source (go:generate-style)** — the same mechanism Go already uses for protobufs, stringer, and its own internal tooling. A code generator produces Go source files with `//go:embed` directives in a hidden build directory (`.o-/embed/`); `go build` picks them up naturally. No binary post-processing, no runtime archive readers, no platform-specific payload tricks. This approach inherits all of Go's battle-tested embed machinery, costs zero new runtime dependencies, and keeps the generated code out of source control by construction.

The alternative I reject: **append-zip payload**. Appending a zip/tar archive to the compiled ELF/Mach-O binary is clever on the whiteboard but creates three classes of bugs in 6 months: platform-specific parsing (ELF section headers, Mach-O fat binaries, PE COFF for Windows), position-dependent runtime loader (breaks with `go build -buildmode=pie`, stripped binaries, `upx` compression), and a bespoke runtime reader that must handle page alignment, endianness, and appended-data detection across three OSes. That is exactly the kind of clever-but-fragile design the war room exists to veto. "Boring Bazaar" precedent applies: use what Go already gives us.

---

## 1. The Embedding Mechanism: go:embed Generated Source

### Choice: Code generation into `.o-/embed/` — NO binary post-processing

**How it works:**

```
User writes o-.yaml:
  bundle:
    include: ["templates/**/*", "config/*.yaml"]

User runs `o- bundle` (or `o- build` triggers it):
  1. Resolve globs against project root
  2. Sort matched files by canonical relative path (deterministic)
  3. Compute sha256 of all matched content -> hash_H
  4. Write .o-/embed/bundle.go:
       //go:generate o- bundle (marker)
       package embedgen
       import "embed"
       //go:embed assets/*
       var BundleFS embed.FS
       const ManifestHash = "hash_H"
  5. Write assets/* symlinks (or copies) under .o-/embed/ for go:embed to find
  6. Write .o-/embed/.hash (current bundle hash for staleness check)

go build sees .o-/embed/bundle.go + .o-/embed/assets/ naturally.
Internal/bundle package imports embedgen.BundleFS for runtime access.
```

**Why this wins (and the other two lose):**

| Concern | go:embed generated (win) | Append-zip payload (loss) | Build-tag asset dir (loss) |
|---|---|---|---|
| Runtime dependency | stdlib embed | Custom reader + platform ELF/Mach-O/PE parser | Third-party tool (bindata, etc.) |
| Platform support | Linux/Mac/Win — same code | Different loader per OS. Breaks on stripped/compressed binaries | Tool-specific. Most unmaintained |
| Determinism | Sorted file walk + fixed template | Depends on zip timestamp/compression settings | Tool-dependent |
| Build speed | go:embed is a compile-time copy. Fast. | Append step after go build — sequential, cannot cache | Third-party generate step |
| Cache interaction | Generated .go files participate in Go cache naturally | Binary is built, then patched — cache sees the unpatched binary | Generated .go files participate |
| Debuggability | `//go:embed` dir is on disk — inspectable | Embedded blob is opaque | Tool-generated, tool-specific |
| 6-month worry | Generated .go size scales with assets. See §Risk B | ELF parsing breakage on new Go/compiler versions. PIE-related segfaults. | Tool abandoned; re-generator uses unknown format |

**Verdict:** Append-zip payload is clever but fragile — it hurts us in 6 months when we ship macOS ARM + Windows PE support and the binary payload reader needs three backends. Rejected. Build-tag asset dir depends on a third-party tool (`go-bindata`, `statik`, `packr`) — every one of these is either archived, unmaintained, or has a Go embed compatibility shim that asks "why not just use go:embed." Rejected. go:embed generated source is the boring, proven path.

### Tradeoff accepted: Generated file size

`go:embed` compiles asset bytes into the binary as a large byte array. For a 10MB asset folder, the generated `.o-/embed/assets/` directory uses 10MB on disk (as hardlinks or symlinks) and the binary grows by ~10MB. This is not a codegen artifact we can avoid — embed must read the bytes. The alternative (zip payload) would also grow the binary by ~10MB, just not visible in `go tool nm`. Acceptable: this is what every Go binary with embed does.

---

## 2. Manifest Schema for `bundle.include`

### YAML structure (extension of existing Manifest struct)

```yaml
# o-.yaml
bundle:
  # Required: glob patterns for files to embed.
  # ALL matched files are embedded. Patterns are relative to the project root.
  # Default: none (no bundling).
  include:
    - "templates/**/*"
    - "config/*.yaml"
    # Pattern with destination remapping (optional)
    - pattern: "vendor/module/**/*.wasm"
      as: "wasm-modules/"
    # Pattern with exclusion
    - pattern: "assets/**/*"
      exclude:
        - "assets/raw/*.psd"
        - "assets/raw/*.ai"

  # Optional: global exclude patterns (applied after per-pattern excludes)
  exclude:
    - "**/*.go"        # Go source is not bundled by default
    - "**/testdata/**"
    - "**/.git/**"

  # Collision policy when two patterns map to the same output path
  collision: "error"    # "error" | "first_wins" | "last_wins"

  # Optional: path prefix stripping
  # If set, this prefix is stripped from every asset's embedded path.
  strip_prefix: "assets/"

  # Optional: maximum total asset size (default: 50MB)
  max_size: "50MB"
```

### Go struct

```go
type BundlePattern struct {
    Pattern      string   `yaml:"pattern"`     // glob, required when as is set
    As           string   `yaml:"as"`           // remap destination dir (optional)
    Exclude      []string `yaml:"exclude"`      // per-pattern excludes (optional)
}

type Bundle struct {
    Include     []BundlePatternOrString `yaml:"include"`     // string or {pattern, as, exclude}
    Exclude     []string               `yaml:"exclude"`      // global excludes
    Collision   string                 `yaml:"collision"`    // error (default), first_wins, last_wins
    StripPrefix string                 `yaml:"strip_prefix"` // optional prefix to strip
    MaxSize     string                 `yaml:"max_size"`     // "50MB" (default), or ""
}
```

### Pattern resolution rules

1. A bare string in `include` is equivalent to `{pattern: <string>, as: ""}` — files matched by the glob are embedded at their relative-to-root path.
2. `pattern + as` remaps: if `pattern` matches `assets/images/logo.png` and `as` is `img/`, the embedded path becomes `img/logo.png`.
3. Glob syntax: standard `path/filepath.Match` extended with `**` for recursive matching (using `gobwas/glob` or stdlib `filepath.Glob` + manual walk). `**` matches zero or more directory levels.
4. Exclude patterns take precedence over include patterns — a file matched by both an include glob AND an exclude pattern is excluded.
5. File list is built by walking from project root. Only regular files (not symlinks to outside the project) are included. Symlinks resolved to an absolute path inside the project root are dereferenced to their target (not stored as symlinks).

### Collision policy

- **error** (default): if two patterns produce the same embedded path, fail loudly with a list of conflicting sources. This is the safe default — silent collisions cause "my config was overwritten" bugs that are hell to debug.
- **first_wins**: earlier include pattern wins. Later matches are silently dropped.
- **last_wins**: later include pattern wins. Earlier matches are overwritten.

Rationale for NOT having "rename" in the collision policy: renaming is a per-pattern operation (`as` field). Global rename semantics are confusing. If you need to deduplicate, use `exclude` + precise patterns.

---

## 3. Determinism (Same Inputs -> Same Binary)

This is a Quality #1 requirement from the CEO's success criteria.

**Determinism guarantees:**

1. **Sorted file walk** — `filepath.WalkDir` returns entries in lexical order (Go 1.23+ guarantees deterministic directory reads). Each file's relative path is canonicalized (forward slashes, no `./` prefix, no `//`).

2. **Content-only hash** — The bundle manifest hash (`ManifestHash` constant in generated code) is computed from `sha256(sorted(relativePath + null + fileContent))`. Timestamps, file permissions, and directory modification times are excluded. Only file content and relative path matter.

3. **Ordered generation** — The generated `bundle.go` file is produced from a fixed template. No timestamps in comments, no random identifiers, no build host variables. The generation timestamp is NOT included — it would break determinism.

4. **Asset directory layout** — Files under `.o-/embed/assets/` are symlinks (or hardlinks, same inode content) named by their content hash, not their original filename. The `go:embed` directive references the symlink farm, which is constructed in sorted order. Since the symlinks are content-addressed, identical content across builds produces identical symlink targets — zero change to the generated directory.

5. **Binary reproducibility** — Go's compiler does NOT guarantee reproducible binaries by default (`-trimpath` helps but build IDs embed timestamps). However, since `o- build` uses Go's cache, and the cache key (`projectKey`) includes the source tree hash, the build ID is deterministic for identical inputs. The `go build` `-trimpath` flag is automatically set in the builder to eliminate absolute path leakage.

**What breaks determinism:**
- File content changes (expected — triggers rebuild)
- Glob pattern changes (expected — triggers regeneration)
- Go compiler version changes (expected — Go cache invalidates)

**What does NOT break determinism:**
- File modification timestamps (excluded from hash)
- Directory order on filesystem (Go's WalkDir is sorted)
- Order of `include` patterns (files are sorted by output path before hashing)
- Machine hostname, username, or build directory (`-trimpath`)

---

## 4. How `o- run` and `o- build` Interact with Bundled Assets

### `o- build` (primary path)

```go
func build(output string) error {
    m := loadManifest()
    b := builder.New(dir, m)

    // NEW: Bundle step — part of the build pipeline
    if len(m.Bundle.Include) > 0 {
        if err := b.EnsureBundle(m.Bundle); err != nil {
            return fmt.Errorf("bundle: %w", err)
        }
    }
    // Bundle writes .o-/embed/bundle.go + assets/
    // Then go build sees them naturally as part of the package

    bin, err := b.Build()  // go build picks up .o-/embed/ automatically
    // ... copy to output ...
}
```

`EnsureBundle` logic:
1. Check if `.o-/embed/.hash` exists and matches `sha256(current_bundle_config + current_asset_contents)`
2. If match: skip (generated files are current). Return immediately.
3. If mismatch or absent: regenerate. Walk project, resolve globs, compute hash, write bundle.go, symlink assets, write .hash.
4. Cost: O(N) walk on first run or after any asset change. Cached after that.

### `o- run` (hot-reload path)

The same `EnsureBundle` call happens inside the Builder during `b.Build()`. Since `sourceHash` in the builder already walks the project tree, the bundle check piggybacks on that walk at negligible extra cost. If an asset file changes (e.g. a template edit), the generated bundle.go's content changes → Go cache sees a new file digest → incremental recompile triggers. The user does NOT need to run `o- bundle` explicitly — `o- run` handles it transparently.

### `o- bundle` (standalone command)

A new CLI command `o- bundle` that explicitly regenerates the embedded files and then runs `go build`. Usage: `o- bundle` (regenerate + build), `o- bundle --dry-run` (print what would be embedded without writing), `o- bundle --verify` (check that embedded assets match the expected hash). This is the explicit "I want to update my assets" command, analogous to `go generate ./...`.

### Interaction with the watcher (`o- run` event loop)

The watcher's excluded patterns already include `.o-/` (from sourceHash's `excludedDirs`). Asset files in the user's source tree (e.g. `templates/index.html`) ARE watched — they trigger a file change event, which triggers a rebuild, which calls `EnsureBundle`, which sees the template changed, regenerates bundle.go, and Go compiles it. This is correct behavior: editing a template hot-reloads the app with the new template embedded.

The watcher does NOT watch `.o-/embed/` — no recursive event loops from generated code changes.

---

## 5. Generated Code: Out of Source Control, Out of Source Hash / Watcher

### Location: `.o-/embed/`

The `.o-/` directory is already:
- In `excludedDirs` in `sourceHash()` (`".o-"` is a key in the map)
- Add to the watcher's exclusions in `run.go`
- Implicitly gitignored (`.o-/` should be in `.gitignore` or added as part of `o- new` scaffold)

### Why `.o-/embed/` and not `.o-/bundle/`

Consistency: `.o-` is the O- build artifact directory (already established convention in v0.1 for cache). Putting generated Go source there alongside cached binaries is conceptually wrong — cache is disposable, but generated source is ephemeral in a different sense (regeneratable, but required for compilation). I keep it separate as `.o-/embed/` so that `rm -rf .o-/` still clears everything.

### Gitignore

```
# O- build artifacts
.o-/
```

This should be part of the `o- new` scaffold template output.

### Source hash exclusion

`sourceHash()` already skips `.o-`. However, asset files in the user's project (e.g. `templates/`) ARE included in the source hash — they are part of the build-relevant surface. The generated `.o-/embed/` files are excluded because they are derived, not source.

### Cache key consideration

The builder's `projectKey()` includes `sourceHash()` which walks the project tree. Since asset files (`.html`, `.yaml`, `.json`) are already in the source hash's extension list (present in `case ".html", ".yaml", ".yml", ".json", ".tmpl"`), asset changes automatically change the cache key. No special handling needed — the existing cache invalidation works for assets.

**Addition needed:** The bundle generation ALSO writes `.o-/embed/bundle.go` with a `ManifestHash` constant. This hash must be factored into the build cache key. The builder's `projectKey` currently hashes source files + manifest fingerprint. Since `.o-/embed/bundle.go` is excluded from sourceHash (`.o-` is excluded), we need to explicitly include the bundle manifest hash if `bundle.Include` is non-empty:

```go
func (b *Builder) projectKey() (string, error) {
    fp, err := manifest.Fingerprint(b.Dir)
    // ... existing ...
    h.Write([]byte(fp))
    h.Write([]byte{0})
    h.Write([]byte(sh))
    // NEW: if bundle is defined, include the bundle hash
    if len(b.Manifest.Bundle.Include) > 0 {
        bh, err := bundleHash(b.Dir)
        if err != nil { return "", err }
        h.Write([]byte{0})
        h.Write([]byte(bh))
    }
    return hex.EncodeToString(h.Sum(nil)), nil
}
```

---

## 6. Verification

### Build-time verification

The generated `.o-/embed/bundle.go` contains:

```go
package embedgen

import "embed"

//go:embed assets/*
var BundleFS embed.FS

// ManifestHash is sha256 of all bundled file paths + contents in sorted order.
const ManifestHash = "abc123..."
```

The builder verifies that `ManifestHash` matches the current asset state before trusting the cache. This is done inside `EnsureBundle` — if the hash matches, skip generation; if it mismatches, regenerate.

### Runtime verification

`o- bundle --verify` (or any command with `--verify-bundle` flag) reads `ManifestHash` from the compiled binary via `embedgen.ManifestHash` and compares it to a fresh hash of the project's current assets. If mismatch: "bundled assets are stale — run `o- bundle` to regenerate." This catches the case where a developer forgot to run `o- bundle` before committing a binary.

### CI integration

Recommended CI step: `o- bundle --verify` after checkout to confirm that checked-in binary (if any) matches source assets. Or: `o- bundle && go build -o dist/app` — the deterministic hash ensures CI and local builds match.

### Post-build artifact hash

The builder's existing sha256 verification of the output binary (from v0.1's `verifyCached`) already covers the full binary including embedded assets. No separate asset-hash file needed — the binary sha256 is the asset hash (plus code). If the binary changes, either code or assets changed.

---

## 7. Implementation Plan

### Files to create

| File | Purpose |
|---|---|
| `internal/bundle/bundle.go` | Core logic: glob resolution, file matching, collision detection, hash computation |
| `internal/bundle/generator.go` | Code generation: write `.o-/embed/bundle.go` and asset symlinks |
| `internal/bundle/bundle_test.go` | Tests: glob matching, collision resolution, determinism, traversal safety |
| `internal/bundle/embedgen/embedgen.go` | NO — this is the generated package. It doesn't exist until `o- bundle` runs |
| `internal/cli/bundle.go` | `o- bundle` command (standalone) + `--dry-run`, `--verify` flags |

### Files to modify

| File | Change |
|---|---|
| `internal/manifest/manifest.go` | Add `Bundle` struct and `BundleInclude` types to Manifest |
| `internal/builder/builder.go` | Add `EnsureBundle()` method; bundle hash in `projectKey()` |
| `internal/cli/build.go` | Call `builder.EnsureBundle()` before `b.Build()` |
| `internal/cli/run.go` | Bundle is handled by builder.Build() — no change needed except watcher exclusion for `.o-/embed/` |
| `main.go` | Add `cli.NewBundleCmd()` to root command |
| `internal/builder/builder.go` — sourceHash | Add `.html`, `.yaml`, `.json`, `.tmpl` are already there. Add `.css`, `.js`, `.png`, `.svg`, `.wasm` to the extension list for general asset support |
| `internal/scaffold/templates/*/main.go` | Add `bundle` import path to generated project templates |

### Package structure

```
internal/
  bundle/
    bundle.go        # glob resolution, matching, collision checks
    generator.go     # code generation (write bundle.go + symlinks)
    bundle_test.go

  cli/
    bundle.go        # o- bundle command
```

The `embedgen` package is NOT committed — it lives at `.o-/embedgen/bundle.go` and is created/updated by the generator.

---

## 8. Hidden Coupling and 6-Month Risks

### Coupling: Asset files become compile-time dependencies

This is the fundamental tradeoff of go:embed. A missing template file (developer deleted it but hasn't updated the manifest) causes a compile error, not a runtime error. This is actually BETTER than runtime errors — fail early at build time. But it means asset file availability is coupled to build success. If you have optional assets (e.g. "only embed if the file exists"), you need a separate build tag strategy.

**6-month test:** A team ships a microservice where config templates are embedded. A junior dev deletes `templates/default.yaml` without updating `o-.yaml`. Result: compile error on `o- build`, not a runtime crash in production. This is correct behavior — build-time failure is safer than runtime.

### Coupling: Source hash now includes asset files

Asset files (`.html`, `.png`, `.yaml`, `.json`) are already in the `sourceHash()` walk list for v0.1 by extension. If a developer embeds 5000 small asset files, the source hash walk goes from ~10-30ms to potentially 200ms+. This is acceptable for v0.2; if profiling shows it's a bottleneck, we switch to incremental hashing (v0.3 backlog item, already noted in DECISION.md).

### Coupling: go:embed does NOT support file-level exclusion at the source level

`//go:embed assets/*` includes EVERYTHING in the directory. If a developer puts a `.env` file in the `assets/` directory, it gets embedded. The `exclude` patterns in the bundle manifest handle this at the GENERATION level (before go:embed sees the files), but they must be carefully audited: a missing exclude pattern embeds a secret into the binary. Mitigation: `o- bundle` explicitly resolves all globs and prints what it's embedding. `--dry-run` shows the manifest. The `max_size` cap prevents accidental embedding of giant files.

**6-month test:** Developer adds `secrets/` to their project and accidentally has `assets/**/*` in their bundle includes. Result: secrets are embedded in the binary. Mitigation: exclude patterns for `**/.env`, `**/secrets/**`, `**/*.pem` should be part of the default exclude set.

---

## 9. Security Risks (What Security Reviewer Will Attack)

### Risk A: Path Traversal in Glob Patterns (HIGH)

**Attack vector:** A malicious o-.yaml specifies `include: ["../../etc/passwd"]` or `include: ["../../../root/.ssh/id_rsa"]`. The glob resolver reads files outside the project root and embeds them into the binary.

**Mitigation:**
1. Every matched file path is resolved to an absolute path via `filepath.Abs()` and `filepath.EvalSymlinks()` (or at least the project root is).
2. After resolution, verify the file's absolute path starts with the project root's absolute path. If not, reject with "path traversal denied: <path>".
3. Symlinks inside the project root that resolve outside are also rejected — `EvalSymlinks()` on every matched file before accepting.
4. `..` components in glob patterns are rejected at parse time (before file matching).

**Residual risk:** A symlink inside the project directory pointing to `/etc/passwd`. The glob `templates/**/*` matches `templates/link-to-etc` which resolves to `/etc/passwd`. Mitigation: `EvalSymlinks()` on every file candidate + project-root prefix check catches this. Residual is only if the symlink target is itself under the project root (benign) or if the developer explicitly puts a symlink farm (deliberate, self-inflicted).

### Risk B: Asset Collision Hiding Sensitive File (MEDIUM)

**Attack vector:** Two glob patterns produce the same embedded path, and `collision: "last_wins"` silently overwrites. A developer expects `config/prod.yaml` to be embedded but a later glob's `config/*.yaml` overwrites it with `config/dev.yaml`. Production ships with wrong config.

**Mitigation:**
1. Default `collision` is `"error"` — fail loud.
2. If user sets `"last_wins"` or `"first_wins"`, the generator reports a warning listing every collision at generation time: "WARNING: two files map to the same path 'config.yaml': assets/prod.yaml (kept), config/dev.yaml (dropped)".

### Risk C: Asset Tampering via Generated File Modification (LOW)

**Attack vector:** An attacker modifies `.o-/embed/bundle.go` directly, changing the `ManifestHash` constant or the embedded `BundleFS` variable (e.g. replacing `//go:embed assets/*` with a different directory). The builder skips regeneration because `.o-/embed/.hash` matches, but the generated file is now malicious.

**Mitigation:**
1. `EnsureBundle` re-verifies the hash by reading `bundle.go`, extracting `ManifestHash`, and comparing it against a fresh computation. If mismatch, regenerate (overwriting the tampered file).
2. The `.o-/embed/.hash` file is sha256 of the bundle CONFIGURATION (include/exclude patterns + strip_prefix + max_size), not of the generated output. At generation time, we compute the expected ManifestHash from the files, NOT from the generated file. This means a tampered `.o-/embed/bundle.go` that disagrees with the actual assets will be caught on the next `EnsureBundle` call (which happens before every `o- build` and `o- run`).
3. `.o-/embed/` is excluded from the watcher — no auto-trigger on generated file modification.

**Residual risk:** A race between `EnsureBundle` verification and `go build` in the same invocation. Acceptable: O- is a single-threaded CLI tool.

### Risk D: Binary Size Blowup — Accidental Embedding (MEDIUM)

**Attack vector:** Developer writes `include: ["."]` or `include: ["**/*"]` and embeds the entire project (including `node_modules/`, `vendor/`, `.git/`, build artifacts) into the binary. Binary goes from 15MB to 2GB. Build time and memory explode.

**Mitigation:**
1. `max_size` cap defaults to 50MB. Configurable in manifest. If total asset size exceeds cap, generation fails with a clear error listing the largest files and suggesting exclude patterns.
2. Default exclude patterns: `**/*.go`, `**/node_modules/**`, `**/.git/**`, `**/vendor/**`, `**/dist/**`, `**/.o-/**`, `**/.cache/**`, `**/*.test`, `**/testdata/**`, `**/.env`, `**/*.pem`, `**/secrets/**`.
3. `--dry-run` mode shows total size + file count before generation.

---

## 10. Performance Risks (What Performance Skeptic Will Attack)

### Risk E: Build Time Increase from File Copy (MEDIUM)

**Attack vector:** The generator copies or symlinks every matched asset into `.o-/embed/assets/`. For 1000 small files (e.g. a template project with partials), this is a 5-15ms cost (symlinks) or 50-200ms (copies). Symlinks are faster but less portable (no symlinks on Windows by default in older Go).

**Numbers:**
- 100 files, avg 10KB (1MB total): symlink = ~2ms, copy = ~20ms
- 1000 files, avg 100KB (100MB total): symlink = ~20ms, copy = ~200ms
- 10000 files, avg 10KB (100MB total): symlink = ~200ms, copy = ~1-2s

**Mitigation:** Use symlinks where supported (Linux/macOS). On Windows, fall back to hard links (which work on NTFS). Copy is the last resort and emits a warning: "symlink unavailable, falling back to copy — this is slower."

**Verdict:** Acceptable. The build-time cost is one-time per asset change (cached after that). Even the worst case (10k files, copy) is ~2s, which is small relative to `go build` for a project with 10k files.

### Risk F: Binary Size Growth (MEDIUM)

**Attack vector:** Bundling large assets (images, videos, WASM modules) makes the binary disproportionately large.

**Numbers:**
- Empty Go binary: ~2MB (with -s -w)
- +5MB of assets: binary is ~7MB
- +50MB of assets: binary is ~52MB
- +200MB of assets: binary is ~202MB

200MB is the point where binary distribution becomes painful (GitHub release time, download time, container layer size).

**Mitigation:**
1. `max_size` default 50MB prevents accidental large bundles.
2. Documentation guides users: "For assets >50MB or assets that change independently of the binary, consider loading from a filesystem path at runtime instead of embedding."
3. `o- bundle --dry-run` prints expected binary size increase.

**Residual risk:** A user who genuinely needs 200MB of embedded assets (e.g. a game with assets, or an internal tool with documentation) WILL have a large binary. This is a Go embed limitation, not an O- design flaw. O- should document: "If binary size matters, minimize embedded assets."

### Risk G: Cache Interaction — Generated Files Cause Cache Misses (MEDIUM)

**Attack vector:** The builder's `projectKey()` does NOT include the bundle manifest hash. After a bundle regeneration (template edit), `sourceHash()` sees the file changed (template source is in the walk), so the cache key changes. This works correctly. But if `sourceHash()` were to exclude template files (because they're "not Go source"), the cache would NOT invalidate on a template change, and the app would serve stale templates.

**Mitigation:** Template/asset file extensions (`.html`, `.yaml`, `.json`, `.tmpl`) are already in the sourceHash walk list from v0.1. Adding `.css`, `.js`, `.png`, `.svg`, `.wasm` in v0.2 for the bundle epic ensures all asset types are watched. The generated `.o-/embed/` files are excluded (they are derived), but their source files (the originals) are NOT excluded — correct cache invalidation.

**Additional measure:** Include the bundle manifest hash directly in `projectKey()` (see §5). This catches the edge case where the bundle configuration changes (e.g. a new `exclude` pattern) but no source file content changed. Without this, reconfiguring `bundle.exclude` wouldn't trigger a rebuild.

### Risk H: Concurrent `o- run` + `o- build` Bundle Race (LOW)

**Attack vector:** `o- run` is running a hot-reload loop. Developer opens another terminal and runs `o- build`. Both call `EnsureBundle` concurrently. Two processes write to `.o-/embed/bundle.go` simultaneously — corruption.

**Mitigation:**
1. Lock file: `EnsureBundle` acquires a file lock (`.o-/embed/.lock`, using `os.Create` with O_EXCL or a `flock` syscall). If lock acquisition fails, wait up to 1s (another process is generating), then proceed.
2. Atomic write: generate to a temp file (`.o-/embed/bundle.go.tmp`), then rename to `.o-/embed/bundle.go`. Rename is atomic on Linux/macOS.
3. Same for `.o-/embed/.hash`: write to `.tmp`, then rename.

**Residual risk:** A kill during generation leaves `.o-/embed/bundle.go.tmp` behind. Cleanup: remove `.tmp` files on startup.

---

## 11. Summary Decision Matrix

| Decision | Choice | Rejected Alternative | Rationale |
|---|---|---|---|
| Embedding mechanism | go:embed generated source | Append-zip payload / Build-tag dir | Stdlib, portable, on-disk inspectable, participates in Go cache |
| Manifest format | YAML (existing strict parser) | JSON / TOML | Consistency with v0.1 manifest. Same parser, same security checks |
| Collision policy | Default: error | Silent overwrite | Catch "my config was swapped" bugs at build time |
| Determinism | Sorted walk + content-only hash + fixed template | None | CEO mandate: same inputs -> same binary |
| Generated code location | `.o-/embed/` | Package tree / internal/ | Already excluded from watcher + source hash + git |
| Max size cap | 50MB default (configurable) | Unlimited | Prevent accidental 2GB binary from `include: ["**/*"]` |
| Symlinks vs copies | Symlinks (Linux/macOS), hardlinks (Win), copies (fallback) | Always copy | Performance: symlinks are ~10x faster |
| `o- run` integration | Transparent (EnsureBundle in builder.Build) | Require explicit `o- bundle` first | Ease of use (#3): asset edits should hot-reload without ceremony |
| Verification | bundle.go ManifestHash constant + EnsureBundle re-hash | Separate .sha256 file | Self-contained: one file has the hash, no external tracking |

---

## 12. Risks Summary for Security + Performance Review

### Top 3 Security Risks

1. **Path traversal in globs** — `include: ["../../etc/passwd"]` reads system files into the binary. Mitigation: absolute path resolution + project-root prefix check + `EvalSymlinks()` + reject `..` in patterns. Residual: symlinks inside project root that point outside.

2. **Accidental secret embedding** — `include: ["**/*"]` or loose pattern embeds `.env`, `*.pem`, `secrets/` into the binary. Mitigation: default exclude patterns for sensitive files + `max_size` cap + `--dry-run` output + explicit documentation.

3. **Asset collision hiding content** — Two glob patterns map to the same embedded path; user gets the wrong file in production. Mitigation: `collision: "error"` as default; warnings on `first_wins`/`last_wins`.

### Top 3 Performance Risks

1. **Binary size blowup** — Assets can grow the binary to 200MB+ if not bounded. Mitigation: 50MB default cap, documentation, `--dry-run` size preview.

2. **Build-time asset copying** — 10k files at 1-2s copy time every asset change. Mitigation: symlinks/hardlinks preferred; copy only on Windows fallback; cached after first generation.

3. **Cache invalidation on asset change** — Template edits trigger full Go recompile (not just incremental for the changed `bundle.go`). This is correct Go cache behavior and unavoidable — changing `//go:embed`'s backing files invalidates the cache for the embedding package. Mitigation: document that asset changes trigger a package recompile (same as editing a `.go` file).