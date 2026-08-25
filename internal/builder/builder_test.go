package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amritrai/o-/internal/manifest"
)

func hexSum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeArtifact places a "binary" + matching .sha256 in dir and returns paths.
func writeArtifact(t *testing.T, dir, name string, content []byte) (string, string) {
	t.Helper()
	bin := filepath.Join(dir, name)
	sumFile := bin + ".sha256"
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sumFile, []byte(hexSum(content)), 0o600); err != nil {
		t.Fatal(err)
	}
	return bin, sumFile
}

func TestVerifyCached_Hit(t *testing.T) {
	dir := t.TempDir()
	bin, sumFile := writeArtifact(t, dir, "artifact", []byte("ELF...legit"))
	b := &Builder{cacheDir: dir}
	if !b.verifyCached(bin, sumFile) {
		t.Fatal("valid artifact must verify")
	}
}

func TestVerifyCached_Tampered(t *testing.T) {
	dir := t.TempDir()
	bin, sumFile := writeArtifact(t, dir, "artifact", []byte("ELF...legit"))
	b := &Builder{cacheDir: dir}
	if err := os.WriteFile(bin, []byte("ELF...EVIL"), 0o755); err != nil {
		t.Fatal(err)
	}
	if b.verifyCached(bin, sumFile) {
		t.Fatal("tampered artifact must NOT verify")
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Error("tampered artifact must be removed (no poisoned cache survives)")
	}
	if _, err := os.Stat(sumFile); !os.IsNotExist(err) {
		t.Error("tampered artifact's sum file must be removed")
	}
}

func TestVerifyCached_MissingSum(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "artifact")
	if err := os.WriteFile(bin, []byte("data"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Builder{cacheDir: dir}
	if b.verifyCached(bin, bin+".sha256") {
		t.Fatal("missing sum file must fail verification")
	}
}

func TestVerifyCached_MissingBin(t *testing.T) {
	dir := t.TempDir()
	b := &Builder{cacheDir: dir}
	if b.verifyCached(filepath.Join(dir, "nope"), filepath.Join(dir, "nope.sha256")) {
		t.Fatal("missing binary must fail verification")
	}
}

func TestProjectKey_Deterministic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &Builder{Dir: dir, Manifest: &manifest.Manifest{Name: "x"}}
	k1, err := b.projectKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := b.projectKey()
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 {
		t.Errorf("projectKey not deterministic: %s != %s", k1, k2)
	}
}

func TestProjectKey_ChangesOnSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainGo := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &Builder{Dir: dir, Manifest: &manifest.Manifest{Name: "x"}}
	k1, err := b.projectKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainGo, []byte("package main\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k2, err := b.projectKey()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Error("projectKey must change when source changes (stale-binary guard)")
	}
}

func TestSourceHash_Excludes(t *testing.T) {
	base := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	write("x_test.go", "package main\n")
	write(".git/config", "x")
	write("vendor/v.go", "package v\n")
	write("dist/d.go", "package d\n")
	write("node_modules/n.go", "package n\n")

	// All ignored files must not affect the hash: compare with a dir that has
	// only main.go.
	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := sourceHash(base)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := sourceHash(clean)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("ignored files leaked into source hash:\nbase=%s\nclean=%s", h1, h2)
	}
}

func TestSourceHash_ChangesOnContent(t *testing.T) {
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := sourceHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainGo, []byte("package main // v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := sourceHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Error("source hash must change with content")
	}
}

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("known bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := sha256File(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := hexSum([]byte("known bytes")); got != want {
		t.Errorf("sha256File = %s, want %s", got, want)
	}
}

func TestPrune_KeepsNewest(t *testing.T) {
	dir := t.TempDir()
	b := &Builder{cacheDir: dir}
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxArtifacts+5; i++ {
		bin, _ := writeArtifact(t, dir, "artifact"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+string(rune('0'+i%10)), []byte("data"))
		if i < 5 {
			// make the first 5 oldest regardless of creation order
			_ = os.Chtimes(bin, old, old)
		}
	}
	b.prune()
	count := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sha256") {
			count++
		}
	}
	if count != maxArtifacts {
		t.Fatalf("after prune: %d artifacts, want %d", count, maxArtifacts)
	}
	for i := 0; i < 5; i++ {
		oldest := "artifacta" + string(rune('0'+i))
		if _, err := os.Stat(filepath.Join(dir, oldest)); !os.IsNotExist(err) {
			t.Errorf("oldest artifact %s survived prune", oldest)
		}
	}
}

func TestNew_CacheDirMode(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", xdg)
	dir := t.TempDir()
	b, err := New(dir, &manifest.Manifest{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(b.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("builder cache dir mode = %o, want 700", perm)
	}
}
