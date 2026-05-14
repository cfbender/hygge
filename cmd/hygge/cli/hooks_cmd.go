package cli

import (
	"context"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newHooksCmd builds the `hygge hooks` subcommand group.
func newHooksCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hooks",
		Short: "List and inspect configured hooks",
	}
	root.AddCommand(newHooksListCmd(), newHooksShowCmd())
	return root
}

// newHooksListCmd builds `hygge hooks list`.
func newHooksListCmd() *cobra.Command {
	var eventFilter string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every configured hook",
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

			all := rt.Hooks.All()
			if len(all) == 0 {
				writeln(out(cmd), "(no hooks configured)")
				return nil
			}

			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "NAME\tSOURCE\tEVENTS\tMODE\tTIMEOUT\tDESCRIPTION")
			for _, h := range all {
				// Apply optional event filter.
				if eventFilter != "" {
					matched := false
					for _, ev := range h.Events() {
						if string(ev) == eventFilter {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}

				evStrs := make([]string, 0, len(h.Events()))
				for _, ev := range h.Events() {
					evStrs = append(evStrs, string(ev))
				}
				printf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					h.Name(),
					h.Source(),
					strings.Join(evStrs, ","),
					string(h.Mode()),
					h.Timeout().String(),
					truncateInline(h.Description(), 60),
				)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&eventFilter, "event", "", "filter hooks by event type (pre_tool, post_tool, pre_message, post_message)")
	return cmd
}

// newHooksShowCmd builds `hygge hooks show <name>`.
func newHooksShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print full detail for a single hook",
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
			all := rt.Hooks.All()
			for _, h := range all {
				if h.Name() != name {
					continue
				}
				evStrs := make([]string, 0, len(h.Events()))
				for _, ev := range h.Events() {
					evStrs = append(evStrs, string(ev))
				}
				printf(out(cmd), "name:        %s\n", h.Name())
				printf(out(cmd), "source:      %s\n", h.Source())
				printf(out(cmd), "events:      %s\n", strings.Join(evStrs, ", "))
				printf(out(cmd), "mode:        %s\n", h.Mode())
				printf(out(cmd), "timeout:     %s\n", h.Timeout())
				printf(out(cmd), "description: %s\n", h.Description())
				return nil
			}
			return die(cmd, "no hook named %q (use `hygge hooks list` to see what is configured)", name)
		},
	}
}
