package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed templates
var templates embed.FS

// Template describes an available scaffold template.
type Template struct {
	Name        string
	Description string
}

// List returns the available templates.
func List() []Template {
	return []Template{
		{"minimal", "bare main.go + o+.yaml"},
		{"web-server", "net/http server with PORT env"},
		{"cli", "flag-based CLI entry point"},
	}
}

// Create scaffolds a new project at target using the named template (default
// "minimal"), then runs `go mod init`. Safety (Security condition): refuses to
// write outside home or /tmp unless force is set; refuses to touch a non-empty
// directory unless force is set.
func Create(target, tmpl, module string, force bool) error {
	if tmpl == "" {
		tmpl = "minimal"
	}
	if !force {
		if err := safePath(target); err != nil {
			return err
		}
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if info, err := os.Stat(abs); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", abs)
		}
		entries, _ := os.ReadDir(abs)
		if len(entries) > 0 && !force {
			return fmt.Errorf("%s already exists and is not empty (use --force to overwrite)", abs)
		}
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}

	name := filepath.Base(abs)
	src := filepath.Join("templates", tmpl)
	ok := false
	if err := fs.WalkDir(templates, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ok = true
		data, err := templates.ReadFile(p)
		if err != nil {
			return err
		}
		content := strings.ReplaceAll(string(data), "{{NAME}}", name)
		rel := strings.TrimPrefix(p, src)
		out := filepath.Join(abs, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, []byte(content), 0o644)
	}); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown template %q (available: minimal, web-server, cli)", tmpl)
	}

	mod := module
	if mod == "" {
		mod = name
	}
	cmd := exec.Command("go", "mod", "init", mod)
	cmd.Dir = abs
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod init failed: %v\n%s", err, out)
	}
	return nil
}

// safePath rejects targets outside the user's home or /tmp (Security condition:
// `o+ new /etc/whatever` must not be able to clobber system paths).
//
// Symlink-aware (Security finding, 2026-08-25): filepath.Abs does NOT resolve
// symlinks, so a path like ~/link-to-etc/sub would previously pass the naive
// prefix check while writing to /etc/sub. We resolve the deepest existing
// ancestor of the target and compare RESOLVED paths, so a symlinked parent
// redirecting outside home/tmp is rejected.
func safePath(target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	resolvedHome := resolve(home)
	resolvedTmp := resolve(os.TempDir())

	ancestor := abs
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("cannot resolve target path: %s", abs)
		}
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("cannot resolve symlinks in %s: %v", abs, err)
	}
	if within(resolved, resolvedHome) || within(resolved, resolvedTmp) {
		return nil
	}
	return fmt.Errorf("refusing to scaffold outside home or %s without --force: %s", os.TempDir(), abs)
}

// resolve follows symlinks; on failure returns the path unchanged.
func resolve(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

// within reports whether p is inside root (or equals it).
func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
