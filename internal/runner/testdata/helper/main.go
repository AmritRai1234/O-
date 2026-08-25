package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// Helper binary for runner integration tests (go:build integration).
// Modes:
//   (default)   sleep 60s, dies on SIGTERM (graceful-stop test)
//   ignore      trap SIGTERM and keep running (SIGKILL escalation test)
//   grandchild  spawn `sleep 300`, write its pid to os.Args[2], wait
//               (process-group stop must reach grandchildren)
func main() {
	mode := "sleep"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "ignore":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		// ready handshake: the test must not Stop() before the handler is
		// installed, or the first SIGTERM kills the child instead of being
		// trapped (that would make the escalation test racy).
		if len(os.Args) > 2 {
			_ = os.WriteFile(os.Args[2], []byte("ready"), 0o644)
		}
		<-sig
		time.Sleep(60 * time.Second)
	case "grandchild":
		cmd := exec.Command("sleep", "300")
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "grandchild spawn:", err)
			os.Exit(1)
		}
		if len(os.Args) > 2 {
			_ = os.WriteFile(os.Args[2], []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0o644)
		}
		_ = cmd.Wait()
	default:
		time.Sleep(60 * time.Second)
	}
}
