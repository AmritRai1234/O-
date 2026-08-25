package runner

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Runner supervises one child process. Security conditions baked in:
// fresh process group (Setpgid), sanitized env, no inherited FDs beyond stdio.
type Runner struct {
	cmd    *exec.Cmd
	done   chan error
	exited atomic.Bool
}

// Start launches bin in dir with a fresh process group and sanitized env.
func Start(bin, dir string) (*Runner, error) {
	return StartArgs(bin, nil, dir)
}

// StartArgs is Start with explicit child arguments (used by integration tests
// to exercise process-group lifecycle with helper binaries).
func StartArgs(bin string, args []string, dir string) (*Runner, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = sanitizeEnv(os.Environ())
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	r := &Runner{cmd: cmd, done: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		// Wait() must complete before the goroutine finishes; the flag is set
		// first so Exited() stays true even after Stop drains done.
		err := cmd.Wait()
		r.exited.Store(true)
		r.done <- err
	}()
	return r, nil
}

// sanitizeEnv drops o+ internal variables AND shared-library injection vectors
// (Security finding, 2026-08-25): LD_PRELOAD / LD_LIBRARY_PATH inherited from
// the invoking shell would let a hostile repo's hooks preload arbitrary .so
// into the developer's app. macOS DYLD_* equivalents are added with the macOS
// port (see ledger: Windows/macOS deferred).
func sanitizeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "O+_") ||
			strings.HasPrefix(kv, "LD_PRELOAD=") ||
			strings.HasPrefix(kv, "LD_LIBRARY_PATH=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Stop sends SIGTERM to the whole process group, waits for grace, then SIGKILL.
func (r *Runner) Stop(grace time.Duration) error {
	if r == nil || r.cmd.Process == nil {
		return nil
	}
	pgid := r.cmd.Process.Pid // Setpgid makes the child its own group leader
	if runtime.GOOS == "windows" {
		_ = r.cmd.Process.Kill()
		<-r.done
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-r.done:
		return nil
	case <-time.After(grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-r.done
		return errors.New("process did not exit after SIGTERM; SIGKILL sent")
	}
}

// Exited reports whether the child has already terminated (non-blocking peek).
// Uses the atomic flag, not the done channel: Stop() drains done, so peeking
// the channel would wrongly report "not exited" after a clean stop.
func (r *Runner) Exited() bool {
	if r == nil || r.cmd.Process == nil {
		return true
	}
	return r.exited.Load()
}

// PID returns the child's process id.
func (r *Runner) PID() int {
	if r == nil || r.cmd.Process == nil {
		return 0
	}
	return r.cmd.Process.Pid
}
