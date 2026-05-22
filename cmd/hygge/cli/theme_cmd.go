package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// newThemeCmd builds the `hygge theme` subcommand group.
func newThemeCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "theme",
		Short: "Inspect the active theme",
	}
	root.AddCommand(newThemeShowCmd())
	return root
}

// newThemeShowCmd builds `hygge theme show`.
func newThemeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the active theme atoms and a sample preview",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:  rootFlags.ConfigFile,
				ProfileName: rootFlags.Profile,
				Pwd:         rootFlags.Pwd,
				// Theme inspection should not need a real provider.
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			printRaw(out(cmd), rt.Theme.FormatTheme())
			writeln(out(cmd))
			writeln(out(cmd), "preview:")
			for _, atom := range styles.AllAtoms() {
				style := rt.Theme.Style(atom)
				printf(out(cmd), "  %-16s %s\n",
					string(atom),
					style.Render(fmt.Sprintf("this is %s text", string(atom))),
				)
			}
			return nil
		},
	}
}
