package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amritrai/o-/internal/manifest"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func manifestWith(t *testing.T, include, exclude []string, maxSize int64) *manifest.Manifest {
	t.Helper()
	return &manifest.Manifest{Name: "t", Bundle: manifest.Bundle{Include: include, Exclude: exclude, MaxSize: maxSize}}
}

func TestResolve_BasicGlob(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "templates/a.html", "<html>a</html>")
	writeFile(t, dir, "templates/sub/b.html", "<html>b</html>")
	writeFile(t, dir, "config/app.yaml", "x: 1")
	writeFile(t, dir, "main.go", "package main\n")

	files, total, err := resolveAssets(dir, manifestWith(t, []string{"templates/**/*"}, nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "templates/a.html" || files[1] != "templates/sub/b.html" {
		t.Errorf("files = %v", files)
	}
	if total <= 0 {
		t.Errorf("total = %d", total)
	}
}

func TestResolve_MultiPatternSorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "z.txt", "z")
	writeFile(t, dir, "a.txt", "a")
	writeFile(t, dir, "main.go", "package main\n")
	files, _, err := resolveAssets(dir, manifestWith(t, []string{"*.txt"}, nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "a.txt" || files[1] != "z.txt" {
		t.Errorf("files not sorted: %v", files)
	}
}

func TestResolve_TraversalRejected(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveAssets(dir, manifestWith(t, []string{"../../etc/*"}, nil, 0))
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected traversal error, got %v", err)
	}
	_, _, err = resolveAssets(dir, manifestWith(t, []string{"templates/../secrets/*"}, nil, 0))
	if err == nil {
		t.Fatal("embedded .. must be rejected")
	}
}

func TestResolve_DefaultExcludes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "templates/.env", "SECRET=1")
	writeFile(t, dir, "templates/key.pem", "PRIVATE")
	writeFile(t, dir, "templates/app.key", "PRIVATE")
	writeFile(t, dir, "secrets/db.txt", "sensitive")
	writeFile(t, dir, "templates/ok.html", "ok")
	files, _, err := resolveAssets(dir, manifestWith(t, []string{"**/*"}, nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f, ".env") || strings.Contains(f, ".pem") || strings.Contains(f, ".key") || strings.HasPrefix(f, "secrets/") {
			t.Errorf("secret file leaked into bundle: %s", f)
		}
	}
	if len(files) != 1 || files[0] != "templates/ok.html" {
		t.Errorf("files = %v", files)
	}
}

func TestResolve_ManifestExclude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "templates/a.html", "a")
	writeFile(t, dir, "templates/secret.html", "s")
	files, _, err := resolveAssets(dir, manifestWith(t, []string{"templates/**/*"}, []string{"templates/secret.html"}, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "templates/a.html" {
		t.Errorf("files = %v", files)
	}
}

func TestResolve_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "assets", "evil.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "assets/ok.txt", "ok")
	files, _, err := resolveAssets(dir, manifestWith(t, []string{"assets/**/*"}, nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f, "evil") {
			t.Errorf("symlink escape leaked into bundle: %s", f)
		}
	}
	if len(files) != 1 {
		t.Errorf("files = %v", files)
	}
}

func TestResolve_SizeCap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.bin", strings.Repeat("x", 100))
	writeFile(t, dir, "b.bin", strings.Repeat("y", 100))
	_, _, err := resolveAssets(dir, manifestWith(t, []string{"*.bin"}, nil, 150))
	if err == nil || !strings.Contains(err.Error(), "max_size") {
		t.Fatalf("expected max_size error, got %v", err)
	}
}

func TestResolve_UnembeddableName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad file.txt", "x") // space in name
	_, _, err := resolveAssets(dir, manifestWith(t, []string{"*"}, nil, 0))
	if err == nil || !strings.Contains(err.Error(), "cannot represent") {
		t.Fatalf("expected unembeddable-name error, got %v", err)
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, rel string
		want         bool
	}{
		{"templates/**/*", "templates/a.html", true},
		{"templates/**/*", "templates/sub/deep/b.html", true},
		{"templates/**/*", "config/x.yaml", false},
		{"config/*.yaml", "config/x.yaml", true},
		{"config/*.yaml", "config/sub/x.yaml", false},
		{"*.go", "main.go", true},
		{"**/*.html", "a/b/c.html", true},
		{"assets/*", "assets/logo.png", true},
		{"assets/*", "assets/sub/logo.png", false},
	}
	for _, tc := range cases {
		if got := matchPattern(tc.pattern, tc.rel); got != tc.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tc.pattern, tc.rel, got, tc.want)
		}
	}
}

func TestEnsure_GeneratesDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "templates/hello.html", "<h1>hi</h1>")
	m := manifestWith(t, []string{"templates/**/*"}, nil, 0)

	res, err := Ensure(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated || res.Files != 1 || res.Hash == "" {
		t.Fatalf("first Ensure: %+v", res)
	}
	gen1, err := os.ReadFile(filepath.Join(dir, GeneratedFile))
	if err != nil {
		t.Fatal(err)
	}
	// content must include the embed directive and the hash constant
	if !strings.Contains(string(gen1), "//go:embed templates/hello.html") {
		t.Errorf("generated file missing embed directive:\n%s", gen1)
	}
	if !strings.Contains(string(gen1), `const BundleHash = "`+res.Hash+`"`) {
		t.Errorf("generated file missing hash constant")
	}

	// unchanged -> not stale -> no regeneration
	res2, err := Ensure(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Generated {
		t.Error("unchanged asset set must not regenerate")
	}
	gen2, _ := os.ReadFile(filepath.Join(dir, GeneratedFile))
	if string(gen1) != string(gen2) {
		t.Error("regeneration must be byte-deterministic")
	}
}

func TestEnsure_RegeneratesOnAssetChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "templates/a.html", "v1")
	m := manifestWith(t, []string{"templates/**/*"}, nil, 0)
	if _, err := Ensure(dir, m); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "templates/a.html", "v2-changed")
	writeFile(t, dir, "templates/new.html", "new")
	res, err := Ensure(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated || res.Files != 2 {
		t.Fatalf("expected regeneration after change: %+v", res)
	}
	data, _ := os.ReadFile(filepath.Join(dir, GeneratedFile))
	if !strings.Contains(string(data), "templates/new.html") {
		t.Error("generated file missing new asset")
	}
}

func TestEnsure_NoIncludesNoop(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	res, err := Ensure(dir, manifestWith(t, nil, nil, 0))
	if err != nil {
		t.Fatal(err)
	}
	if res.Generated {
		t.Error("no include patterns must not generate a file")
	}
	if _, err := os.Stat(filepath.Join(dir, GeneratedFile)); !os.IsNotExist(err) {
		t.Error("no include patterns must not write o-bundle.gen.go")
	}
}

func TestRootPackage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	pkg, err := rootPackage(dir)
	if err != nil || pkg != "main" {
		t.Fatalf("rootPackage = %q, %v", pkg, err)
	}
	writeFile(t, dir, "lib.go", "package mylib\n")
	if _, err := rootPackage(dir); err == nil {
		t.Error("mixed packages must error")
	}
}
