//go:build integration

// Integration tests for the process-supervisor lifecycle. These spawn real
// child processes (Security finding B1: process-group stop must reach
// grandchildren, or they become orphans). Tagged `integration` because they
// fork and use bounded waits; CI runs them via `go test -tags integration`.
package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var helperPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "o+helper")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain:", err)
		os.Exit(1)
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "helper"), "./testdata/helper")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper build failed: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	helperPath = filepath.Join(dir, "helper")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// waitFor polls a condition with a bounded deadline (no unbounded sleeps).
// Windows are generous: CI runners are slow and cold (helper build, scheduler
// load), and a timeout here is a flake, not a signal.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestStartStop_Graceful: a normal child dies on group SIGTERM, Stop returns nil.
func TestStartStop_Graceful(t *testing.T) {
	r, err := Start(helperPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "child start", func() bool { return r.PID() != 0 })
	if err := r.Stop(2 * time.Second); err != nil {
		t.Fatalf("graceful Stop returned error: %v", err)
	}
	if !r.Exited() {
		t.Fatal("runner must report exited after Stop")
	}
}

// TestStop_SIGTERMIgnoring: a child that traps SIGTERM survives grace, so Stop
// escalates to SIGKILL and reports the escalation. The helper writes a ready
// file only after its handler is installed, so Stop never races setup.
func TestStop_SIGTERMIgnoring(t *testing.T) {
	dir := t.TempDir()
	readyFile := filepath.Join(dir, "ready")
	r, err := StartArgs(helperPath, []string{"ignore", readyFile}, dir)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "helper ready (SIGTERM handler installed)", func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	})
	start := time.Now()
	err = r.Stop(1 * time.Second)
	if err == nil {
		t.Fatal("Stop must report escalation when the child ignores SIGTERM")
	}
	if time.Since(start) < 800*time.Millisecond {
		t.Errorf("SIGKILL escalated too early: %v", time.Since(start))
	}
	if !r.Exited() {
		t.Fatal("runner must report exited after SIGKILL")
	}
}

// procState reads /proc/<pid>/stat and returns the process state letter.
// ok=false means the process is fully gone. A zombie (Z/X) is DEAD — its parent
// died before reaping, so it lingers until init reaps it; the group kill still
// landed, which is what this test proves.
func procState(pid int) (string, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	i := strings.LastIndex(string(data), ")")
	if i < 0 || i+2 >= len(data) {
		return "", false
	}
	return string(data[i+2]), true
}

// TestStop_KillsGrandchildren (Security finding B1): the child spawns `sleep
// 300`; the process group kill must take the grandchild down too, or it
// becomes an orphan holding resources.
func TestStop_KillsGrandchildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	r, err := StartArgs(helperPath, []string{"grandchild", pidFile}, dir)
	if err != nil {
		t.Fatal(err)
	}
	var grandPID int
	waitFor(t, 15*time.Second, "grandchild pidfile", func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		_, err = fmt.Sscanf(string(data), "%d", &grandPID)
		return err == nil && grandPID > 0
	})
	if err := r.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	// grandchild must be dead: either reaped (gone) or a zombie awaiting init.
	state, ok := procState(grandPID)
	if ok && state != "Z" && state != "X" {
		t.Fatalf("grandchild pid %d is still running (state %s) — process-group stop missed it (orphan!)", grandPID, state)
	}
}
