package tester

import (
	"strings"
	"sync"
	"testing"
)

func TestHandleEvent_StateMachine(t *testing.T) {
	states := map[string]*testState{}
	var mu sync.Mutex

	feed := func(ev TestEvent) {
		handleEvent(ev, states, &mu)
	}

	feed(TestEvent{Action: "run", Package: "pkg", Test: "TestA"})
	feed(TestEvent{Action: "output", Package: "pkg", Test: "TestA", Output: "log line\n"})
	feed(TestEvent{Action: "pass", Package: "pkg", Test: "TestA", Elapsed: 0.01})

	feed(TestEvent{Action: "run", Package: "pkg", Test: "TestB"})
	feed(TestEvent{Action: "output", Package: "pkg", Test: "TestB", Output: "boom\n"})
	feed(TestEvent{Action: "fail", Package: "pkg", Test: "TestB", Elapsed: 0.5})

	feed(TestEvent{Action: "run", Package: "pkg", Test: "TestC"})
	feed(TestEvent{Action: "skip", Package: "pkg", Test: "TestC"})

	mu.Lock()
	defer mu.Unlock()
	if got := states["pkg/TestA"]; got == nil || got.result != "pass" {
		t.Errorf("TestA state = %+v, want pass", got)
	}
	if got := states["pkg/TestB"]; got == nil || got.result != "fail" || len(got.output) != 1 {
		t.Errorf("TestB state = %+v, want fail with 1 buffered output line", got)
	}
	if got := states["pkg/TestC"]; got == nil || got.result != "skip" {
		t.Errorf("TestC state = %+v, want skip", got)
	}
}

func TestHandleEvent_PackageLevelFailure(t *testing.T) {
	states := map[string]*testState{}
	var mu sync.Mutex
	feed := func(ev TestEvent) { handleEvent(ev, states, &mu) }

	feed(TestEvent{Action: "output", Package: "pkg", Output: "build error: undefined: x\n"})
	feed(TestEvent{Action: "fail", Package: "pkg"}) // package-level fail (no Test)

	mu.Lock()
	defer mu.Unlock()
	if got := states["pkg"]; got == nil || got.result != "fail" || len(got.output) != 1 {
		t.Errorf("package-level state = %+v, want fail with build output", got)
	}
}

func TestSummaryLine(t *testing.T) {
	states := map[string]*testState{
		"pkg/TestA": {result: "pass"},
		"pkg/TestB": {result: "fail"},
		"pkg/TestC": {result: "skip"},
	}
	line, fails := summaryLine(states)
	if fails != 1 {
		t.Errorf("fails = %d, want 1", fails)
	}
	if !strings.Contains(line, "3 tests") || !strings.Contains(line, "1 failed") {
		t.Errorf("summary line = %q", line)
	}
}

func TestSummaryLine_Empty(t *testing.T) {
	line, fails := summaryLine(map[string]*testState{})
	if fails != 0 || !strings.Contains(line, "0 tests, 0 failed") {
		t.Errorf("empty summary = %q, fails=%d", line, fails)
	}
}
