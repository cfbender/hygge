package cli

import (
	"context"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newSubagentsCmd builds the `hygge subagents` subcommand group.
// Parallels `hygge skills` and `hygge mcp` so users have a uniform
// surface for inspecting agent-side configuration.
func newSubagentsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "subagents",
		Short: "List and inspect sub-agent types available to the `task` tool",
	}
	root.AddCommand(newSubagentsListCmd(), newSubagentsShowCmd())
	return root
}

// newSubagentsListCmd builds `hygge subagents list`.
func newSubagentsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every registered sub-agent type with its source",
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

			all := rt.Subagents.List()
			if len(all) == 0 {
				// Should never happen: the builtin `general` type is
				// always present.  Defensive print so the user sees
				// SOMETHING rather than an empty pipe.
				writeln(out(cmd), "(no sub-agent types registered)")
				return nil
			}
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "NAME\tSOURCE\tTOOLS\tDESCRIPTION")
			for _, t := range all {
				tools := "(defaults)"
				if len(t.Tools) > 0 {
					tools = strings.Join(t.Tools, ",")
				}
				printf(tw, "%s\t%s\t%s\t%s\n",
					t.Name,
					t.Source,
					truncateInline(tools, 30),
					truncateInline(t.Description, 60),
				)
			}
			return tw.Flush()
		},
	}
}

// newSubagentsShowCmd builds `hygge subagents show <name>`.
func newSubagentsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print the system prompt and tool allowlist for a single sub-agent type",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			name := args[0]
			t, ok := rt.Subagents.Get(name)
			if !ok {
				return die(cmd, "no sub-agent type named %q (use `hygge subagents list` to see what is loaded)", name)
			}

			printf(out(cmd), "name:        %s\n", t.Name)
			printf(out(cmd), "source:      %s\n", t.Source)
			if t.Model != "" {
				// Stage A parses but ignores; surface so the user knows
				// the value reached the registry.
				printf(out(cmd), "model:       %s (parsed but ignored in Stage A)\n", t.Model)
			}
			printf(out(cmd), "description: %s\n", t.Description)
			if len(t.Tools) == 0 {
				printf(out(cmd), "tools:       (defaults: %s)\n",
					strings.Join(rt.Subagents.DefaultTools(), ", "))
			} else {
				printf(out(cmd), "tools:       %s\n", strings.Join(t.Tools, ", "))
			}
			printf(out(cmd), "\n--- system prompt ---\n%s\n", t.SystemPrompt)
			return nil
		},
	}
}
