package cli

import (
	"context"
	"io"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/config"
)

// newConfigCmd builds the `hygge config` subcommand group.
func newConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Inspect the merged configuration",
	}
	root.AddCommand(newConfigExplainCmd())
	return root
}

// newConfigExplainCmd builds `hygge config explain [key]`.
//
// With no argument, prints every known effective config value grouped by
// section, with a subtle inline comment on each line showing the exact
// winning source (file path + line when available, or <defaults>, <env>,
// <flag>).  Override chains are shown concisely so it is immediately clear
// what is overriding what.
//
// With a key argument, prints the full provenance chain for that key
// using the config package's Explain helper.
func newConfigExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain [key]",
		Short: "Show effective config values and their sources",
		Args:  cobra.MaximumNArgs(1),
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

			if len(args) == 1 {
				explained, _, err := config.Explain(rt.Provenance, rt.Config, args[0])
				if err != nil {
					return err
				}
				printRaw(out(cmd), explained)
				return nil
			}

			// No key argument: print the full merged TOML view.
			output := config.ExplainAll(rt.Provenance, rt.Config)
			printRaw(out(cmd), renderConfigExplainOutput(out(cmd), output))
			return nil
		},
	}
}

func renderConfigExplainOutput(w io.Writer, output string) string {
	if !isColorWriter(w) {
		return output
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(120),
	)
	if err != nil {
		return output
	}
	rendered, err := r.Render("```toml\n" + output + "\n```\n")
	if err != nil {
		return output
	}
	return strings.TrimRight(rendered, "\n") + "\n"
}
