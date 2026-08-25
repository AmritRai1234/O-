package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/amritrai/o-/internal/manifest"
)

const (
	// DefaultMaxSize caps the total embedded asset size (Security condition:
	// a loose glob must not balloon the binary).
	DefaultMaxSize = 50 << 20 // 50MB
	// GeneratedFile is written at the PROJECT ROOT. go:embed patterns cannot
	// contain "..", so the generated file must sit where it can reach every
	// asset without parent traversal. Root placement also means the generated
	// .go participates in sourceHash (cache invalidation) and the watcher
	// (hot reload) automatically. (Ledger: deviation from the winning draft's
	// .o-/embed/ placement — embed ignores symlinks, so the draft's symlink
	// trick would silently embed nothing.)
	GeneratedFile = "o-bundle.gen.go"
)

// bundleManifestPath is relative to the project root.
var bundleManifestPath = filepath.Join(".o-", "embed", ".bundle.json")

// defaultExcludes are hard-coded secret/source guards (Security condition:
// mandatory, not suggested). .env files can't be embedded by go:embed anyway
// (dotfiles are ignored), but *.pem/*.key/secrets can.
var defaultExcludes = []string{
	".env", ".env.*",
	"*.pem", "*.key", "*.p12", "*.pfx",
	"secrets",
	".git", "node_modules", "dist", "vendor", ".o-", ".cache",
}

// Result describes what Ensure did.
type Result struct {
	Generated bool
	Files     int
	Size      int64
	Hash      string
}

type fileMeta struct {
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

type bundleManifest struct {
	Files map[string]fileMeta `json:"files"`
	Hash  string              `json:"hash"`
}

// Ensure regenerates o-bundle.gen.go when the resolved asset set is stale.
// Called by o- build / o- run before go build, and by the o- bundle command.
func Ensure(dir string, m *manifest.Manifest) (*Result, error) {
	files, total, err := resolveAssets(dir, m)
	if err != nil {
		return nil, err
	}
	// No assets to embed: remove any previously generated file so builds never
	// pick up a stale embedded set after bundle config is removed.
	if len(files) == 0 {
		genPath := filepath.Join(dir, GeneratedFile)
		if _, err := os.Stat(genPath); err == nil {
			_ = os.Remove(genPath)
			_ = os.Remove(filepath.Join(dir, bundleManifestPath))
			return &Result{Generated: true, Files: 0, Size: 0}, nil
		}
		return &Result{Generated: false, Files: 0, Size: 0}, nil
	}
	genPath := filepath.Join(dir, GeneratedFile)
	if !stale(dir, genPath, files) {
		hash, err := readManifestHash(dir)
		if err != nil {
			return nil, err
		}
		return &Result{Generated: false, Files: len(files), Size: total, Hash: hash}, nil
	}
	hash, err := contentHash(dir, files)
	if err != nil {
		return nil, err
	}
	if err := writeGenerated(dir, m, files, hash); err != nil {
		return nil, err
	}
	if err := writeBundleManifest(dir, files, hash); err != nil {
		return nil, err
	}
	return &Result{Generated: true, Files: len(files), Size: total, Hash: hash}, nil
}

// Preview returns the resolved asset list without writing anything (--dry-run).
func Preview(dir string, m *manifest.Manifest) ([]string, int64, error) {
	files, total, err := resolveAssets(dir, m)
	if err != nil {
		return nil, 0, err
	}
	return files, total, nil
}

// resolveAssets walks the project, matches include globs, applies excludes,
// enforces safety (traversal, symlink escape, size cap), and returns the
// sorted, deduped relative paths plus total size.
func resolveAssets(dir string, m *manifest.Manifest) ([]string, int64, error) {
	includes := m.Bundle.Include
	if len(includes) == 0 {
		return nil, 0, nil
	}
	maxSize := m.Bundle.MaxSize
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	excludes := append(append([]string{}, defaultExcludes...), m.Bundle.Exclude...)

	absRoot, err := filepath.Abs(dir)
	if err != nil {
		return nil, 0, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, 0, err
	}

	// Parse-time traversal guard (Security condition): reject ".." in patterns.
	for _, p := range includes {
		clean := strings.TrimPrefix(filepath.ToSlash(p), "./")
		if strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
			return nil, 0, fmt.Errorf("bundle.include pattern %q escapes the project root", p)
		}
	}

	var files []string
	var total int64
	err = filepath.WalkDir(absRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees
		}
		if d.IsDir() {
			if p != absRoot && excludedDir(p, excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(absRoot, p)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if !matchesAny(relSlash, includes) {
			return nil
		}
		if excludedFile(relSlash, excludes) {
			return nil
		}
		// Symlink escape guard (Security condition): resolved target must stay
		// inside the resolved project root.
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil
		}
		if !within(resolved, resolvedRoot) {
			return nil // symlink points outside the project: skip
		}
		// Filename sanitization (Security condition): embed patterns are
		// space-separated and line-based; weird names would break or inject
		// into the generated file. Reject, don't mangle.
		if !embeddableName(relSlash) {
			return fmt.Errorf("asset %q has a name that go:embed cannot represent (spaces/quotes/control chars); rename it or exclude it", rel)
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		if total > maxSize {
			return fmt.Errorf("bundled assets exceed max_size %d bytes (%d); add excludes or raise bundle.max_size", maxSize, total)
		}
		files = append(files, relSlash)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Strings(files)
	// dedupe (overlapping include patterns)
	out := files[:0]
	var prev string
	for _, f := range files {
		if f != prev {
			out = append(out, f)
		}
		prev = f
	}
	return out, total, nil
}

func excludedFile(rel string, excludes []string) bool {
	for _, e := range excludes {
		e = strings.TrimPrefix(e, "./")
		if strings.Contains(e, "/") {
			if ok, _ := path.Match(e, rel); ok {
				return true
			}
			continue
		}
		// bare pattern: match any basename along the path
		base := path.Base(rel)
		if ok, _ := path.Match(e, base); ok {
			return true
		}
		for _, part := range strings.Split(rel, "/") {
			if ok, _ := path.Match(e, part); ok {
				return true
			}
		}
	}
	return false
}

func excludedDir(p string, excludes []string) bool {
	base := filepath.Base(p)
	for _, e := range excludes {
		if strings.Contains(e, "/") {
			continue
		}
		if ok, _ := path.Match(e, base); ok {
			return true
		}
	}
	return false
}

func matchesAny(rel string, includes []string) bool {
	for _, p := range includes {
		if matchPattern(p, rel) {
			return true
		}
	}
	return false
}

// matchPattern matches a manifest glob (with ** support) against a slash
// relative path.
func matchPattern(pattern, rel string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	return matchSegments(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func matchSegments(ps, rs []string) bool {
	for len(ps) > 0 {
		if ps[0] == "**" {
			for i := 0; i <= len(rs); i++ {
				if matchSegments(ps[1:], rs[i:]) {
					return true
				}
			}
			return false
		}
		if len(rs) == 0 {
			return false
		}
		if ok, err := path.Match(ps[0], rs[0]); err != nil || !ok {
			return false
		}
		ps, rs = ps[1:], rs[1:]
	}
	return len(rs) == 0
}

func embeddableName(rel string) bool {
	for _, r := range rel {
		if r < 0x20 || r == 0x7f || r == ' ' || r == '"' || r == '`' || r == '\\' {
			return false
		}
	}
	return true
}

func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// contentHash is content-only (mtime-independent) so regeneration is
// deterministic: sha256 over "rel\0sha256(content)" in sorted order.
func contentHash(dir string, files []string) (string, error) {
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		inner := sha256.Sum256(data)
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(inner[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stale reports whether the generated file and manifest are current: the
// recorded file list must match, sizes/mtimes must match, and the generated
// file must exist. Stat-only, no content hashing.
func stale(dir, genPath string, files []string) bool {
	if _, err := os.Stat(genPath); err != nil {
		return true
	}
	data, err := os.ReadFile(filepath.Join(dir, bundleManifestPath))
	if err != nil {
		return true
	}
	var bm bundleManifest
	if err := json.Unmarshal(data, &bm); err != nil {
		return true
	}
	if len(bm.Files) != len(files) {
		return true
	}
	for _, rel := range files {
		meta, ok := bm.Files[rel]
		if !ok {
			return true
		}
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return true
		}
		if info.Size() != meta.Size || info.ModTime().Format(time.RFC3339Nano) != meta.Mtime {
			return true
		}
	}
	return false
}

func readManifestHash(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, bundleManifestPath))
	if err != nil {
		return "", err
	}
	var bm bundleManifest
	if err := json.Unmarshal(data, &bm); err != nil {
		return "", err
	}
	return bm.Hash, nil
}

func writeGenerated(dir string, m *manifest.Manifest, files []string, hash string) error {
	pkg, err := rootPackage(dir)
	if err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("// Code generated by o- bundle. DO NOT EDIT.\n")
	sb.WriteString("// Regenerated automatically by o- build / o- run when assets change.\n\n")
	sb.WriteString("package " + pkg + "\n\n")
	sb.WriteString("import \"embed\"\n\n")
	sb.WriteString("// BundleFS holds the assets declared in o-.yaml bundle.include.\n")
	for _, rel := range files {
		sb.WriteString("//go:embed " + rel + "\n")
	}
	sb.WriteString("var BundleFS embed.FS\n\n")
	sb.WriteString("// BundleHash is the sha256 of the embedded asset set (sorted relpath + content).\n")
	sb.WriteString("const BundleHash = " + strconv.Quote(hash) + "\n")
	return os.WriteFile(filepath.Join(dir, GeneratedFile), []byte(sb.String()), 0o644)
}

// rootPackage returns the Go package name declared by files at the project
// root (o- targets apps: package main).
func rootPackage(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if e.Name() == GeneratedFile {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "package ") {
				pkg := strings.Fields(line)[1]
				if !seen[pkg] {
					seen[pkg] = true
				}
				break
			}
		}
	}
	if len(seen) != 1 {
		return "", fmt.Errorf("cannot determine a single root package (found %v); o- bundle requires one package at the project root", seen)
	}
	for pkg := range seen {
		return pkg, nil
	}
	return "", fmt.Errorf("no root package found")
}

func writeBundleManifest(dir string, files []string, hash string) error {
	bm := bundleManifest{Files: map[string]fileMeta{}, Hash: hash}
	for _, rel := range files {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		bm.Files[rel] = fileMeta{Size: info.Size(), Mtime: info.ModTime().Format(time.RFC3339Nano)}
	}
	data, err := json.MarshalIndent(bm, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(dir, bundleManifestPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
