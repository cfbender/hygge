package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/agentsmd"
)

// newContextCmd builds the `hygge context` subcommand group.  Both
// subcommands surface the AGENTS.md files hygge loaded into the
// system prompt at startup.
func newContextCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "context",
		Short: "Inspect the AGENTS.md files contributing to the system prompt",
	}
	root.AddCommand(newContextShowCmd(), newContextPathsCmd())
	return root
}

// newContextShowCmd builds `hygge context show`.  Prints every
// AGENTS.md hygge loaded, in precedence order, with the same
// formatting the system prompt sees.
func newContextShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print every AGENTS.md loaded, in precedence order",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			if len(rt.AgentsBlocks) == 0 {
				writeln(out(cmd), "(no AGENTS.md files loaded)")
				return nil
			}
			rendered := agentsmd.BuildSystemPromptAdditions(rt.AgentsBlocks)
			writeln(out(cmd), rendered)
			return nil
		},
	}
}

// newContextPathsCmd builds `hygge context paths`.  Emits the paths
// one per line so shell pipelines can consume them directly.
func newContextPathsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "paths",
		Short: "Print the path of every loaded AGENTS.md (one per line)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			for _, b := range rt.AgentsBlocks {
				writeln(out(cmd), b.Path)
			}
			return nil
		},
	}
}
