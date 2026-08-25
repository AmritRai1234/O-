package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafePath_HomeOK(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := safePath(filepath.Join(home, "myapp")); err != nil {
		t.Errorf("path under home must be allowed: %v", err)
	}
	if err := safePath(filepath.Join(home, "a", "b", "myapp")); err != nil {
		t.Errorf("nested path under home must be allowed: %v", err)
	}
}

func TestSafePath_TmpOK(t *testing.T) {
	if err := safePath(filepath.Join(os.TempDir(), "o+test", "myapp")); err != nil {
		t.Errorf("path under /tmp must be allowed: %v", err)
	}
}

func TestSafePath_EtcRejected(t *testing.T) {
	if err := safePath("/etc/whatever"); err == nil {
		t.Error("/etc must be rejected")
	}
	if err := safePath("/usr/local/bin"); err == nil {
		t.Error("/usr must be rejected")
	}
}

func TestSafePath_SymlinkBypassRejected(t *testing.T) {
	// Security finding (2026-08-25): filepath.Abs does not resolve symlinks,
	// so ~/esc -> /etc used to pass the naive prefix check.
	home := t.TempDir()
	t.Setenv("HOME", home)
	esc := filepath.Join(home, "esc")
	if err := os.Symlink("/etc", esc); err != nil {
		t.Fatal(err)
	}
	if err := safePath(filepath.Join(esc, "pwned")); err == nil {
		t.Fatal("symlink escape out of home must be rejected")
	}
}

func TestSafePath_SymlinkInsideHomeAllowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := safePath(filepath.Join(link, "app")); err != nil {
		t.Errorf("symlink staying inside home must be allowed: %v", err)
	}
}

func TestCreate_RefusesNonEmpty(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Create(target, "minimal", "", false); err == nil {
		t.Fatal("Create must refuse a non-empty directory without --force")
	}
}

func TestCreate_ScaffoldsMinimal(t *testing.T) {
	target := filepath.Join(t.TempDir(), "myapp")
	if err := Create(target, "minimal", "example.com/myapp", false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go", "o+.yaml", "README.md", "go.mod"} {
		if _, err := os.Stat(filepath.Join(target, want)); err != nil {
			t.Errorf("missing scaffolded file %s: %v", want, err)
		}
	}
	gomod, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gomod), "module example.com/myapp") {
		t.Errorf("go.mod missing module line: %s", gomod)
	}
	manifestData, err := os.ReadFile(filepath.Join(target, "o+.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifestData), "name: myapp") {
		t.Errorf("manifest name not substituted: %s", manifestData)
	}
}

func TestCreate_UnknownTemplate(t *testing.T) {
	target := filepath.Join(t.TempDir(), "x")
	if err := Create(target, "does-not-exist", "", false); err == nil {
		t.Fatal("unknown template must error")
	}
}

func TestList_HasThree(t *testing.T) {
	got := List()
	if len(got) != 3 {
		t.Errorf("List = %v, want 3 templates", got)
	}
}
