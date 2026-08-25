package main

import (
	"fmt"
	"os"

	"github.com/amritrai/o-/internal/cli"
	"github.com/amritrai/o-/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:     "o-",
		Short:   "o- — a Bun-class developer toolchain for Go",
		Long:    "o- gives Go developers the one-binary developer experience: run with hot reload, build, test, and scaffold. Quality first: no known-broken ships.",
		Version: version.Version,
	}
	root.AddCommand(
		cli.NewRunCmd(),
		cli.NewBuildCmd(),
		cli.NewTestCmd(),
		cli.NewNewCmd(),
	)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "o-:", err)
		os.Exit(1)
	}
}
