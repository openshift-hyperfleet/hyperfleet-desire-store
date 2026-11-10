/*
hyperfleet-desire-store serves the HyperFleet Desire Store API: the REST v1
service that transports desires from adapters to appliers.

The service design lives in the architecture repository:
https://github.com/openshift-hyperfleet/architecture/blob/main/hyperfleet/docs/desire-store-dd.md
*/
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Build metadata, injected with -ldflags at build time.
var (
	version = "0.0.0-dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "hyperfleet-desire-store",
		Short:         "HyperFleet Desire Store API",
		Long:          "HyperFleet Desire Store API transports desires from adapters to appliers.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newServeCommand(), newVersionCommand())
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return printVersion(cmd.OutOrStdout())
		},
	}
}

// printVersion writes the build metadata and reports a write failure, so a
// broken stdout surfaces instead of being ignored.
func printVersion(out io.Writer) error {
	lines := []struct{ label, value string }{
		{"Version", version},
		{"Commit", commit},
		{"Build Date", date},
	}

	for _, line := range lines {
		if _, err := fmt.Fprintf(out, "%-12s%s\n", line.label+":", line.value); err != nil {
			return fmt.Errorf("writing version output: %w", err)
		}
	}
	return nil
}
