package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/catalog"
)

// newCatalogCmd builds the `hygge catalog` subcommand group.
func newCatalogCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "catalog",
		Short: "Inspect and refresh the Catwalk-backed catalog",
		Long: `Inspect and refresh hygge's model catalog.

The catalog is hygge's central source of truth for model metadata:
pricing, capabilities, and context-window limits. It is sourced from
Catwalk, persisted at $XDG_STATE_HOME/hygge/catalog.json, and shipped
with an embedded snapshot so hygge works fully offline.

Subcommands:
  list                     Summary by provider, with model counts.
  list <provider>          Table of every model the catalog knows for
                           that provider id (id, context, capabilities,
                           pricing).
  show <provider>/<model>  All metadata for a single model.
  refresh                  Pull a fresh snapshot from Catwalk and write
                           it to the state directory.`,
	}
	root.AddCommand(newCatalogListCmd(), newCatalogShowCmd(), newCatalogRefreshCmd())
	return root
}

// newCatalogListCmd builds `hygge catalog list [<provider>]`.
func newCatalogListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list [<provider>]",
		Short: "List providers with model counts, or models for a single provider",
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

			cat := rt.Catalog.Source()
			if cat == nil {
				return die(cmd, "no catalog available")
			}

			if len(args) == 0 {
				return printCatalogProviders(cmd, cat)
			}
			return printCatalogProvider(cmd, cat, args[0])
		},
	}
}

// printCatalogProviders writes the "by provider" summary table.
func printCatalogProviders(cmd *cobra.Command, cat *catalog.Catalog) error {
	styles := newCLIStylesFor(out(cmd))
	loaded := cat.Loaded()
	printf(out(cmd), "%s %s   %s %s   %s %s\n",
		styles.Label.Render("source:"), styles.Value.Render(string(loaded.Source)),
		styles.Label.Render("fetched:"), styles.Value.Render(formatLoadedTime(loaded.FetchedAt)),
		styles.Label.Render("age:"), styles.Value.Render(formatAge(loaded.Age)))
	printf(out(cmd), "%s %s %s %s\n\n",
		styles.Label.Render("models:"), styles.Value.Render(fmt.Sprintf("%d", loaded.Models)),
		styles.Muted.Render("across"), styles.Value.Render(fmt.Sprintf("%d providers", loaded.Providers)))

	rows := [][]string{{"PROVIDER", "MODELS", "REASONING"}}
	for _, p := range cat.Providers() {
		entries := cat.Models(p)
		reason := 0
		for _, e := range entries {
			if e.Capabilities.Reasoning {
				reason++
			}
		}
		rows = append(rows, []string{p, fmt.Sprintf("%d", len(entries)), fmt.Sprintf("%d", reason)})
	}
	printCLITable(cmd, styles, rows)
	return nil
}

// printCatalogProvider writes the per-provider model table.
func printCatalogProvider(cmd *cobra.Command, cat *catalog.Catalog, providerID string) error {
	entries := cat.Models(providerID)
	if len(entries) == 0 {
		return die(cmd, "no models for provider %q", providerID)
	}
	styles := newCLIStylesFor(out(cmd))
	rows := [][]string{{"MODEL", "CONTEXT", "CAPABILITIES", "INPUT$/MTok", "OUTPUT$/MTok"}}
	for _, e := range entries {
		rows = append(rows, []string{
			e.ID,
			formatContext(e.Limit.ContextWindow),
			formatCapabilities(e.Capabilities),
			formatMoney(e.Cost.Input),
			formatMoney(e.Cost.Output),
		})
	}
	printCLITable(cmd, styles, rows)
	return nil
}

func printCLITable(cmd *cobra.Command, styles cliStyles, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && lipgloss.Width(cell) > widths[i] {
				widths[i] = lipgloss.Width(cell)
			}
		}
	}
	for rowIdx, row := range rows {
		for i, cell := range row {
			if i > 0 {
				printf(out(cmd), "  ")
			}
			style := styles.Value
			if rowIdx == 0 {
				style = styles.Header
			} else if i == 0 {
				style = styles.Accent
			} else if i == 2 {
				style = styles.Info
			}
			printf(out(cmd), "%s", style.Render(cliPadRight(cell, widths[i])))
		}
		printf(out(cmd), "\n")
	}
}

func printCLIField(cmd *cobra.Command, styles cliStyles, label, value string) {
	printf(out(cmd), "%s  %s\n", styles.Label.Render(cliPadRight(label, 16)), styles.Value.Render(value))
}

// newCatalogShowCmd builds `hygge catalog show <provider>/<model>`.
func newCatalogShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <provider>/<model>",
		Short: "Print all metadata for a single catalog entry",
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

			ref := args[0]
			providerID, modelID, ok := splitProviderModel(ref)
			if !ok {
				return die(cmd, "expected <provider>/<model>, got %q", ref)
			}
			cat := rt.Catalog.Source()
			if cat == nil {
				return die(cmd, "no catalog available")
			}
			e, ok := cat.Lookup(providerID, modelID)
			if !ok {
				return die(cmd, "no entry for %s/%s (try `hygge catalog list %s` to see what's available)",
					providerID, modelID, providerID)
			}
			printCatalogEntry(cmd, e)
			return nil
		},
	}
}

// printCatalogEntry writes the full per-entry detail block.
func printCatalogEntry(cmd *cobra.Command, e catalog.Entry) {
	styles := newCLIStylesFor(out(cmd))
	printCLIField(cmd, styles, "provider:", e.Provider)
	printCLIField(cmd, styles, "id:", e.ID)
	if e.Name != "" {
		printCLIField(cmd, styles, "name:", e.Name)
	}
	printCLIField(cmd, styles, "source:", string(e.Source))
	writeln(out(cmd), "")
	printCLIField(cmd, styles, "context_window:", formatContext(e.Limit.ContextWindow))
	printCLIField(cmd, styles, "max_output:", formatContext(e.Limit.MaxOutput))
	writeln(out(cmd), "")
	writeln(out(cmd), styles.Header.Render("capabilities:"))
	printCLIField(cmd, styles, "  reasoning:", fmt.Sprintf("%v", e.Capabilities.Reasoning))
	printCLIField(cmd, styles, "  tool_calling:", fmt.Sprintf("%v", e.Capabilities.ToolCalling))
	printCLIField(cmd, styles, "  attachment:", fmt.Sprintf("%v", e.Capabilities.Attachment))
	printCLIField(cmd, styles, "  input_text:", fmt.Sprintf("%v", e.Capabilities.InputText))
	printCLIField(cmd, styles, "  input_images:", fmt.Sprintf("%v", e.Capabilities.InputImages))
	printCLIField(cmd, styles, "  output_text:", fmt.Sprintf("%v", e.Capabilities.OutputText))
	printCLIField(cmd, styles, "  output_images:", fmt.Sprintf("%v", e.Capabilities.OutputImages))
	writeln(out(cmd), "")
	writeln(out(cmd), styles.Header.Render("cost (USD per 1M tokens):"))
	printCLIField(cmd, styles, "  input:", formatMoney(e.Cost.Input))
	printCLIField(cmd, styles, "  output:", formatMoney(e.Cost.Output))
	printCLIField(cmd, styles, "  cache_read:", formatMoney(e.Cost.CacheRead))
	printCLIField(cmd, styles, "  cache_write:", formatMoney(e.Cost.CacheWrite))
}

// newCatalogRefreshCmd builds `hygge catalog refresh [--quiet]`.
func newCatalogRefreshCmd() *cobra.Command {
	var quiet bool
	c := &cobra.Command{
		Use:   "refresh",
		Short: "Pull a fresh snapshot from Catwalk and persist it",
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

			cat := rt.Catalog.Source()
			if cat == nil {
				return die(cmd, "no catalog available")
			}
			prev := cat.Loaded()
			res, err := cat.Refresh(cmd.Context())
			if err != nil {
				return fmt.Errorf("catalog refresh: %w", err)
			}
			if quiet {
				return nil
			}
			path := cat.StatePath()
			printf(out(cmd), "refreshed: %d providers / %d models", res.Providers, res.Models)
			if path != "" {
				printf(out(cmd), " (%s)", path)
			}
			printf(out(cmd), "\n")
			if !prev.FetchedAt.IsZero() {
				printf(out(cmd), "previous snapshot age: %s\n", formatAge(prev.Age))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&quiet, "quiet", false, "suppress success output (exit code still reflects failure)")
	return c
}

// formatCapabilities collapses the Capabilities struct into a
// short, comma-separated list of advertised features.  Used by the
// per-provider table.
func formatCapabilities(c catalog.Capabilities) string {
	flags := []string{}
	if c.Reasoning {
		flags = append(flags, "reasoning")
	}
	if c.ToolCalling {
		flags = append(flags, "tools")
	}
	if c.InputImages {
		flags = append(flags, "vision")
	}
	if c.Attachment {
		flags = append(flags, "attachments")
	}
	if c.OutputImages {
		flags = append(flags, "image-out")
	}
	sort.Strings(flags)
	if len(flags) == 0 {
		return "-"
	}
	return strings.Join(flags, ",")
}

// formatContext renders an integer context window as a human-friendly
// string ("200K", "1M") with a fallback to the raw number when the
// magnitude doesn't match a clean prefix.
func formatContext(n int64) string {
	if n <= 0 {
		return "-"
	}
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000 && n%1_000 == 0:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// formatMoney renders a USD-per-1M-tokens rate.  Returns "-" for zero
// (meaning "model doesn't charge for that token class").
func formatMoney(v float64) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprintf("$%g", v)
}

// formatAge renders a duration in a way that's readable for the user
// (e.g. "5h", "3d", "two-weeks").  Falls back to the duration's
// default String() for small values.
func formatAge(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	day := 24 * time.Hour
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		days := int(d / day)
		return fmt.Sprintf("%dd", days)
	}
}

// formatLoadedTime renders the snapshot timestamp.  Falls back to
// "embedded (no timestamp)" for zero times — that's the marker the
// loader leaves on the embedded snapshot.
func formatLoadedTime(t time.Time) string {
	if t.IsZero() {
		return "embedded"
	}
	return t.Format(time.RFC3339)
}

// splitProviderModel splits "<provider>/<model>" into its two parts.
// Returns ok=false when the input has no slash.
//
// The model id may itself contain slashes (e.g. openrouter ids are
// "openrouter/openai/o3-mini" if a user enters them with the
// "openrouter/" prefix); we split on the FIRST slash so the remainder
// keeps its namespaced form.
func splitProviderModel(ref string) (provider, model string, ok bool) {
	i := strings.Index(ref, "/")
	if i <= 0 || i == len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}
