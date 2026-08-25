//go:build integration

// End-to-end proof: o- bundle generation -> go build -> the built binary
// serves the embedded asset from BundleFS.
package bundle

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amritrai/o-/internal/manifest"
)

func TestBundleEndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", `package main

import (
	"fmt"
	"io/fs"
)

func main() {
	data, err := fs.ReadFile(BundleFS, "templates/hello.html")
	if err != nil {
		panic(err)
	}
	fmt.Print(string(data))
}
`)
	writeFile(t, dir, "go.mod", "module e2e\n\ngo 1.24\n")
	writeFile(t, dir, "templates/hello.html", "EMBEDDED-HELLO")
	writeFile(t, dir, "o-.yaml", "name: e2e\nbundle:\n  include:\n    - templates/**/*\n")

	m, err := manifest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Ensure(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Generated || res.Files != 1 {
		t.Fatalf("Ensure: %+v", res)
	}

	bin := filepath.Join(dir, "e2e-bin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil {
		t.Fatalf("binary failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "EMBEDDED-HELLO" {
		t.Fatalf("binary output = %q, want EMBEDDED-HELLO", out)
	}
}
