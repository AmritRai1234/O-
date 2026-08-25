package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/amritrai/oplus/internal/builder"
	"github.com/amritrai/oplus/internal/manifest"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewBuildCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a static binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return build(output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "output path (default: manifest build.output or ./dist/<name>)")
	return cmd
}

func build(output string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := manifest.Load(dir)
	if err != nil {
		return err
	}
	b, err := builder.New(dir, m)
	if err != nil {
		return err
	}

	start := time.Now()
	bin, err := b.Build()
	if err != nil {
		return err
	}

	out := output
	if out == "" {
		out = m.Build.Output
	}
	if out == "" {
		out = filepath.Join("dist", m.Name)
	}
	out = filepath.Join(dir, out)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := copyFile(bin, out); err != nil {
		return err
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	color.Green("o+ build: %s (%s) in %s", out, humanSize(info.Size()), time.Since(start).Round(time.Millisecond))
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
