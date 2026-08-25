package cli

import (
	"fmt"
	"os"

	"github.com/amritrai/o-/internal/bundle"
	"github.com/amritrai/o-/internal/manifest"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewBundleCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Embed declared assets into the binary (go:embed generation)",
		Long:  "Generates o-bundle.gen.go from o-.yaml bundle.include globs so the static binary carries templates, config, and static assets. Runs automatically on o- build / o- run when assets change.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return bundleCmd(dryRun)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be embedded without writing anything")
	return cmd
}

func bundleCmd(dryRun bool) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := manifest.Load(dir)
	if err != nil {
		return err
	}
	if dryRun {
		files, total, err := bundle.Preview(dir, m)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			color.Yellow("o- bundle: no assets match bundle.include")
			return nil
		}
		for _, f := range files {
			fmt.Println(" ", f)
		}
		color.Cyan("o- bundle: %d files, %s total", len(files), humanSize(total))
		return nil
	}
	res, err := bundle.Ensure(dir, m)
	if err != nil {
		return err
	}
	if res.Generated {
		color.Green("o- bundle: embedded %d files (%s)", res.Files, humanSize(res.Size))
	} else {
		color.Cyan("o- bundle: up to date (%d files)", res.Files)
	}
	return nil
}
