package cli

import (
	"context"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/command"
)

// newCommandsCmd builds the `hygge commands` subcommand group.
// Mirrors `hygge skills` / `hygge subagents` so users have a uniform
// surface for inspecting agent-side configuration.
func newCommandsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "commands",
		Short: "List and inspect slash commands available in the TUI",
	}
	root.AddCommand(newCommandsListCmd(), newCommandsShowCmd())
	return root
}

// newCommandsListCmd builds `hygge commands list`.
func newCommandsListCmd() *cobra.Command {
	var sourceFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every registered slash command with its source",
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
			all := rt.Commands.List()
			if len(all) == 0 {
				// The built-in set is always present so this should
				// never fire; print a defensive message just in
				// case.
				writeln(out(cmd), sty.Muted.Render("(no slash commands registered)"))
				return nil
			}
			// Tabwriter: always plain to preserve column alignment.
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			printf(tw, "NAME\tSOURCE\tARGS\tDESCRIPTION\n")
			for _, c := range all {
				if sourceFilter != "" && c.Source() != sourceFilter {
					continue
				}
				printf(tw, "/%s\t%s\t%s\t%s\n",
					c.Name(),
					c.Source(),
					summariseArgs(c.Args()),
					truncateInline(c.Description(), 60),
				)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&sourceFilter, "source", "", `filter by source ("builtin" | "user" | "project")`)
	return cmd
}

// newCommandsShowCmd builds `hygge commands show <name>`.
func newCommandsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print the full detail for a single slash command",
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

			name := strings.TrimPrefix(args[0], "/")
			c, ok := rt.Commands.Get(name)
			if !ok {
				return die(cmd, "no command named /%s (use `hygge commands list` to see what is loaded)", name)
			}
			sty := newInspectStylesFor(out(cmd))
			printf(out(cmd), "%s %s\n", sty.Label.Render("name:"), sty.Value.Render("/"+c.Name()))
			printf(out(cmd), "%s %s\n", sty.Label.Render("source:"), sty.Value.Render(c.Source()))
			printf(out(cmd), "%s %s\n", sty.Label.Render("description:"), c.Description())
			argSpecs := c.Args()
			if len(argSpecs) == 0 {
				printf(out(cmd), "%s %s\n", sty.Label.Render("args:"), sty.Muted.Render("(none)"))
			} else {
				printf(out(cmd), "%s\n", sty.Label.Render("args:"))
				for _, a := range argSpecs {
					req := ""
					if a.Required {
						req = " (required)"
					}
					desc := a.Description
					if desc == "" {
						desc = "(no description)"
					}
					printf(out(cmd), "  %s%s — %s\n", sty.Value.Render(a.Name), sty.Muted.Render(req), desc)
				}
			}
			return nil
		},
	}
}

// summariseArgs renders an ArgSpec slice as a compact "<name>,<name>"
// string for the list table.  Required args are wrapped in <…>;
// optional in [<…>].  Empty list yields "—".
func summariseArgs(specs []command.ArgSpec) string {
	if len(specs) == 0 {
		return "—"
	}
	parts := make([]string, len(specs))
	for i, s := range specs {
		if s.Required {
			parts[i] = "<" + s.Name + ">"
		} else {
			parts[i] = "[" + s.Name + "]"
		}
	}
	return strings.Join(parts, " ")
}
