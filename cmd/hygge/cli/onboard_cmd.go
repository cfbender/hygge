package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/config"
)

type onboardOptions struct {
	provider string
	model    string
}

// newOnboardCmd builds `hygge onboard`, a first-run helper that writes the
// General agent's provider/model selection to the user's config file.
func newOnboardCmd() *cobra.Command {
	opts := onboardOptions{}
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Configure the General agent model",
		Long: `Configure the General agent model.

The selected provider and model are written to the user config at
$XDG_CONFIG_HOME/hygge/config.toml (normally ~/.config/hygge/config.toml).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOnboard(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.provider, "provider", "", "provider to use for the General agent")
	cmd.Flags().StringVar(&opts.model, "model", "", "model to use for the General agent")
	return cmd
}

func runOnboard(cmd *cobra.Command, opts onboardOptions) error {
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

	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	reader := bufio.NewReader(cmd.InOrStdin())

	providerName := strings.TrimSpace(opts.provider)
	if providerName == "" {
		providerName, err = pickProvider(cmd, reader, isTTY)
		if err != nil {
			return err
		}
	}
	if providerName == "" {
		return die(cmd, "provider is required (pass --provider or run interactively)")
	}

	modelName := strings.TrimSpace(opts.model)
	if modelName == "" {
		cat := rt.Catalog.Source()
		if cat == nil {
			return die(cmd, "model is required (pass --model; no catalog available to pick from)")
		}
		modelName, err = pickModel(cmd, reader, isTTY, cat, providerName)
		if err != nil {
			return err
		}
	}
	if modelName == "" {
		return die(cmd, "model is required (pass --model or run interactively)")
	}

	target, err := config.WriteModelSelection(config.WriteModelOptions{
		HomeDir:       rt.StateOpts.HomeDir,
		XDGConfigHome: rt.XDGConfigHome,
		Pwd:           rt.Pwd,
		Provenance:    rt.Provenance,
	}, providerName, modelName)
	if err != nil {
		return err
	}

	printf(out(cmd), "Configured General agent: %s/%s\n", providerName, modelName)
	printf(out(cmd), "Wrote user config: %s\n", target)
	return nil
}

func pickModel(cmd *cobra.Command, reader *bufio.Reader, isTTY bool, cat *catalog.Catalog, providerName string) (string, error) {
	entries := cat.Models(providerName)
	if len(entries) == 0 {
		if isTTY {
			printf(out(cmd), "No catalog models found for %s. Enter a model name: ", providerName)
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return "", fmt.Errorf("read model name: %w", err)
			}
			return strings.TrimSpace(line), nil
		}
		return "", die(cmd, "no catalog models found for provider %q; pass --model explicitly", providerName)
	}

	if isTTY {
		printf(out(cmd), "Models for %s:\n", providerName)
		tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
		for i, entry := range entries {
			printf(tw, "  %d)\t%s\t%s\n", i+1, entry.ID, formatContext(entry.Limit.ContextWindow))
		}
		if err := tw.Flush(); err != nil {
			return "", err
		}
		printRaw(out(cmd), "Pick a model (number or name): ")
	}

	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read model selection: %w", err)
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return "", nil
	}
	if n, convErr := strconv.Atoi(choice); convErr == nil {
		if n < 1 || n > len(entries) {
			return "", fmt.Errorf("selection %d out of range [1, %d]", n, len(entries))
		}
		return entries[n-1].ID, nil
	}
	return choice, nil
}
