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

			sty := newInspectStylesFor(out(cmd))
			all := rt.Subagents.List()
			if len(all) == 0 {
				// Should never happen: the builtin `general` type is
				// always present.  Defensive print so the user sees
				// SOMETHING rather than an empty pipe.
				writeln(out(cmd), sty.Muted.Render("(no sub-agent types registered)"))
				return nil
			}
			// Tabwriter: always plain to preserve column alignment.
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			printf(tw, "NAME\tSOURCE\tMODEL\tTOOLS\tDESCRIPTION\n")
			for _, t := range all {
				tools := "(defaults)"
				if len(t.Tools) > 0 {
					tools = strings.Join(t.Tools, ",")
				}
				model := "(parent)"
				if t.Model != "" {
					model = t.Model
				}
				printf(tw, "%s\t%s\t%s\t%s\t%s\n",
					t.Name,
					t.Source,
					truncateInline(model, 32),
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

			sty := newInspectStylesFor(out(cmd))
			printf(out(cmd), "%s %s\n", sty.Label.Render("name:"), sty.Value.Render(t.Name))
			printf(out(cmd), "%s %s\n", sty.Label.Render("source:"), sty.Value.Render(t.Source))
			if t.Model != "" {
				printf(out(cmd), "%s %s\n", sty.Label.Render("model:"), sty.Value.Render(t.Model))
			} else {
				printf(out(cmd), "%s %s\n", sty.Label.Render("model:"), sty.Muted.Render("(inherits parent's)"))
			}
			printf(out(cmd), "%s %s\n", sty.Label.Render("description:"), t.Description)
			if len(t.Tools) == 0 {
				printf(out(cmd), "%s %s\n", sty.Label.Render("tools:"),
					sty.Muted.Render("(defaults: "+strings.Join(rt.Subagents.DefaultTools(), ", ")+")"))
			} else {
				printf(out(cmd), "%s %s\n", sty.Label.Render("tools:"), strings.Join(t.Tools, ", "))
			}
			printf(out(cmd), "\n%s\n%s\n", sty.Muted.Render("--- system prompt ---"), t.SystemPrompt)
			return nil
		},
	}
}
