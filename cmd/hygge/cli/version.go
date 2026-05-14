package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// versionString is the canonical version banner used by both `hygge
// version` and `hygge --version`.
func versionString() string {
	return fmt.Sprintf("hygge %s (go%s on %s/%s)",
		Version,
		runtime.Version()[2:], // strip "go" prefix to avoid "gogo1.26"
		runtime.GOOS,
		runtime.GOARCH,
	)
}

// newVersionCmd builds the `hygge version` subcommand.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			writeln(out(cmd), versionString())
			return nil
		},
	}
}
