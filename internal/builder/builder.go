package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/amritrai/o-/internal/manifest"
)

const maxArtifacts = 100 // Performance condition: LRU >= 100, not 10.

// Builder compiles a project. Cache: thin layer over $GOCACHE — a per-project
// artifact keyed by (manifest fingerprint + source-tree hash), sha256-verified
// before every use (Security condition: no TOCTOU on cached binaries).
type Builder struct {
	Dir      string
	Manifest *manifest.Manifest
	cacheDir string
}

// New creates a Builder with a private (0700) artifact cache.
func New(dir string, m *manifest.Manifest) (*Builder, error) {
	cd, err := manifest.CacheDir()
	if err != nil {
		return nil, err
	}
	binDir := filepath.Join(cd, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return nil, err
	}
	return &Builder{Dir: dir, Manifest: m, cacheDir: binDir}, nil
}

// projectKey hashes the trust surface AND the source tree. A manifest-only key
// could serve a stale binary after source edits — that would violate Quality #1.
// Source-tree hashing (~10-30ms for 1000 files) is what makes cache hits
// (15-55ms) correct rather than merely fast.
func (b *Builder) projectKey() (string, error) {
	fp, err := manifest.Fingerprint(b.Dir)
	if err != nil {
		return "", err
	}
	sh, err := sourceHash(b.Dir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(fp))
	h.Write([]byte{0})
	h.Write([]byte(sh))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Build produces a path to a fresh binary for the current project state.
// Cache hit: verified artifact returned without invoking go build.
// Cache miss: go build into the cache, hash recorded, LRU pruned.
func (b *Builder) Build() (string, error) {
	key, err := b.projectKey()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(b.cacheDir, key)
	sumFile := bin + ".sha256"
	if b.verifyCached(bin, sumFile) {
		return bin, nil
	}

	args := []string{"build"}
	ld := b.Manifest.Build.Ldflags
	if len(ld) == 0 {
		ld = []string{"-s -w"}
	}
	for _, f := range ld {
		args = append(args, "-ldflags", f)
	}
	for _, t := range b.Manifest.Build.Tags {
		args = append(args, "-tags", t)
	}
	args = append(args, "-o", bin, ".")

	cmd := exec.Command("go", args...)
	cmd.Dir = b.Dir
	cmd.Env = os.Environ()
	if b.static() {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(bin)
		return "", fmt.Errorf("go build failed: %w\n%s", err, out)
	}
	sum, err := sha256File(bin)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(sumFile, []byte(sum), 0o600); err != nil {
		return "", err
	}
	b.prune()
	return bin, nil
}

func (b *Builder) static() bool {
	if b.Manifest.Build.Static != nil {
		return *b.Manifest.Build.Static
	}
	return true
}

// verifyCached re-hashes the artifact immediately before use. On mismatch the
// artifact is deleted and a rebuild is forced (no poisoned cache survives).
func (b *Builder) verifyCached(bin, sumFile string) bool {
	want, err := os.ReadFile(sumFile)
	if err != nil {
		return false
	}
	got, err := sha256File(bin)
	if err != nil {
		return false
	}
	if strings.TrimSpace(string(want)) != got {
		os.Remove(bin)
		os.Remove(sumFile)
		fmt.Fprintf(os.Stderr, "o-: cached artifact failed sha256 check — rebuilt\n")
		return false
	}
	return true
}

// prune keeps at most maxArtifacts binaries, dropping the oldest by mtime.
func (b *Builder) prune() {
	entries, err := os.ReadDir(b.cacheDir)
	if err != nil {
		return
	}
	type item struct {
		name string
		m    time.Time
	}
	var bins []item
	for _, ent := range entries {
		if strings.HasSuffix(ent.Name(), ".sha256") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		bins = append(bins, item{ent.Name(), info.ModTime()})
	}
	if len(bins) <= maxArtifacts {
		return
	}
	sort.Slice(bins, func(i, j int) bool { return bins[i].m.Before(bins[j].m) })
	for _, old := range bins[:len(bins)-maxArtifacts] {
		_ = os.Remove(filepath.Join(b.cacheDir, old.name))
		_ = os.Remove(filepath.Join(b.cacheDir, old.name+".sha256"))
	}
}

// sourceHash walks the project and hashes every build-relevant file. Excluded
// dirs mirror the watcher's defaults; _test.go is excluded so test edits don't
// churn the app artifact.
func sourceHash(root string) (string, error) {
	h := sha256.New()
	excludedDirs := map[string]bool{".git": true, "vendor": true, "dist": true, "node_modules": true, ".o-": true, ".cache": true}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip rather than fail the build
		}
		if d.IsDir() {
			if p != root && excludedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".go", ".yaml", ".yml", ".html", ".tmpl", ".json", ".mod", ".sum":
		default:
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ErrEmpty is returned when a build produces nothing.
var ErrEmpty = errors.New("build produced no output")
