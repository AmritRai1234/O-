package cli

import (
	"github.com/amritrai/oplus/internal/scaffold"
	"github.com/spf13/cobra"
)

func NewNewCmd() *cobra.Command {
	var template, module string
	var force bool
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a new project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return scaffold.Create(args[0], template, module, force)
		},
	}
	cmd.Flags().StringVar(&template, "template", "minimal", "template: minimal | web-server | cli")
	cmd.Flags().StringVar(&module, "module", "", "go module name (default: project name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing non-empty directory")
	return cmd
}
