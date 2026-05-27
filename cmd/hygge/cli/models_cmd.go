package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/catalog"
)

const defaultModelsOutputWidth = 96

// newModelsCmd builds `hygge models`, a friendlier model catalog browser for
// humans. Machine-oriented scripts can keep using `hygge catalog list`.
func newModelsCmd() *cobra.Command {
	var provider string
	var limit int

	cmd := &cobra.Command{
		Use:   "models [provider]",
		Short: "Browse available AI models",
		Long: `Browse the local Catwalk-backed model catalog.

By default, hygge groups models by provider. Pass a provider id or use
--provider to focus the list. For raw tabular output, use catalog list.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				provider = args[0]
			}
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
			availableProviders := authConfiguredProviders(rt.StateOpts)
			return printModels(cmd, cat, provider, limit, availableProviders)
		},
	}
	cmd.Flags().StringVarP(&provider, "provider", "p", "", "filter by provider id")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum models per provider (0 means all)")
	return cmd
}

func printModels(cmd *cobra.Command, cat *catalog.Catalog, providerID string, limit int, availableProviders []string) error {
	styles := modelsCLIStyles(out(cmd))
	loaded := cat.Loaded()
	available := providerSet(availableProviders)
	providers := filterCatalogProviders(cat.Providers(), available)
	if providerID != "" {
		if len(cat.Models(providerID)) == 0 {
			return die(cmd, "no models for provider %q", providerID)
		}
		if !available[providerID] {
			return die(cmd, "provider %q is not configured or authenticated (run `hygge provider auth %s`)", providerID, providerID)
		}
		providers = []string{providerID}
	}
	if len(providers) == 0 {
		writeln(out(cmd), "No configured or authenticated providers found.")
		writeln(out(cmd), "Run `hygge provider auth <provider>` to add credentials, or use `hygge catalog list` to inspect the full catalog.")
		return nil
	}

	modelCount := 0
	for _, p := range providers {
		modelCount += len(cat.Models(p))
	}

	printf(out(cmd), "%s\n", styles.Title.Render("Models"))
	printf(out(cmd), "%s\n\n", styles.Meta.Render(fmt.Sprintf(
		"%d available models across %d configured providers · catalog source %s · fetched %s · age %s",
		modelCount,
		len(providers),
		loaded.Source,
		formatLoadedTime(loaded.FetchedAt),
		formatAge(loaded.Age),
	)))

	for i, p := range providers {
		entries := cat.Models(p)
		if len(entries) == 0 {
			continue
		}
		if i > 0 {
			writeln(out(cmd))
		}
		printModelProviderSection(cmd, styles, p, entries, limit)
	}
	return nil
}

func providerSet(providers []string) map[string]bool {
	set := make(map[string]bool, len(providers))
	for _, p := range providers {
		p = strings.TrimSpace(p)
		if p != "" {
			set[p] = true
		}
	}
	return set
}

func filterCatalogProviders(providers []string, available map[string]bool) []string {
	if len(available) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(providers))
	for _, p := range providers {
		if available[p] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

type modelsStyles struct {
	Title       lipgloss.Style
	Meta        lipgloss.Style
	Section     lipgloss.Style
	Configured  lipgloss.Style
	Model       lipgloss.Style
	Detail      lipgloss.Style
	Capability  lipgloss.Style
	Capability2 lipgloss.Style
	Muted       lipgloss.Style
}

func modelsCLIStyles(w io.Writer) modelsStyles {
	plain := lipgloss.NewStyle()
	if !isColorWriter(w) {
		return modelsStyles{
			Title:       plain,
			Meta:        plain,
			Section:     plain,
			Configured:  plain,
			Model:       plain,
			Detail:      plain,
			Capability:  plain,
			Capability2: plain,
			Muted:       plain,
		}
	}
	// Colors are sourced from the shared inspectStyles palette so that
	// `hygge models` matches other CLI inspection commands (skills,
	// subagents, etc.) on terminals that support color.
	return modelsStyles{
		Title:       lipgloss.NewStyle().Bold(true).Underline(true).Foreground(inspectHeaderColor()),
		Meta:        lipgloss.NewStyle().Foreground(inspectMutedColor()),
		Section:     lipgloss.NewStyle().Bold(true).Foreground(inspectHeaderColor()),
		Configured:  lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")),
		Model:       lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")),
		Detail:      lipgloss.NewStyle().Foreground(inspectMutedColor()),
		Capability:  lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")),
		Capability2: lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")),
		Muted:       lipgloss.NewStyle().Foreground(inspectMutedColor()),
	}
}

func printModelProviderSection(cmd *cobra.Command, styles modelsStyles, providerID string, entries []catalog.Entry, limit int) {
	reasoning := 0
	for _, e := range entries {
		if e.Capabilities.Reasoning {
			reasoning++
		}
	}
	header := fmt.Sprintf("%s %s", providerID, styles.Muted.Render(fmt.Sprintf("%d models · %d reasoning", len(entries), reasoning)))
	printf(out(cmd), "%s\n", styles.Section.Render(header))

	shown := entries
	if limit > 0 && len(entries) > limit {
		shown = entries[:limit]
	}
	for _, e := range shown {
		printf(out(cmd), "%s\n", renderModelRow(styles, e, defaultModelsOutputWidth))
	}
	if len(shown) < len(entries) {
		printf(out(cmd), "  %s\n", styles.Muted.Render(fmt.Sprintf("… %d more (use --limit 0 to show all)", len(entries)-len(shown))))
	}
}

func renderModelRow(styles modelsStyles, e catalog.Entry, width int) string {
	name := e.Name
	if name == "" {
		name = e.ID
	}
	modelWidth := 42
	if width > 0 && width < defaultModelsOutputWidth {
		modelWidth = max(24, width/2)
	}
	model := ansi.Truncate(name, modelWidth, "…")
	if name != e.ID && e.ID != "" {
		model = fmt.Sprintf("%s %s", model, styles.Muted.Render(ansi.Truncate(e.ID, 24, "…")))
	}

	details := []string{
		formatContext(e.Limit.ContextWindow) + " ctx",
		formatModelCost(e),
	}
	if caps := formatModelBadges(styles, e.Capabilities); caps != "" {
		details = append(details, caps)
	}
	return fmt.Sprintf("  %s  %s", styles.Model.Render(model), styles.Detail.Render(strings.Join(details, "  ")))
}

func formatModelBadges(styles modelsStyles, c catalog.Capabilities) string {
	badges := []string{}
	if c.Reasoning {
		badges = append(badges, styles.Capability.Render("reasoning"))
	}
	if c.ToolCalling {
		badges = append(badges, styles.Capability2.Render("tools"))
	}
	if c.InputImages {
		badges = append(badges, styles.Capability2.Render("vision"))
	}
	if c.Attachment {
		badges = append(badges, styles.Capability2.Render("files"))
	}
	if c.OutputImages {
		badges = append(badges, styles.Capability2.Render("image-out"))
	}
	sort.Strings(badges)
	return strings.Join(badges, " ")
}

func formatModelCost(e catalog.Entry) string {
	input := formatMoney(e.Cost.Input)
	output := formatMoney(e.Cost.Output)
	if input == "-" && output == "-" {
		return "price n/a"
	}
	return fmt.Sprintf("%s/%s $/MTok", input, output)
}
