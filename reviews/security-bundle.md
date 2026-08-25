# Security Review — O- Bundle Epic (v0.2), Round 2

**Persona:** Paranoid Security Reviewer
**Bias:** Input is malicious, deploy env hostile. Nitpick auth/injection/secrets/trust boundaries ONLY. Ignore style.
**Date:** 2026-08-25
**Scope:** `o- bundle` — two drafts reviewed against codebase at `/home/amritrai/spine/O+/internal/` (manifest, builder, watcher, scaffold, trust).

---

## Draft 1: "Static Embed" (Staff Architect) — `/home/amritrai/spine/O+/drafts/bundle-architect.md`

**Verdict: APPROVE-WITH-CONDITIONS**

The Architect correctly identifies path traversal (Risk A), asset collision (Risk B), generated-file tampering (Risk C), and accidental embedding (Risk D). The proposed mitigations are well-reasoned. However, four attack vectors are either incompletely mitigated or unmentioned. Three are MEDIUM severity and must be resolved before implementation.

---

### Attack 1 (MEDIUM): Generated-Code Injection via Weird Filenames — Breaking `bundle.go` Syntax

**Where:** §1, «Write .o-/embed/bundle.go:» — code generation step (lines 32–42). Also §9 Risk C mitigation discusses re-verifying `ManifestHash` but does not mention filename sanitization in the generated output.

**What:** The code generator writes Go source that includes file paths or content-derived identifiers. If a matched asset file has a name containing characters that break Go syntax — newlines, double-quotes, backslashes, `/*` (block-comment opener), `//` (line-comment opener), or null bytes — the generated `bundle.go` will be syntactically invalid or, worse, compile as valid Go with attacker-controlled structure.

**Concrete example:**
```
templates/";import "os";/*.html
```
If this path appears unescaped in the generated Go, the resulting file becomes:
```go
//go:embed assets/";import "os";/*.html   ← string literal broken, "os" imported
```
Go's `//go:embed` directive takes a path literal. If the path isn't properly quoted/escaped, the injection is into the Go compiler's AST, not just a string value. The attacker gets Go-level code execution during `go build`.

**Codebase cross-check:** The existing codebase has no filename-sanitization helpers. The `scaffold.go` templates use `strings.ReplaceAll` for `{{NAME}}` substitution — but that's content in pre-written templates, not dynamically generated Go source from filesystem paths.

**Mitigation required (CONDITION 1):**
- Every path or identifier derived from the filesystem that appears in generated Go source MUST be sanitized:
  - For paths in `//go:embed` directives: validate they match `^[a-zA-Z0-9_/.\\-]+$` (no whitespace, no quotes, no control chars).
  - For identifiers (package names, variable names): use `sanitizeGoIdent()` that rejects non-identifier characters.
  - OR: avoid embedding paths directly in source — use a content-addressed symlink farm where symlink names are deterministic hashes, not user-controlled filenames.

---

### Attack 2 (MEDIUM): Secret Embedding via Missing Mandatory Default Excludes

**Where:** §9, Risk D (lines 398–405). The draft *proposes* default excludes for `**/.env`, `**/*.pem`, `**/secrets/**` but lists them as «Default exclude patterns» in the mitigation text — not as binding requirements. Section 8 (lines 360–362) mentions the risk but says «Mitigation: exclude patterns for `**/.env`, `**/secrets/**`, `**/*.pem` should be part of the default exclude set» — *should* is not *will*.

**What:** Without enforced default excludes, a developer writing `bundle.include: ["**/*"]` or `include: ["templates/**"]` embeds every `.env`, `.pem`, `.key`, credential file, and SSH key present anywhere in the resolved tree. The `max_size` cap (50MB) prevents a DoS, but it does NOT prevent a 10KB secret from being embedded — and once embedded, the secret lives forever in every binary distributed from that commit.

**Codebase cross-check:** The watcher's `DefaultExcludes` (watcher.go, line 16) and `sourceHash`'s `excludedDirs` (builder.go, line 175) show the pattern: O- already has hard-coded default exclude lists for `.git`, `vendor`, `dist`, `node_modules`, `.o-`, `.cache`. The bundle epic must follow the same pattern but for *file patterns* (not just directory names).

**Codebase gap:** The existing `manifest.Fingerprint()` (trust.go, line 13) hashes `o-.yaml + go.mod + go.sum` — none of these include asset content. `sourceHash()` (builder.go, line 173) walks `.go`, `.yaml`, `.html`, `.tmpl`, `.json`, `.mod`, `.sum` — which excludes `.css`, `.js`, `.png`, `.svg`, `.wasm` (the very types bundle targets). Neither mechanism can detect or warn about `.env` files entering the bundle.

**Mitigation required (CONDITION 2):**
- Default exclude patterns MUST be hard-coded, not merely documented:
  - `**/.env` — environment files
  - `**/*.pem`, `**/*.key`, `**/*.cert` — cryptographic material
  - `**/secrets/**` — secret directories
  - `**/.git/**` — git internals (redundant with dir-name check but belt-and-suspenders)
  - `**/node_modules/**` — npm dependencies (redundant, same reason)
  - `**/.o-/**` — O- own cache (redundant, same reason)
- These must be enforced at the BUNDLE RESOLUTION level, not just documented. The generated `//go:embed` directive must only include files that pass these exclude checks.
- `--dry-run` output must show "N files excluded (secrets/crypto)" as a separate count.

---

### Attack 3 (LOW): go:embed Directory Over-Inclusion — Content-Addressed Symlink Farm May Be Insufficient

**Where:** §1, lines 38–41: «Write assets/* symlinks (or copies) under .o-/embed/ for go:embed to find» followed by `//go:embed assets/*`. Also §1, line 155: «Files under `.o-/embed/assets/` are symlinks... named by their content hash».

**What:** The draft relies on a content-addressed symlink farm under `.o-/embed/assets/` to ensure only glob-resolved files are exposed to `//go:embed`. This works if — and only if — the symlink farm is atomically replaced on every regeneration, and no stale symlinks remain from a previous generation with a different file set.

**Attack scenario:** On the FIRST `o- bundle` run, globs resolve to `[file_a, file_b]` → symlinks created for both. Developer removes `file_b` from `o-.yaml` and runs `o- bundle` again. If the symlink for `file_b` is not cleaned up (draft proposes content-hash naming, so hash may differ... but if file_b's content hash is `abc123` and no other file shares it, the stale symlink `abc123` remains). The stale symlink still exists in `assets/` → `//go:embed assets/*` includes it → binary contains `file_b` even though the manifest no longer lists it.

**Stronger (but not required for approval):** Rather than naming by content hash only, the generator should:
- Remove and re-create the entire `.o-/embed/assets/` directory on every regeneration (not incremental update).
- OR: verify that EVERY entry under `.o-/embed/assets/` corresponds to a currently-declared asset.

**Condition 3 covers this if we mandate atomic replacement of the entire symlink directory.**

---

### Attack 4 (LOW): TOCTOU Between EnsureBundle Hash Check and go build — Symlink Target Swap

**Where:** §4, lines 196–199 (EnsureBundle logic) and §9 Risk C (lines 388–396, tamper mitigation).

**What:** The proposed sequence:
1. `EnsureBundle` verifies that `.o-/embed/.hash` matches computed bundle hash
2. If match: skip generation, trust `.o-/embed/bundle.go` as current
3. `go build` reads `.o-/embed/bundle.go` and follows `//go:embed` to the symlink farm

Between step 2 and step 3, an attacker with filesystem write access to `.o-/embed/` can:
- Replace a symlink in `.o-/embed/assets/` to point to a DIFFERENT file (same symlink name, different target)
- Replace `.o-/embed/bundle.go` with a version that has the correct `ManifestHash` constant but a different `//go:embed` directive

The draft's mitigation (step in §9, line 393: «re-verify the hash by reading bundle.go, extracting ManifestHash, and comparing it against a fresh computation») only checks the `ManifestHash` constant in bundle.go — it does NOT verify what files the symlinks currently point to, and it does NOT verify the total set of files under `assets/`.

**Concrete attack:** Attacker replaces symlink `a1b2c3` (which pointed to `config/prod.yaml`) to instead point to `config/dev.yaml`. Both files have different content, but the symlink name `a1b2c3` hasn't changed — the old ManifestHash still matches because bundle.go wasn't modified. `go build` compiles the wrong config into the binary. (This assumes content-hash naming; if symlinks are named by relative path, the attack is even easier: replace `templates/index.html → /etc/passwd`.)

**Residual risk acceptance:** In a single-user CLI context, the attacker already needs write access to `.o-/embed/`, which means they already have write access to the project tree. This is LOW severity because the attacker could also modify source files directly. However, in CI environments where build cache is shared (e.g., a multi-tenant runner), this is MEDIUM.

**Mitigation (suggested, not blocking):** Before `go build`, stat every symlink under `.o-/embed/assets/` and verify it resolves to an expected file. Or simpler: re-read the content of every bundled file and re-compute the hash — not just compare `ManifestHash` constants.

---

### Attack 5 (LOW): Symlink-Followed File with Extension Bypass

**Where:** §9 Risk A (lines 368–376) — symlink resolution is proposed for path-traversal protection, but only for *outside-the-project* detection, not for *inside-the-project* file-extension bypass.

**What:** The draft correctly rejects symlinks that resolve outside the project root. However, within the project, a glob like `templates/**` matches a symlink `templates/config` that resolves to `../secrets/.env`. The resolver calls `EvalSymlinks()`, sees the resolved path is UNDER the project root, and allows it. But now a `.env` file has entered the bundle through a symlink bypass of the exclude system (which relies on file-extension or directory-name patterns).

**Codebase cross-check:** The scaffold's `safePath` (scaffold.go, lines 110–141) resolves ancestors to detect symlink escapes — but that code is for *destination* checking, not for *source* glob content filtering. No existing code in the repo verifies the extension or type of a symlink-resolved target.

**Mitigation (CONDITION 3):**
- After `EvalSymlinks()` on every matched file candidate, verify that the resolved target's file extension is in the allowed set (if the bundle has extension-based filtering).
- OR: if symlinks are followed, apply the full exclude rule set to the RESOLVED target path, not just the symlink path.

---

### Summary of Conditions for Draft 1

| # | Severity | Vector | Condition |
|---|----------|--------|-----------|
| 1 | MEDIUM | Generated-code injection via weird filenames | Sanitize every filesystem-derived path/identifier before insertion into generated Go source |
| 2 | MEDIUM | Secret embedding via missing mandatory default excludes | Hard-code default excludes for `.env`, `*.pem`, `*.key`, `secrets/`, `.git/`, `node_modules/` |
| 3 | LOW | Symlink-followed file extension bypass | After EvalSymlinks(), apply exclude rules to resolved path; verify extension against allowed set |
| 4 | LOW | TOCTOU between EnsureBundle and go build | Re-verify symlink targets and asset set, not just ManifestHash constant |
| 5 | LOW | Stale symlinks from incremental generation | Atomically recreate entire `.o-/embed/assets/` directory on each regeneration (remove-and-replace, not incremental) |

All conditions are resolvable with < 100 lines of code and zero new dependencies. APPROVE-WITH-CONDITIONS.

---

## Draft 2: "go:embed Gen" (Pragmatic Builder) — `/home/amritrai/spine/O+/drafts/bundle-builder.md`

**Verdict: REJECT**

The Builder draft's minimalism is attractive for simplicity, but it introduces attack vectors that the Architect draft correctly avoids. Seven named attack vectors, two CRITICAL, three HIGH, two MEDIUM. The critical vectors are fundamental to the design (directory-level go:embed + no excludes) — they cannot be fixed with a code review; they require redesign.

---

### Attack 1 (CRITICAL): Top-Level Directory Grouping → go:embed Over-Inclusion → Guaranteed Secret Leak

**Where:** § «Generated file», lines 68–74: «Groups matched files by their top-level directory (the first path component)» and «Emits one `//go:embed` directive per unique top-level directory».

**The attack:** The draft resolves globs at the FILE level (correct), then groups matched files by top-level DIRECTORY (e.g. `templates/`), and emits `//go:embed templates` (a directory, not individual files). Go's `//go:embed` on a directory embeds EVERYTHING in that directory tree — all subdirectories, all files, regardless of what the glob filtered.

**Concrete example:**
```yaml
bundle:
  include:
    - "templates/**/*.html"    # user expects only HTML files
```
- Glob resolution matches: `templates/index.html`, `templates/partials/header.html`
- Grouping says: top-level dir = `templates`
- Generated: `//go:embed templates`
- BUT: `templates/secrets/api.key`, `templates/.env`, `templates/node_modules/...` ALSO exist in the directory tree
- go:embed embeds ALL of them, including the secrets
- The glob resolution filtering is effectively WASTED — the go:embed directive is coarser than the glob

This is a DESIGN-LEVEL flaw, not a code-level bug. The go:embed directive's scope (directory) does not match the manifest's declared scope (file list with glob filters). Every project that uses `bundle.include` with path patterns and has ANY sensitive files in the same directory tree will leak secrets.

**This is CRITICAL because:**
- It's silent (no error, no warning)
- It violates the Principle of Least Surprise (the glob says one thing, the binary does another)
- It affects every bundle configuration that uses directory-level go:embed
- It directly enables secret leakage

**Why a code fix cannot resolve this without redesign:** To fix this, the generated code must use file-level go:embed directives (one per file, which is unwieldy) OR a symlink farm like the Architect draft's approach (which requires the Architect's infrastructure). The Builder's minimal design has no mechanism for this.

---

### Attack 2 (CRITICAL): No Exclude Mechanism — Mandatory Secrets Inclusion

**Where:** § «What I would cut» lines 43–50: `bundle.exclude` is explicitly marked «Cut». The entire manifest has only `bundle.include` — no exclude list of any kind.

**The attack:** The builder's draft intentionally omits the exclude field. Without excludes:
- `include: ["**/*"]` embeds the entire project tree including `.env`, `.pem`, `.key`, `.git/`, `node_modules/`
- There is no way to say «include everything in templates/ EXCEPT .env files»
- There is no way to add a global exclude for `secrets/` directories
- The only mitigation is a file-count or size cap (mentioned as a *risk*, not a requirement)

**This is CRITICAL because:**
- The Architect draft (rightly) identifies secret exclusion as a top-3 security risk
- The Builder draft intentionally removes the only mechanism that could address it
- The «reasonable cap» (10K files / 100MB) prevents DoS but does NOT prevent a 1KB `.env` file from being embedded
- The draft's explicit cutting of `bundle.exclude` means this design CHOOSES to have no secret exclusion mechanism

---

### Attack 3 (HIGH): No Max Size / File Count Cap — Resource Exhaustion DoS

**Where:** § Risks, lines 172–173: «Reasonable cap (e.g., 10K files or 100MB total), documented in the error message» — this is listed as a *concern*, not a *specification*. No size cap is in the manifest schema.

**The attack:** Without a hard default cap:
1. `include: ["**/*"]` on a monorepo with 100K files produces a Go file with enormous `//go:embed` directives
2. `go build` must scan, hash, and embed 100K+ files — memory usage spikes to tens of GBs
3. The resulting binary is >2GB
4. CI runs OOM, developer machines freeze
5. Even without malicious intent, a loose glob in a large project causes this

**Codebase cross-check:** The manifest parser has a 1MB cap on `o-.yaml` itself (manifest.go, line 76) — this is the right pattern. The bundle system needs an analogous cap on TOTAL ASSET SIZE. The Architect draft mandates it (50MB default); the Builder draft does not.

---

### Attack 4 (HIGH): No Collision Policy — Silent File Overwrite

**Where:** The draft has ZERO mention of collision handling. The manifest schema (lines 35–41) shows only a flat `include` list with no collision resolution.

**The attack:** Two include patterns:
```yaml
include:
  - "config/prod/*.yaml"
  - "config/overrides/*.yaml"
```
Both match `config/app.yaml` at different source paths → same embedded path → one silently overwrites the other. The developer ships with the wrong config file embedded and has no diagnostic to detect it.

**This is HIGH because:**
- Silent correctness failures in production are difficult to debug
- An attacker who controls one glob pattern's matching files can deliberately shadow another glob's files
- The Architect draft correctly makes `collision: "error"` the default
- The Builder draft has no collision detection at all

---

### Attack 5 (HIGH): Hash-in-Comment Cache Key is Ineffective

**Where:** § Determinism, line 79: «Include a sha256 comment so the build cache key materializes it correctly: `// sha256: <hash of resolved file set>`»

**The attack:** Go's build cache keys are computed from the source file CONTENT, not from comments. The string `// sha256: abc123...` in a *comment* is ignored by Go's cache — it has exactly zero effect on the cache key. The only thing that affects the cache key is the content of the surrounding Go source, which is deterministic regardless of the comment.

**Impact:** The «comment hash» mechanism does not trigger rebuilds when asset content changes. The existing `sourceHash()` in builder.go does NOT walk `.css`, `.js`, `.png`, `.svg`, `.wasm` (it only walks `.go`, `.yaml`, `.html`, `.tmpl`, `.json`, `.mod`, `.sum` — builder.go line 187). So:

1. Developer edits `static/style.css` (asset file, not in sourceHash)
2. Runs `o- bundle` → glob resolves → new hash computed → comment updated → generated Go file content changed → Go cache INVALIDATED correctly because the generated file content changed (not because of the comment)

Wait — actually the generated file DOES change because the comment hash would be different, so the Go cache WOULD invalidate. The comment hashes the resolved file set: if the resolved file set changes (e.g., new CSS file added), the comment changes, the generated Go file content changes, and Go rebuilds. So this isn't as broken as I initially thought.

BUT: if a .css file ALREADY in the resolved set has its content change, and the file list (paths) stays the same, the comment hash derived from file *paths* (not content) wouldn't change, and the generated Go file wouldn't change, and Go wouldn't rebuild. The draft says «hash of resolved file set» — if that means file *paths* (as it appears: the sorted directory list), then content-only changes are invisible.

**Revised assessment:** The hash-in-comment mechanism only detects file additions/removals, not content modifications. Content changes to embedded files go unnoticed. The mechanism is incomplete for its stated purpose.

---

### Attack 6 (MEDIUM): safePath Claim is Unverified for Bundle Globs

**Where:** § «Security Reviewer's attacks» line 168: «Already have this pattern in `internal/scaffold/safePath» — claims code reuse for bundle glob path traversal prevention.

**Codebase cross-check:** `scaffold.safePath()` (scaffold.go, lines 110–141):
- Takes a TARGET destination path (where to scaffold a new project)
- Checks that the TARGET is under home or /tmp
- Resolves ancestors to detect symlink escapes
- Returns an ERROR if the path is disallowed

This is a REJECTION function for scaffold *destinations* — it returns error on disallowed paths. The bundle glob resolver needs a CONTENT resolution function for *source* file paths — it needs to resolve glob matches against the project root and reject paths that escape. These are semantically different operations:
- safePath rejects paths outside home/tmp (not applicable — bundle always operates within the project dir)
- Glob resolution needs to reject patterns with `..` components (not the same as safePath's check)
- Glob resolution needs to verify that matched files resolve back to the project root (a CHECK, not a REJECTION — it's normal for matched files to be under root)

**Claim is incorrect.** The safePath pattern is not reusable for bundle glob resolution without significant adaptation. New code is required.

---

### Attack 7 (MEDIUM): Generated-Code Injection via Weird Filenames

**Where:** Same vulnerability as Draft 1, Attack 1. The Builder draft does not mention filename sanitization either.

The generated file emits paths directly in `//go:embed` directives (e.g., `//go:embed templates` where `templates` is derived from a glob match). A directory named `templates";import "os"` or a file named `templates/foo`
breaks the generated Go. The Builder draft is even more exposed than the Architect draft because it uses user-controlled directory NAMES as `//go:embed` targets rather than content-addressed symlink names.

---



### Summary Comparison

| # | Vector | Severity | Architect (Draft 1) | Builder (Draft 2) |
|---|--------|----------|---------------------|--------------------|
| 1 | go:embed directory over-inclusion (secret leak) | CRITICAL | Mitigated by content-addressed symlink farm (CONDITION 3 covers atomic regen) | **DESIGN FLAW**: top-level directory grouping + go:embed dir = guaranteed over-inclusion |
| 2 | No exclude mechanism (secret leakage) | CRITICAL | Proposed default excludes (CONDITION 2 makes them binding) | **INTENTIONALLY OMITTED**: `bundle.exclude` marked "Cut" |
| 3 | No size/count cap (DoS) | HIGH | 50MB default cap, configurable | Mentioned as risk only, not specified |
| 4 | No collision detection | HIGH | `collision: "error"` default | None |
| 5 | Hash-in-comment cache key fails on content-only changes | HIGH | Separate hash in projectKey() (proper cache key) | Ineffective: comment-based hash doesn't detect content changes |
| 6 | Generated-code injection via filenames | MEDIUM | Unmentioned (CONDITION 1) | Unmentioned |
| 7 | safePath claim unverified for glob resolution | MEDIUM | N/A (separate path resolution logic) | Claim is incorrect — scaffold.safePath validates destinations, not glob sources |
| 8 | Symlink bypass via file extension | LOW | Unmentioned (CONDITION 3) | N/A (no symlink farm in this design) |
| 9 | TOCTOU between EnsureBundle and go build | LOW | Partial mitigation (CONDITION 4) | N/A (auto-regen before build mitigates differently) |
| 10 | Stale symlinks from incremental generation | LOW | Unmentioned (CONDITION 5) | N/A (no symlink farm) |

---

### Requirements

#### Draft 1 (Architect): APPROVE-WITH-CONDITIONS

Must resolve before implementation:

| # | Condition | Severity | Fix guidance |
|---|-----------|----------|-------------|
| 1 | Filename sanitization in generated Go source | MEDIUM | Validate paths match `^[a-zA-Z0-9_/\-.]+$` before inserting into `//go:embed` directives |
| 2 | Hard-coded default exclude patterns | MEDIUM | Code `**/.env`, `**/*.pem`, `**/*.key`, `**/secrets/**`, `**/.git/**`, `**/node_modules/**` as mandatory excludes at the bundle resolution level |
| 3 | Symlink-followed file extension verification + atomic directory replacement | LOW | After EvalSymlinks(), apply exclude rules to resolved path; recreate entire `.o-/embed/assets/` on each generation (not incremental) |
| 4 | Pre-build verification of symlink targets, not just ManifestHash | LOW | Stat every symlink under `.o-/embed/assets/` before go build; verify resolution to expected target |
| 5 | Atomic write + file lock for concurrent EnsureBundle | LOW | Implement flock/O_EXCL as proposed in draft §10 Risk H (line 455) |

#### Draft 2 (Builder): REJECT

Rejected for the following reasons (any one alone is sufficient):

1. **CRITICAL: Top-level directory grouping + go:embed directory = guaranteed secret leakage.** `//go:embed templates` embeds all files in the directory tree, not just glob-matched files. A redesign to file-level go:embed or a symlink farm is required.

2. **CRITICAL: No exclude mechanism.** The draft intentionally cuts `bundle.exclude`, making it impossible to prevent `.env`, `.pem`, `secrets/`, or `.git/` files from being embedded. Every bundle configuration is a potential secret leak.

3. **HIGH: No size/count cap, no collision policy, ineffective cache key mechanism.** Three separate specification gaps that individually are bugs but together make the design incomplete.

4. **MEDIUM: Unsafe claim about reusing scaffold.safePath for glob resolution.** The claim that existing code handles path traversal for bundles is incorrect — scaffold.safePath validates destinations, not glob sources. New code is required, which the Builder draft doesn't account for.

**The Builder draft cannot be fixed by code review — the design needs to be reworked.** Specifically: the directory-level `//go:embed` approach (Attack 1) and the lack of excludes (Attack 2) are fundamental to the minimal-manifest philosophy. Fixing them would add the very fields (`bundle.exclude`, per-file go:embed or symlink farm) that the Builder draft explicitly cuts as over-engineering. The Architect draft already has the correct design; adopt it.

---

## Additional Observations (both drafts, not blocking)

- **sourceHash extension gap (codebase):** `sourceHash()` (builder.go:187) only walks `.go`, `.yaml`, `.html`, `.tmpl`, `.json`, `.mod`, `.sum`. Bundled asset types `.css`, `.js`, `.png`, `.svg`, `.wasm` are excluded from the source hash. If asset content changes without the file list changing, the cache key doesn't invalidate. Both drafts mention this as a cache issue — I flag it as a security concern: a stale binary means a deploy might not reflect the current source, which could hide a backdoor introduced via an asset file. Fix: add the bundle manifest hash to `projectKey()` (both drafts propose this) AND add `.css`, `.js`, `.png`, `.svg`, `.wasm` to the sourceHash extension list.

- **Hardlink bypass of max_size (Architect draft only):** If two hardlinks to the same inode appear in the resolved file set, the size check counts the size twice while the actual binary only embeds it once. Low severity (the binary doesn't actually exceed the cap), but a correctness edge case.

- **`//go:embed` on directory vs individual files (Architect draft):** The draft's `//go:embed assets/*` approach with content-addressed symlinks under `assets/` should work correctly IF the symlink farm is a flat directory where each symlink name is the content hash, AND the runtime code uses a lookup table (from `ManifestHash` derivation) to map user-visible paths to content-hash names. The draft is not explicit about how `BundleFS.ReadFile` resolves the original path. The implementation must ensure this mapping is correct and documented.

- **Watcher exclusion for `.o-/embed/` (both drafts):** Architect draft line 213 says «The watcher does NOT watch `.o-/embed/` — no recursive event loops from generated code changes.» This is correct if `.o-` remains in DefaultExcludes (watcher.go:16). Builder draft doesn't mention watcher interaction. Verify that the watcher's excluded() correctly matches `.o-` as a directory name component (it does, via `strings.HasSuffix(p, string(filepath.Separator)+e)` at watcher.go:267 plus path-component match at line 270). No change needed, observation only.