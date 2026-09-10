package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/sriramr98/vectorized/commands"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "vectorized",
		Short:         "Vectorized in-memory database",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(commands.ServerCommand)
	return root
}
