package cli

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/amritrai/o-/internal/builder"
	"github.com/amritrai/o-/internal/manifest"
	"github.com/amritrai/o-/internal/runner"
	"github.com/amritrai/o-/internal/watcher"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewRunCmd() *cobra.Command {
	var trust bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the app with hot reload",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(trust)
		},
	}
	cmd.Flags().BoolVar(&trust, "trust", false, "trust this project (required when the manifest defines pre_run hooks)")
	return cmd
}

func run(trust bool) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := manifest.Load(dir)
	if err != nil {
		return err
	}

	// Trust gate (Security condition): pre_run hooks exec code from the repo;
	// first run in a directory requires an explicit --trust.
	if len(m.Run.PreRun) > 0 {
		ok, err := manifest.Trusted(dir)
		if err != nil {
			return err
		}
		if !ok {
			if !trust {
				fp, _ := manifest.Fingerprint(dir)
				return fmt.Errorf("this project defines pre_run hooks and is not trusted (fingerprint %s). Re-run with --trust to accept", fp)
			}
			if err := manifest.Trust(dir); err != nil {
				return err
			}
		}
	}
	// pre_run hooks: direct exec, no shell interpretation (Security condition).
	for _, h := range m.Run.PreRun {
		parts := strings.Fields(h)
		if len(parts) == 0 {
			continue
		}
		c := exec.Command(parts[0], parts[1:]...)
		c.Dir = dir
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		color.Cyan("o- run: pre_run %s", h)
		if err := c.Run(); err != nil {
			return fmt.Errorf("pre_run hook %q failed: %v", h, err)
		}
	}

	b, err := builder.New(dir, m)
	if err != nil {
		return err
	}
	w, err := watcher.New(dir, watchPatterns(m), append(m.Run.Exclude, "_test.go"), 100*time.Millisecond)
	if err != nil {
		return err
	}
	defer w.Close()

	// Build-before-kill (Performance + DX condition): first build, then start.
	bin, err := b.Build()
	if err != nil {
		return err
	}
	current, err := runner.Start(bin, dir)
	if err != nil {
		return err
	}
	color.Green("o- run: %s up (pid %d), watching for changes (ctrl-c to stop)", m.Name, current.PID())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	reported := false

	for {
		select {
		case <-sig:
			if current != nil {
				_ = current.Stop(3 * time.Second)
			}
			color.Yellow("o- run: stopped")
			return nil
		case <-tick.C:
			if current != nil && current.Exited() && !reported {
				color.Yellow("o- run: app exited; waiting for changes (ctrl-c to quit)")
				reported = true
			}
		case <-w.Events():
			color.Cyan("o- run: change detected, building...")
			start := time.Now()
			newBin, err := b.Build()
			if err != nil {
				// Compile failed: old process keeps running (decision: a broken
				// save must never kill a working app).
				color.Red("o- run: build failed — keeping current process running")
				continue
			}
			if current != nil {
				if err := current.Stop(3 * time.Second); err != nil {
					color.Yellow("o- run: %v", err)
				}
			}
			current, err = runner.Start(newBin, dir)
			if err != nil {
				return err
			}
			reported = false
			color.Green("o- run: restarted in %s", time.Since(start).Round(time.Millisecond))
		}
	}
}

func watchPatterns(m *manifest.Manifest) []string {
	if len(m.Run.Watch) > 0 {
		return m.Run.Watch
	}
	return []string{"./**/*.go", "./**/*.yaml", "./**/*.html"}
}
