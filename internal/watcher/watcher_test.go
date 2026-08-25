package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// fakeSource is a scriptable source (Architect decision: test the debounce
// loop with a fake event source, no flaky sleeps on real inotify). Add runs on
// the loop goroutine while tests read added, hence the mutex.
type fakeSource struct {
	mu     sync.Mutex
	evCh   chan fsnotify.Event
	errCh  chan error
	added  []string
	closed bool
}

func newFakeSource() *fakeSource {
	return &fakeSource{evCh: make(chan fsnotify.Event, 16), errCh: make(chan error, 1)}
}
func (f *fakeSource) Events() chan fsnotify.Event { return f.evCh }
func (f *fakeSource) Errors() chan error          { return f.errCh }
func (f *fakeSource) Add(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, p)
	return nil
}
func (f *fakeSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakeSource) addedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.added))
	copy(out, f.added)
	return out
}

// testWatcher builds a Watcher on a fake source with the loop running.
func testWatcher(t *testing.T, debounce time.Duration, exts map[string]bool) (*Watcher, *fakeSource) {
	t.Helper()
	fake := newFakeSource()
	w := &Watcher{
		src:      fake,
		events:   make(chan string, 16),
		debounce: debounce,
		excludes: DefaultExcludes,
		exts:     exts,
		done:     make(chan struct{}),
	}
	go w.loop()
	t.Cleanup(func() { w.Close() })
	return w, fake
}

// awaitEvent waits up to d for a change signal; returns "" on timeout.
func awaitEvent(t *testing.T, w *Watcher, d time.Duration) string {
	t.Helper()
	select {
	case p := <-w.Events():
		return p
	case <-time.After(d):
		return ""
	}
}

func TestLoop_DebounceCollapses(t *testing.T) {
	w, fake := testWatcher(t, 10*time.Millisecond, map[string]bool{".go": true})
	// burst of events within the debounce window -> exactly ONE signal
	fake.evCh <- fsnotify.Event{Name: "a.go", Op: fsnotify.Write}
	fake.evCh <- fsnotify.Event{Name: "b.go", Op: fsnotify.Write}
	fake.evCh <- fsnotify.Event{Name: "c.go", Op: fsnotify.Write}
	got := awaitEvent(t, w, 300*time.Millisecond)
	if got == "" {
		t.Fatal("expected a debounced event, got nothing")
	}
	// no second signal from the same burst
	if extra := awaitEvent(t, w, 150*time.Millisecond); extra != "" {
		t.Fatalf("debounce emitted a second event (%q) for one burst", extra)
	}
}

func TestLoop_NonMatchingIgnored(t *testing.T) {
	w, fake := testWatcher(t, 5*time.Millisecond, map[string]bool{".go": true})
	fake.evCh <- fsnotify.Event{Name: "notes.txt", Op: fsnotify.Write}
	if got := awaitEvent(t, w, 120*time.Millisecond); got != "" {
		t.Fatalf("non-matching file emitted event %q", got)
	}
}

func TestLoop_CreateDirAddsWatch(t *testing.T) {
	_, fake := testWatcher(t, 5*time.Millisecond, nil)
	dir := t.TempDir()
	fake.evCh <- fsnotify.Event{Name: dir, Op: fsnotify.Create}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, a := range fake.addedPaths() {
			if a == dir {
				return // watch added
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("created dir %s was never added to the source (added: %v)", dir, fake.addedPaths())
}

func TestExcluded(t *testing.T) {
	w := &Watcher{excludes: append(DefaultExcludes, "_test.go")}
	cases := []struct {
		path string
		want bool
	}{
		{"/proj/.git/config", true},
		{"/proj/vendor/x/y.go", true},
		{"/proj/dist/app", true},
		{"/proj/node_modules/a.js", true},
		{"/proj/.o+/tmp", true},
		{"/proj/.cache/x", true},
		{"/proj/main_test.go", true},
		{"/proj/src/main.go", false},
		{"/proj/README.md", false},
	}
	for _, tc := range cases {
		if got := w.excluded(tc.path); got != tc.want {
			t.Errorf("excluded(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestMatches_ExtensionFilter(t *testing.T) {
	w := &Watcher{exts: map[string]bool{".go": true}, excludes: DefaultExcludes}
	if !w.matches("/proj/main.go") {
		t.Error(".go file should match")
	}
	if w.matches("/proj/main.txt") {
		t.Error(".txt file should not match")
	}
	if w.matches("/proj/vendor/main.go") {
		t.Error("excluded dir should never match")
	}
}

func TestMatches_AllWhenNil(t *testing.T) {
	w := &Watcher{exts: nil, excludes: DefaultExcludes}
	if !w.matches("/proj/anything.bin") {
		t.Error("nil ext set must match everything")
	}
}

func TestExtSet(t *testing.T) {
	if set := extSet([]string{"./**/*.go"}); !set[".go"] || len(set) != 1 {
		t.Errorf("extSet(go) = %v", set)
	}
	if set := extSet([]string{"./**/*.go", "./**/*.yaml"}); !set[".go"] || !set[".yaml"] {
		t.Errorf("extSet(multi) = %v", set)
	}
	if set := extSet([]string{"./**/*"}); set != nil {
		t.Errorf("all-catch pattern should yield nil, got %v", set)
	}
	if set := extSet(nil); len(set) != 0 {
		t.Errorf("empty patterns should yield empty set, got %v", set)
	}
}

func TestReadInotifyLimitFrom(t *testing.T) {
	p := filepath.Join(t.TempDir(), "max_user_watches")
	if err := os.WriteFile(p, []byte("524288\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readInotifyLimitFrom(p); got != 524288 {
		t.Errorf("readInotifyLimitFrom = %d, want 524288", got)
	}
	if got := readInotifyLimitFrom(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Errorf("missing file should yield 0, got %d", got)
	}
}

func TestExcluded_SuffixPattern(t *testing.T) {
	w := &Watcher{excludes: []string{"**/*_test.go"}}
	if !w.excluded("/proj/pkg/foo_test.go") {
		t.Error("**/*_test.go pattern must exclude test files")
	}
	if w.excluded("/proj/pkg/foo.go") {
		t.Error("plain .go file must not match _test.go pattern")
	}
	if !strings.HasSuffix("/proj/pkg/foo_test.go", "_test.go") {
		t.Error("sanity")
	}
}
