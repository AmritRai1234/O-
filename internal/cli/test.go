package cli

import (
	"os"

	"github.com/amritrai/oplus/internal/manifest"
	"github.com/amritrai/oplus/internal/tester"
	"github.com/spf13/cobra"
)

func NewTestCmd() *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run tests with a friendly UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			m, err := manifest.Load(dir)
			if err != nil {
				return err
			}
			return tester.Run(dir, m.Test.Tags, watch)
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "rerun tests on file change")
	return cmd
}
