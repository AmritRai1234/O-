package runner

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Runner supervises one child process. Security conditions baked in:
// fresh process group (Setpgid), sanitized env, no inherited FDs beyond stdio.
type Runner struct {
	cmd  *exec.Cmd
	done chan error
}

// Start launches bin in dir with a fresh process group and sanitized env.
func Start(bin, dir string) (*Runner, error) {
	cmd := exec.Command(bin)
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
		r.done <- cmd.Wait()
	}()
	return r, nil
}

// sanitizeEnv drops o+ internal variables so the child never inherits them.
func sanitizeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "O+_") {
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
func (r *Runner) Exited() bool {
	if r == nil || r.cmd.Process == nil {
		return true
	}
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// PID returns the child's process id.
func (r *Runner) PID() int {
	if r == nil || r.cmd.Process == nil {
		return 0
	}
	return r.cmd.Process.Pid
}
