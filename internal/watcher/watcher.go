package watcher

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultExcludes are always ignored (decision: aggressive defaults).
var DefaultExcludes = []string{".git", "vendor", "dist", "node_modules", ".o+", ".cache"}

const (
	pollInterval = 500 * time.Millisecond
)

// Watcher emits debounced change signals for a project tree. Uses fsnotify on
// Linux; transparently falls back to polling when the inotify watch limit would
// be exceeded (Performance condition: never fail silently at ~8192 dirs).
type Watcher struct {
	fs       *fsnotify.Watcher
	events   chan string
	debounce time.Duration
	excludes []string
	exts     map[string]bool // allowed extensions; nil = watch all files
	polling  bool
	root     string
	done     chan struct{}
}

// New creates a watcher on root. watchPatterns are manifest-style globs
// (e.g. "./**/*.go"); only their extensions are used for filtering. extraExcludes
// are appended to DefaultExcludes.
func New(root string, watchPatterns, extraExcludes []string, debounce time.Duration) (*Watcher, error) {
	w := &Watcher{
		events:   make(chan string, 16),
		debounce: debounce,
		excludes: append(DefaultExcludes, extraExcludes...),
		exts:     extSet(watchPatterns),
		root:     root,
		done:     make(chan struct{}),
	}
	if runtime.GOOS == "linux" && w.wouldExceedInotify(root) {
		fmt.Fprintf(os.Stderr, "o+: inotify watch limit would be exceeded — falling back to polling (%s)\n", pollInterval)
		w.polling = true
		go w.pollLoop()
		return w, nil
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w.fs = fw
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && w.excluded(p) {
			return filepath.SkipDir
		}
		return fw.Add(p)
	}); err != nil {
		fw.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

// Events returns the debounced change-signal channel.
func (w *Watcher) Events() <-chan string {
	return w.events
}

// Close stops watching.
func (w *Watcher) Close() error {
	close(w.done)
	if w.fs != nil {
		return w.fs.Close()
	}
	return nil
}

// wouldExceedInotify reports whether the project tree needs more watches than
// 80% of the kernel limit (Performance condition: detect, don't fail silently).
func (w *Watcher) wouldExceedInotify(root string) bool {
	limit := readInotifyLimit()
	if limit <= 0 {
		return false
	}
	count := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root && w.excluded(p) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			count++
		}
		return nil
	})
	return float64(count) > 0.8*float64(limit)
}

func readInotifyLimit() int {
	data, err := os.ReadFile("/proc/sys/fs/inotify/max_user_watches")
	if err != nil {
		return 0 // not Linux or unreadable: let fsnotify try
	}
	var n int
	fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &n)
	return n
}

func (w *Watcher) loop() {
	var timer *time.Timer
	var timerC <-chan time.Time
	var pending string
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() && !w.excluded(ev.Name) {
					_ = w.fs.Add(ev.Name)
				}
			}
			if !w.matches(ev.Name) {
				continue
			}
			pending = ev.Name
			if timer == nil {
				timer = time.NewTimer(w.debounce)
				timerC = timer.C
			} else {
				timer.Reset(w.debounce)
			}
		case <-timerC:
			w.events <- pending
			pending = ""
			timerC = nil
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "o+: watcher error: %v\n", err)
		}
	}
}

func (w *Watcher) pollLoop() {
	mtimes := map[string]time.Time{}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			changed := false
			seen := map[string]bool{}
			_ = filepath.WalkDir(w.root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if p != w.root && w.excluded(p) {
						return filepath.SkipDir
					}
					return nil
				}
				if !w.matches(p) {
					return nil
				}
				seen[p] = true
				info, err := d.Info()
				if err != nil {
					return nil
				}
				mt := info.ModTime()
				if prev, ok := mtimes[p]; !ok || !prev.Equal(mt) {
					mtimes[p] = mt
					changed = true
				}
				return nil
			})
			for p := range mtimes {
				if !seen[p] { // deleted file
					delete(mtimes, p)
					changed = true
				}
			}
			if changed {
				w.events <- w.root
			}
		}
	}
}

// matches applies extension + exclusion filters to a path.
func (w *Watcher) matches(p string) bool {
	if w.excluded(p) {
		return false
	}
	if w.exts == nil {
		return true
	}
	return w.exts[filepath.Ext(p)]
}

// excluded reports whether a path is covered by an exclude rule. Rules are
// either directory names ("vendor") or suffix patterns ("_test.go").
func (w *Watcher) excluded(p string) bool {
	for _, e := range w.excludes {
		if strings.HasPrefix(e, "**/*") {
			if strings.HasSuffix(p, strings.TrimPrefix(e, "**/*")) {
				return true
			}
			continue
		}
		if strings.HasSuffix(p, string(filepath.Separator)+e) {
			return true
		}
		for _, part := range strings.Split(p, string(filepath.Separator)) {
			if part == e {
				return true
			}
		}
	}
	return false
}

// extSet converts watch glob patterns into an extension set. A pattern without
// a recognizable extension (e.g. "**/*") means watch everything.
func extSet(patterns []string) map[string]bool {
	set := map[string]bool{}
	all := false
	for _, p := range patterns {
		ext := filepath.Ext(filepath.Base(p))
		if ext == "" {
			all = true
			continue
		}
		set[ext] = true
	}
	if all {
		return nil
	}
	return set
}
