package tester

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/amritrai/oplus/internal/watcher"
	"github.com/fatih/color"
)

var (
	green  = color.New(color.FgGreen, color.Bold)
	red    = color.New(color.FgRed, color.Bold)
	yellow = color.New(color.FgYellow)
	cyan   = color.New(color.FgCyan)
	dim    = color.New(color.FgHiBlack)
)

// TestEvent mirrors go test -json events.
type TestEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type testState struct {
	output []string
	result string
}

// Run executes go test with a friendly UI. With watch, it reruns on file
// change (cache-preserving: no -count=1, so unchanged tests hit $GOCACHE —
// Performance condition). Returns an error when tests fail in non-watch mode.
func Run(dir string, tags []string, watch bool) error {
	for {
		code := runOnce(dir, tags)
		if !watch {
			if code != 0 {
				return fmt.Errorf("tests failed (exit %d)", code)
			}
			return nil
		}
		fmt.Println()
		cyan.Println("o+ test: watching for changes (ctrl-c to stop)...")
		w, err := watcher.New(dir, []string{"./**/*.go"}, []string{"_test.go"}, 200*time.Millisecond)
		if err != nil {
			return err
		}
		<-w.Events()
		_ = w.Close()
	}
}

func runOnce(dir string, tags []string) int {
	args := []string{"test", "-json"}
	for _, t := range tags {
		args = append(args, "-tags", t)
	}
	args = append(args, "./...")
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		red.Println("o+ test:", err)
		return 1
	}
	if err := cmd.Start(); err != nil {
		red.Println("o+ test:", err)
		return 1
	}

	states := map[string]*testState{}
	var mu sync.Mutex
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev TestEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		handleEvent(ev, states, &mu)
	}
	waitErr := cmd.Wait()
	printSummary(states)
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

func handleEvent(ev TestEvent, states map[string]*testState, mu *sync.Mutex) {
	key := ev.Package + "/" + ev.Test
	mu.Lock()
	defer mu.Unlock()

	switch ev.Action {
	case "run":
		if ev.Test != "" {
			cyan.Printf("  RUN   %s\n", ev.Test)
		}
	case "output":
		st := states[key]
		if st == nil {
			st = &testState{}
			states[key] = st
		}
		st.output = append(st.output, ev.Output)
	case "pass", "skip":
		if ev.Test == "" {
			return
		}
		st := states[key]
		if st == nil {
			st = &testState{}
			states[key] = st
		}
		st.result = ev.Action
		if ev.Action == "pass" {
			green.Printf("  PASS  %s (%.2fs)\n", ev.Test, ev.Elapsed)
		} else {
			yellow.Printf("  SKIP  %s\n", ev.Test)
		}
	case "fail":
		st := states[key]
		if st == nil {
			st = &testState{}
			states[key] = st
		}
		st.result = "fail"
		if ev.Test == "" { // package-level failure (e.g. build error)
			red.Printf("  FAIL  %s\n", ev.Package)
			for _, l := range st.output {
				fmt.Print(l)
			}
			return
		}
		red.Printf("  FAIL  %s (%.2fs)\n", ev.Test, ev.Elapsed)
		for _, l := range st.output {
			fmt.Print(l)
		}
	}
}

func printSummary(states map[string]*testState) {
	total, fails := 0, 0
	for _, st := range states {
		if st.result == "" {
			continue
		}
		total++
		if st.result == "fail" {
			fails++
		}
	}
	line := fmt.Sprintf("o+ test: %d tests, %d failed", total, fails)
	if fails > 0 {
		red.Println(line)
	} else {
		green.Println(line)
	}
	dim.Println("(exit code preserved for CI)")
}
