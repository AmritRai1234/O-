package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := root
	root = dir
	t.Cleanup(func() { root = old })
	return dir
}

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rr := httptest.NewRecorder()
	handleFile(rr, req)
	return rr
}

func TestServesFileWithReloadInjection(t *testing.T) {
	setup(t, map[string]string{"index.html": "<html><body>hi</body></html>"})
	rr := get(t, "/index.html")
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "hi") {
		t.Errorf("body missing content: %s", body)
	}
	if !strings.Contains(body, "/__o_reload.js") {
		t.Errorf("HTML missing injected reload script: %s", body)
	}
}

func TestServesIndexAtRoot(t *testing.T) {
	setup(t, map[string]string{"index.html": "<p>root</p>"})
	rr := get(t, "/index.html")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "root") {
		t.Fatalf("root file not served: %d %s", rr.Code, rr.Body.String())
	}
}

func TestTraversalBlocked(t *testing.T) {
	dir := setup(t, map[string]string{"ok.txt": "fine"})
	// write a file OUTSIDE the served root and try to reach it
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/../outside.txt", "/..%2foutside.txt", "/a/../../outside.txt"} {
		rr := get(t, p)
		if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
			t.Errorf("traversal %q: status = %d, want 403/404", p, rr.Code)
		}
	}
}

func TestDirectoryListing(t *testing.T) {
	setup(t, map[string]string{"sub/a.txt": "a", "sub/b.txt": "b"})
	rr := get(t, "/sub/")
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "a.txt") || !strings.Contains(body, "b.txt") {
		t.Errorf("listing missing entries: %s", body)
	}
	if !strings.Contains(body, "/__o_reload.js") {
		t.Errorf("listing missing reload script")
	}
}

func TestReloadEndpoint(t *testing.T) {
	setup(t, map[string]string{"x.txt": "x"})
	req := httptest.NewRequest("GET", "/__o_reload", nil)
	rr := httptest.NewRecorder()
	handleReload(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"mtime":`) {
		t.Errorf("reload endpoint malformed: %s", body)
	}
}

func TestReloadScriptEmbedded(t *testing.T) {
	setup(t, map[string]string{})
	req := httptest.NewRequest("GET", "/__o_reload.js", nil)
	rr := httptest.NewRecorder()
	handleReloadScript(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	data, err := io.ReadAll(rr.Body)
	if err != nil || len(data) == 0 {
		t.Errorf("reload script empty: %v", err)
	}
}
