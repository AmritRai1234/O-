package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// root is the directory being served (default: current directory).
var root = "."

func main() {
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		log.Fatal(err)
	}
	root = abs

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/__o_reload", handleReload)
	mux.HandleFunc("/__o_reload.js", handleReloadScript)
	mux.HandleFunc("/", handleFile)
	log.Printf("serve: %s on :%s (live reload injected)", root, port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// handleReload reports the newest mtime (unix ms) under root. The injected
// client script polls this and reloads the page when it changes.
func handleReload(w http.ResponseWriter, r *http.Request) {
	var newest time.Time
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"mtime":%d}`, newest.UnixMilli())
}

// handleReloadScript serves the embedded live-reload client.
func handleReloadScript(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(BundleFS, "templates/livereload.js")
	if err != nil {
		http.Error(w, "reload script missing", 500)
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.Write(data)
}

// handleFile serves files, directory listings, and injects the reload script
// into HTML responses. Path traversal is blocked (Security: no escaping root).
func handleFile(w http.ResponseWriter, r *http.Request) {
	clean := filepath.Clean("/" + r.URL.Path)
	target := filepath.Join(root, clean)
	if !within(target, root) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		renderListing(w, r, target)
		return
	}
	serveFile(w, target)
}

func serveFile(w http.ResponseWriter, target string) {
	data, err := os.ReadFile(target)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	ctype := mimeType(target)
	if strings.HasPrefix(ctype, "text/html") {
		// inject the live-reload client before </body>
		data = injectReload(data)
	}
	w.Header().Set("Content-Type", ctype)
	w.Write(data)
}

func renderListing(w http.ResponseWriter, r *http.Request, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "unreadable", http.StatusInternalServerError)
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var sb strings.Builder
	sb.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	sb.WriteString("<title>index of " + htmlEscape(r.URL.Path) + "</title></head><body>")
	sb.WriteString("<h1>index of " + htmlEscape(r.URL.Path) + "</h1><ul>")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		sb.WriteString(`<li><a href="` + htmlEscape(name) + `">` + htmlEscape(name) + `</a></li>`)
	}
	sb.WriteString("</ul>")
	sb.WriteString(`<script src="/__o_reload.js"></script></body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(sb.String()))
}

func mimeType(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js":
		return "application/javascript"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// injectReload appends the reload client to HTML before </body>.
func injectReload(data []byte) []byte {
	script := []byte(`<script src="/__o_reload.js"></script>`)
	s := string(data)
	if i := strings.LastIndex(s, "</body>"); i >= 0 {
		return []byte(s[:i] + string(script) + s[i:])
	}
	return append(data, script...)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// within reports whether p stays inside base (symlink-aware: resolved targets
// must also stay inside the resolved base).
func within(p, base string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, abs)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return false
	}
	resolvedBase, err := filepath.EvalSymlinks(baseAbs)
	if err != nil {
		return false
	}
	rel2, err := filepath.Rel(resolvedBase, resolved)
	if err != nil {
		return false
	}
	return rel2 != ".." && !strings.HasPrefix(rel2, ".."+string(filepath.Separator))
}
