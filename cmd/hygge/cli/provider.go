// Package cli — `hygge provider` subcommand group.
//
// Three subcommands manage the per-machine credential store:
//
//   - `hygge provider auth [name]` — interactively (or via stdin) save
//     an API key for a named provider.
//   - `hygge provider list` — print the stored credentials with masked
//     keys.
//   - `hygge provider remove <name>` — delete a stored credential.
//
// Credentials are persisted by [internal/auth] at
// $XDG_STATE_HOME/hygge/auth.json (mode 0600).  See that package for
// the storage contract.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/auth"
)

var errProviderAuthCancelled = errors.New("provider auth cancelled")

// newProviderCmd builds the `hygge provider` subcommand group.
func newProviderCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "provider",
		Short: "Manage per-machine provider credentials",
		Long: `Manage per-machine provider credentials.

Credentials live at $XDG_STATE_HOME/hygge/auth.json (mode 0600).  They
are intentionally separate from the human-edited TOML config so the
config can be checked into a dotfiles repository safely.`,
	}
	root.AddCommand(
		newProviderAuthCmd(),
		newProviderListCmd(),
		newProviderRemoveCmd(),
	)
	return root
}

// newProviderAuthCmd builds `hygge provider auth [name]`.
//
// Interactive flow:
//   - if name is omitted, open a searchable known-provider picker.
//   - if the provider has multiple supported auth methods, open a method picker.
//   - for API key: read once without echo via term.ReadPassword.
//
// Non-interactive flow (stdin is not a TTY):
//   - read a single line from stdin as the key.  Used by tests and
//     by `echo $KEY | hygge provider auth anthropic`.
func newProviderAuthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "auth [name]",
		Short: "Save an API key for a provider",
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

			authOpts := auth.LoadOptions{
				HomeDir:      rt.StateOpts.HomeDir,
				XDGStateHome: rt.StateOpts.XDGStateHome,
			}

			isTTY := term.IsTerminal(int(os.Stdin.Fd()))
			in := cmd.InOrStdin()
			reader := bufio.NewReader(in)

			// 1. Resolve the provider name.
			var name string
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}
			if name == "" {
				name, err = pickProvider(cmd, reader, isTTY)
				if errors.Is(err, errProviderAuthCancelled) {
					return nil
				}
				if err != nil {
					return err
				}
			}
			if name == "" {
				return die(cmd, "provider name is required")
			}

			// 2. Pick auth method. Non-TTY stdin always implies API key
			// because it is the only flow that does not need interactive input.
			method := authMethodAPIKey
			if isTTY {
				method, err = pickAuthMethod(cmd, reader, name, true)
				if errors.Is(err, errProviderAuthCancelled) {
					return nil
				}
				if err != nil {
					return err
				}
			}

			switch method {
			case authMethodAPIKey:
				return runProviderAuthAPIKey(cmd, reader, isTTY, name, authOpts)
			case authMethodOAuth:
				return runProviderAuthOAuth(cmd, name, authOpts)
			default:
				return die(cmd, "unknown auth method %q", method)
			}
		},
	}
}

type authMethod string

const (
	authMethodAPIKey authMethod = "api_key"
	authMethodOAuth  authMethod = "oauth"
)

type authMethodOption struct {
	method      authMethod
	title       string
	description string
}

func authMethodOptions(providerName string) []authMethodOption {
	options := []authMethodOption{{
		method:      authMethodAPIKey,
		title:       "API key",
		description: "Paste a provider API key; stored locally in the auth store.",
	}}
	if providerSupportsOAuth(providerName) {
		options = append(options, authMethodOption{
			method:      authMethodOAuth,
			title:       "OAuth",
			description: "Sign in with ChatGPT Pro/Plus for the OpenAI Codex endpoint.",
		})
	}
	return options
}

func providerSupportsOAuth(providerName string) bool {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "openai":
		return true
	default:
		return false
	}
}

func pickAuthMethod(cmd *cobra.Command, reader *bufio.Reader, providerName string, isTTY bool) (authMethod, error) {
	options := authMethodOptions(providerName)
	if len(options) == 1 {
		return options[0].method, nil
	}
	if isTTY {
		return pickAuthMethodInteractive(cmd, providerName, options)
	}
	return pickAuthMethodFromLine(reader, options)
}

func pickAuthMethodFromLine(reader *bufio.Reader, options []authMethodOption) (authMethod, error) {
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read auth method selection: %w", err)
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return options[0].method, nil
	}
	if n, convErr := strconv.Atoi(choice); convErr == nil {
		if n < 1 || n > len(options) {
			return "", fmt.Errorf("selection %d out of range [1, %d]", n, len(options))
		}
		return options[n-1].method, nil
	}
	choice = strings.ToLower(strings.ReplaceAll(choice, "-", "_"))
	for _, option := range options {
		if choice == string(option.method) || choice == strings.ToLower(option.title) {
			return option.method, nil
		}
	}
	return "", fmt.Errorf("auth method %q is not available for this provider", choice)
}

func pickAuthMethodInteractive(cmd *cobra.Command, providerName string, options []authMethodOption) (authMethod, error) {
	items := make([]list.Item, 0, len(options))
	for _, option := range options {
		items = append(items, authMethodSelectItem{option: option})
	}

	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(providerSelectAccentColor())
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(providerSelectMutedColor())

	// Height accounts for title (2 lines with bottom padding), help (2 lines
	// with top padding), and 2 lines per item, plus 1 line of breathing room.
	// This ensures all options are visible at once without pagination for the
	// typical small auth-method list (1-2 options).
	authMethodListHeight := len(items)*2 + 5
	l := list.New(items, delegate, 72, authMethodListHeight)
	l.Title = "Auth method for " + providerName
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(true)
	l.KeyMap.ForceQuit.SetHelp("ctrl+c", "cancel")
	l.AdditionalShortHelpKeys = func() []key.Binding { return nil }

	m := authMethodSelectModel{list: l}
	p := tea.NewProgram(m, tea.WithInput(cmd.InOrStdin()), tea.WithOutput(out(cmd)))
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("auth method picker: %w", err)
	}
	selected, ok := finalModel.(authMethodSelectModel)
	if !ok || selected.cancelled || selected.choice == "" {
		writeln(out(cmd), "Cancelled.")
		return "", errProviderAuthCancelled
	}
	return selected.choice, nil
}

type authMethodSelectItem struct {
	option authMethodOption
}

func (i authMethodSelectItem) FilterValue() string { return i.option.title }
func (i authMethodSelectItem) Title() string       { return i.option.title }
func (i authMethodSelectItem) Description() string { return i.option.description }

type authMethodSelectModel struct {
	list      list.Model
	choice    authMethod
	cancelled bool
}

func (m authMethodSelectModel) Init() tea.Cmd { return nil }

func (m authMethodSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.Keystroke() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if item, ok := m.list.SelectedItem().(authMethodSelectItem); ok {
				m.choice = item.option.method
				return m, tea.Quit
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m authMethodSelectModel) View() tea.View {
	return tea.NewView(m.list.View())
}

// runProviderAuthOAuth runs the OpenAI Codex device authorization flow.
func runProviderAuthOAuth(cmd *cobra.Command, name string, authOpts auth.LoadOptions) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	printf(out(cmd), "Starting device authorization...\n")
	device, err := auth.StartDeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("start device auth: %w", err)
	}

	printf(out(cmd), "\nOpen this URL:  %s\nEnter code:     %s\n\nWaiting for authorization...\n", device.VerifyURL, device.UserCode)

	tokens, err := auth.PollDeviceAuth(ctx, device.DeviceAuthID, device.UserCode, device.Interval)
	if err != nil {
		return fmt.Errorf("device auth: %w", err)
	}

	accountID := auth.ExtractAccountID(tokens.AccessToken)
	if accountID == "" {
		accountID = auth.ExtractAccountID(tokens.IDToken)
	}

	expiresAt := time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	cred := auth.Credential{
		Type:         auth.CredOAuth,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    expiresAt,
		AccountID:    accountID,
		AddedAt:      time.Now(),
	}
	if err := auth.Set(name, cred, authOpts); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	printf(out(cmd), "Saved OAuth credential for %s.\n", name)
	return nil
}

// runProviderAuthAPIKey handles the "paste an API key" branch of
// `hygge provider auth`.
func runProviderAuthAPIKey(cmd *cobra.Command, reader *bufio.Reader, isTTY bool, name string, authOpts auth.LoadOptions) error {
	var key string
	if isTTY {
		printf(out(cmd), "Paste your %s API key (hidden input, press Enter when done): ", name)
		// term.ReadPassword reads directly from the FD — there's no
		// way to redirect it to a *bufio.Reader, so non-TTY callers
		// must use the stdin-pipe branch below.
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		// term.ReadPassword does not echo a newline; print one so
		// subsequent output starts on its own line.
		writeln(out(cmd))
		key = strings.TrimSpace(string(raw))
	} else {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("read stdin: %w", err)
		}
		key = strings.TrimSpace(line)
	}

	if key == "" {
		return die(cmd, "empty API key — aborted")
	}

	cred := auth.Credential{
		Type:    auth.CredAPIKey,
		APIKey:  key,
		AddedAt: time.Now(),
	}
	if err := auth.Set(name, cred, authOpts); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	printf(out(cmd), "Saved credential for %s. (%s)\n", name, maskKey(key))
	return nil
}

// pickProvider resolves a provider name. In a TTY it opens a searchable list;
// in non-interactive flows it preserves the line-oriented number-or-name input
// used by tests and shell pipelines.
func pickProvider(cmd *cobra.Command, reader *bufio.Reader, isTTY bool) (string, error) {
	names := knownProviders()
	if isTTY {
		return pickProviderInteractive(cmd, names)
	}
	return pickProviderFromLine(reader, names)
}

func pickProviderFromLine(reader *bufio.Reader, names []string) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read provider selection: %w", err)
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return "", nil
	}
	if n, convErr := strconv.Atoi(choice); convErr == nil {
		if n < 1 || n > len(names) {
			return "", fmt.Errorf("selection %d out of range [1, %d]", n, len(names))
		}
		return names[n-1], nil
	}
	return choice, nil
}

func pickProviderInteractive(cmd *cobra.Command, names []string) (string, error) {
	items := make([]list.Item, 0, len(names))
	for _, name := range names {
		items = append(items, providerSelectItem{name: name})
	}

	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(providerSelectAccentColor())
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(providerSelectMutedColor())

	l := list.New(items, delegate, 72, min(18, max(8, len(items)+4)))
	l.Title = "Choose a provider"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)
	l.KeyMap.ForceQuit.SetHelp("ctrl+c", "cancel")
	l.AdditionalShortHelpKeys = func() []key.Binding { return nil }

	m := providerSelectModel{list: l}
	p := tea.NewProgram(m, tea.WithInput(cmd.InOrStdin()), tea.WithOutput(out(cmd)))
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("provider picker: %w", err)
	}
	selected, ok := finalModel.(providerSelectModel)
	if !ok || selected.cancelled || selected.choice == "" {
		writeln(out(cmd), "Cancelled.")
		return "", errProviderAuthCancelled
	}
	return selected.choice, nil
}

type providerSelectItem struct {
	name string
}

func (i providerSelectItem) FilterValue() string { return i.name }
func (i providerSelectItem) Title() string       { return i.name }
func (i providerSelectItem) Description() string {
	if env := providerEnvVar(i.name); env != "" {
		return "uses " + env
	}
	return "custom provider credential"
}

type providerSelectModel struct {
	list      list.Model
	choice    string
	cancelled bool
}

func (m providerSelectModel) Init() tea.Cmd { return nil }

func (m providerSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.Keystroke() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if item, ok := m.list.SelectedItem().(providerSelectItem); ok {
				m.choice = item.name
				return m, tea.Quit
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m providerSelectModel) View() tea.View {
	return tea.NewView(m.list.View())
}

func providerSelectAccentColor() color.Color {
	return cliAccentColor()
}

func providerSelectMutedColor() color.Color {
	return cliMutedColor()
}

// newProviderListCmd builds `hygge provider list`.
func newProviderListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored credentials with masked keys",
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

			authOpts := auth.LoadOptions{
				HomeDir:      rt.StateOpts.HomeDir,
				XDGStateHome: rt.StateOpts.XDGStateHome,
			}
			store, err := auth.Load(authOpts)
			if err != nil {
				return fmt.Errorf("load auth store: %w", err)
			}
			names := store.List()
			if len(names) == 0 {
				writeln(out(cmd), "(no providers configured — run hygge provider auth)")
				return nil
			}

			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			printf(tw, "NAME\tTYPE\tKEY\tADDED\n")
			for _, n := range names {
				cred, _ := store.Get(n)
				display := "<oauth>"
				if cred.Type == auth.CredAPIKey {
					display = maskKey(cred.APIKey)
				}
				added := cred.AddedAt.Format("2006-01-02")
				printf(tw, "%s\t%s\t%s\t%s\n", n, cred.Type, display, added)
			}
			return tw.Flush()
		},
	}
}

// newProviderRemoveCmd builds `hygge provider remove <name>`.
func newProviderRemoveCmd() *cobra.Command {
	var noConfirm bool
	c := &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a stored credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
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

			authOpts := auth.LoadOptions{
				HomeDir:      rt.StateOpts.HomeDir,
				XDGStateHome: rt.StateOpts.XDGStateHome,
			}
			store, err := auth.Load(authOpts)
			if err != nil {
				return fmt.Errorf("load auth store: %w", err)
			}
			if _, ok := store.Get(name); !ok {
				printf(out(cmd), "(no credential for %s; nothing to do)\n", name)
				return nil
			}

			if !noConfirm {
				printf(out(cmd), "Remove credential for %s? [y/N]: ", name)
				reader := bufio.NewReader(cmd.InOrStdin())
				line, _ := reader.ReadString('\n')
				ans := strings.ToLower(strings.TrimSpace(line))
				if ans != "y" && ans != "yes" {
					writeln(out(cmd), "Cancelled.")
					return nil
				}
			}

			if err := auth.Remove(name, authOpts); err != nil {
				return fmt.Errorf("remove credential: %w", err)
			}
			printf(out(cmd), "Removed credential for %s\n", name)
			return nil
		},
	}
	c.Flags().BoolVarP(&noConfirm, "no-confirm", "f", false, "skip the confirmation prompt")
	return c
}

// providerKnownContains reports whether name is in the known-providers
// list.  Kept as a small helper so tests can assert against the same
// list the picker uses.
func providerKnownContains(name string) bool {
	return slices.Contains(knownProviders(), name)
}
