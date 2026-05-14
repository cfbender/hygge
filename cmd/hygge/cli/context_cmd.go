package cli

import (
	"context"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/agentsmd"
)

// newContextCmd builds the `hygge context` subcommand group.  These
// commands surface the project-context files (AGENTS.md / CLAUDE.md)
// that hygge loaded into the system prompt at startup.
func newContextCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "context",
		Short: "Inspect the project-context files contributing to the system prompt",
	}
	root.AddCommand(
		newContextListCmd(),
		newContextShowCmd(),
		newContextPathsCmd(),
	)
	return root
}

// newContextListCmd builds `hygge context list` — a tabular summary
// of every context source hygge loaded, with source layer, path
// (project-relative for subdir blocks), size, and line count.
func newContextListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every loaded context source with size and origin",
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
				writeln(out(cmd), "(no context files loaded)")
				return nil
			}
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "SOURCE\tPATH\tBYTES\tLINES")
			home := homeDirFromRuntime(rt)
			for _, b := range rt.AgentsBlocks {
				var display string
				if b.RelPath != "" {
					display = b.RelPath
				} else {
					display = abbreviatePath(b.Path, home)
				}
				printf(tw, "%s\t%s\t%d\t%d\n",
					b.Source.String(),
					display,
					len(b.Content),
					countLines(b.Content),
				)
			}
			return tw.Flush()
		},
	}
}

// newContextShowCmd builds `hygge context show`.  Prints every loaded
// context file, in precedence order, with the same formatting the
// system prompt sees.
func newContextShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print every loaded context file, in precedence order",
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
				writeln(out(cmd), "(no context files loaded)")
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
		Short: "Print the path of every loaded context file (one per line)",
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

// countLines returns the number of newline-delimited lines in s,
// counting the trailing line if it has content.  Empty strings yield 0.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	// If the string ends with a newline, the last "line" is empty.
	if s[len(s)-1] == '\n' {
		n--
	}
	return n
}
