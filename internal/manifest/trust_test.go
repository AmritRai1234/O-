package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// setCacheDir isolates the trust store for the duration of a test.
func setCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	return dir
}

func TestFingerprint_Stable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "o-.yaml"), []byte("name: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp1, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint not stable: %s != %s", fp1, fp2)
	}
}

func TestFingerprint_ChangesOnEdit(t *testing.T) {
	dir := t.TempDir()
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("module a\n")
	fp1, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	write("module a\n// changed\n")
	fp2, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == fp2 {
		t.Error("fingerprint should change when project files change")
	}
}

func TestFingerprint_MissingFiles(t *testing.T) {
	dir := t.TempDir() // completely empty
	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp == "" {
		t.Error("expected a non-empty fingerprint even with no files")
	}
}

func TestTrust_RoundTrip(t *testing.T) {
	setCacheDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "o-.yaml"), []byte("name: t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Trusted(dir); ok {
		t.Fatal("fresh dir must not be trusted")
	}
	if err := Trust(dir); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Trusted(dir); !ok {
		t.Fatal("dir should be trusted after Trust()")
	}
	other := t.TempDir()
	if ok, _ := Trusted(other); ok {
		t.Fatal("unrelated dir must not be trusted")
	}
}

func TestTrusted_FalseOnFingerprintChange(t *testing.T) {
	setCacheDir(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Trust(dir); err != nil {
		t.Fatal(err)
	}
	// project changes after trust was recorded
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Trusted(dir); ok {
		t.Fatal("trusted status must drop when the project fingerprint changes")
	}
}

func TestTrust_CorruptJSON(t *testing.T) {
	cd := setCacheDir(t)
	if _, err := CacheDir(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cd, "o-", "trust.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ok, err := Trusted(dir)
	if err != nil {
		t.Fatalf("corrupt trust.json must not error: %v", err)
	}
	if ok {
		t.Fatal("corrupt trust.json must not trust anything")
	}
}

func TestTrust_SymlinkRejected(t *testing.T) {
	// Security finding: trust.json -> /dev/random would block ReadFile forever.
	cd := setCacheDir(t)
	if _, err := CacheDir(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cd, "target")
	if err := os.WriteFile(target, []byte(`{"dirs":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(cd, "o-", "trust.json")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ok, err := Trusted(dir)
	if err != nil {
		t.Fatalf("symlinked trust.json must not error: %v", err)
	}
	if ok {
		t.Fatal("symlinked trust.json must be treated as untrusted")
	}
}

func TestCacheDir_Mode(t *testing.T) {
	setCacheDir(t)
	d, err := CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(d)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o700 {
		t.Errorf("CacheDir mode = %o, want 700", perm)
	}
}

func TestCacheDir_FallbackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")
	d, err := CacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".cache", "o-")
	if d != want {
		t.Errorf("CacheDir = %q, want %q", d, want)
	}
}
