// Package cli — `hygge mcp add` subcommand.
//
// `hygge mcp add [name]` runs an interactive (or non-interactive) flow
// to configure a new MCP server and persists the result:
//
//   - Server config (name, transport, command/url, etc.) is written to
//     either project-scope .hygge/mcp.toml or global-scope
//     $XDG_CONFIG_HOME/hygge/mcp.toml.
//   - Auth headers/tokens are NOT written to mcp.toml.  Instead they
//     are stored in $XDG_STATE_HOME/hygge/mcp-auth.json and referenced
//     via $VAR placeholders in the mcp.toml headers map.
//
// Non-interactive seam: when stdin is not a TTY the flow reads one
// decision per line from stdin, matching the provider auth pattern.
// Tests use this seam exclusively.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/mcp"
)

type mcpAddScope string

const (
	mcpAddScopeProject mcpAddScope = "project"
	mcpAddScopeGlobal  mcpAddScope = "global"
)

var errMCPAddScopeCancelled = errors.New("mcp add scope selection cancelled")

// newMCPAddCmd builds `hygge mcp add [name]`.
func newMCPAddCmd() *cobra.Command {
	var scopeFlag string
	cmd := &cobra.Command{
		Use:   "add [name]",
		Short: "Add a new MCP server to the user or project config",
		Long: `Interactively configure a new MCP server and persist it to a Hygge MCP config.

Server configuration is written to either:
  - project scope: <project>/.hygge/mcp.toml
  - global scope:  $XDG_CONFIG_HOME/hygge/mcp.toml

Auth material (API keys, bearer tokens) is stored separately in
$XDG_STATE_HOME/hygge/mcp-auth.json (mode 0600) and referenced via
environment-variable placeholders in the config file.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			isTTY := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))

			var initialName string
			if len(args) == 1 {
				initialName = strings.TrimSpace(args[0])
			}

			var scope mcpAddScope
			var scopePath string
			var spec mcp.AppendServerSpec
			var authHeaders map[string]string

			if isTTY {
				req, err := runMCPAddWizard(cmd, initialName, scopeFlag)
				if err != nil {
					return err
				}
				scope = req.scope
				scopePath = req.scopePath
				spec = req.spec
				authHeaders = req.authHeaders
			} else {
				reader := bufio.NewReader(cmd.InOrStdin())
				var err error
				scope, scopePath, err = resolveMCPAddScope(cmd, scopeFlag, false)
				if err != nil {
					return err
				}
				spec, authHeaders, err = readMCPAddNonInteractive(cmd, reader, initialName)
				if err != nil {
					return err
				}
			}

			return writeMCPAddConfig(cmd, scope, scopePath, spec, authHeaders)
		},
	}
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "config scope to write: project or global")
	return cmd
}

func resolveMCPAddScope(cmd *cobra.Command, raw string, isTTY bool) (mcpAddScope, string, error) {
	scope, err := parseMCPAddScope(raw)
	if err != nil {
		return "", "", err
	}
	if scope == "" {
		if !isTTY {
			return "", "", die(cmd, "--scope is required when not running interactively; use --scope project or --scope global")
		}
		return "", "", fmt.Errorf("interactive scope selection is handled by the mcp add wizard")
	}
	path := mcpAddScopePath(scope)
	return scope, path, nil
}

func parseMCPAddScope(raw string) (mcpAddScope, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case string(mcpAddScopeProject):
		return mcpAddScopeProject, nil
	case string(mcpAddScopeGlobal):
		return mcpAddScopeGlobal, nil
	default:
		return "", fmt.Errorf("invalid --scope %q; supported: project, global", raw)
	}
}

func mcpAddScopePath(scope mcpAddScope) string {
	if scope == mcpAddScopeGlobal {
		return filepath.Join(mcpXDGConfig(), "hygge", "mcp.toml")
	}
	return filepath.Join(mcpPwd(), ".hygge", "mcp.toml")
}

type mcpAddRequest struct {
	scope       mcpAddScope
	scopePath   string
	spec        mcp.AppendServerSpec
	authHeaders map[string]string
}

func readMCPAddNonInteractive(cmd *cobra.Command, reader *bufio.Reader, initialName string) (mcp.AppendServerSpec, map[string]string, error) {
	name := strings.TrimSpace(initialName)
	if name == "" {
		var err error
		name, err = mcpAddPromptLine(cmd, reader, false, "")
		if err != nil {
			return mcp.AppendServerSpec{}, nil, fmt.Errorf("read name: %w", err)
		}
		name = strings.TrimSpace(name)
	}
	if name == "" {
		return mcp.AppendServerSpec{}, nil, die(cmd, "server name is required")
	}

	transportRaw, err := mcpAddPromptLine(cmd, reader, false, "")
	if err != nil {
		return mcp.AppendServerSpec{}, nil, fmt.Errorf("read transport: %w", err)
	}
	transport := strings.ToLower(strings.TrimSpace(transportRaw))
	if transport == "" {
		transport = "stdio"
	}
	if !mcpAddValidTransport(transport) {
		return mcp.AppendServerSpec{}, nil, die(cmd, "unknown transport %q; supported: stdio, sse, http", transport)
	}

	spec := mcp.AppendServerSpec{Name: name, Transport: transport}
	switch transport {
	case "stdio":
		cmdStr, err := mcpAddPromptLine(cmd, reader, false, "")
		if err != nil {
			return mcp.AppendServerSpec{}, nil, fmt.Errorf("read command: %w", err)
		}
		cmdStr = strings.TrimSpace(cmdStr)
		if cmdStr == "" {
			return mcp.AppendServerSpec{}, nil, die(cmd, "command is required for stdio transport")
		}
		spec.Command = cmdStr

		argsRaw, err := mcpAddPromptLine(cmd, reader, false, "")
		if err != nil {
			return mcp.AppendServerSpec{}, nil, fmt.Errorf("read args: %w", err)
		}
		if argsRaw = strings.TrimSpace(argsRaw); argsRaw != "" {
			spec.Args = strings.Fields(argsRaw)
		}
	case "sse", "http":
		urlStr, err := mcpAddPromptLine(cmd, reader, false, "")
		if err != nil {
			return mcp.AppendServerSpec{}, nil, fmt.Errorf("read url: %w", err)
		}
		urlStr = strings.TrimSpace(urlStr)
		if urlStr == "" {
			return mcp.AppendServerSpec{}, nil, die(cmd, "url is required for %s transport", transport)
		}
		spec.URL = urlStr
	}

	authHeaders, err := mcpAddResolveAuthHeaders(name, spec.Transport, "", "")
	if err != nil {
		return mcp.AppendServerSpec{}, nil, err
	}
	if spec.Transport == "sse" || spec.Transport == "http" {
		headerNameRaw, err := mcpAddPromptLine(cmd, reader, false, "")
		if err != nil {
			return mcp.AppendServerSpec{}, nil, fmt.Errorf("read auth header name: %w", err)
		}
		headerName := strings.TrimSpace(headerNameRaw)
		if headerName != "" {
			headerVal, err := mcpAddPromptLine(cmd, reader, false, "")
			if err != nil {
				return mcp.AppendServerSpec{}, nil, fmt.Errorf("read auth header value: %w", err)
			}
			authHeaders, err = mcpAddResolveAuthHeaders(name, spec.Transport, headerName, strings.TrimSpace(headerVal))
			if err != nil {
				return mcp.AppendServerSpec{}, nil, err
			}
			if len(authHeaders) > 0 {
				spec.Headers = map[string]string{headerName: "$" + mcpAuthEnvVar(name, headerName)}
			}
		}
	}

	return spec, authHeaders, nil
}

func mcpAddValidTransport(transport string) bool {
	switch transport {
	case "stdio", "sse", "http":
		return true
	default:
		return false
	}
}

func mcpAddResolveAuthHeaders(name, transport, headerName, headerValue string) (map[string]string, error) {
	authHeaders := map[string]string{}
	if transport != "sse" && transport != "http" {
		return authHeaders, nil
	}
	headerName = strings.TrimSpace(headerName)
	if headerName == "" {
		return authHeaders, nil
	}
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return nil, fmt.Errorf("auth header value cannot be empty")
	}
	authOpts := mcp.AuthLoadOptions{HomeDir: mcpHomeDir(), XDGStateHome: mcpXDGStateHome()}
	envVar := mcpAuthEnvVar(name, headerName)
	owner, err := mcpAuthEnvVarOwner(envVar, authOpts)
	if err != nil {
		return nil, fmt.Errorf("inspect mcp-auth.json: %w", err)
	}
	if owner != "" && owner != name {
		return nil, fmt.Errorf("auth placeholder %s would collide with existing MCP server %q; choose a less ambiguous server name", envVar, owner)
	}
	authHeaders[headerName] = headerValue
	return authHeaders, nil
}

func writeMCPAddConfig(cmd *cobra.Command, scope mcpAddScope, scopePath string, spec mcp.AppendServerSpec, authHeaders map[string]string) error {
	// Persist auth first so that if this step fails we have not yet written
	// the server entry to mcp.toml — avoiding a partial state where the
	// config references a $VAR placeholder that is never populated.
	var authPath string
	if len(authHeaders) > 0 {
		authOpts := mcp.AuthLoadOptions{HomeDir: mcpHomeDir(), XDGStateHome: mcpXDGStateHome()}
		if err := mcp.SetAuth(spec.Name, mcp.AuthEntry{Headers: authHeaders}, authOpts); err != nil {
			return fmt.Errorf("write mcp-auth.json: %w", err)
		}
		authPath, _ = mcp.AuthPath(authOpts)
	}

	if err := mcp.AppendServer(mcp.AppendServerOptions{Path: scopePath, Server: spec}); err != nil {
		return fmt.Errorf("write mcp.toml: %w", err)
	}
	printf(out(cmd), "✓ Added MCP server %q to %s config\n", spec.Name, scope)
	printf(out(cmd), "  %s\n", scopePath)

	if authPath != "" {
		printf(out(cmd), "Auth header(s) for %q saved to %s\n", spec.Name, authPath)
		printf(out(cmd), "Hygge will source these values from mcp-auth.json at startup.\n")
	}
	return nil
}

type mcpAddWizardStep int

const (
	mcpAddWizardStepScope mcpAddWizardStep = iota
	mcpAddWizardStepName
	mcpAddWizardStepTransport
	mcpAddWizardStepCommand
	mcpAddWizardStepArgs
	mcpAddWizardStepURL
	mcpAddWizardStepAuthName
	mcpAddWizardStepAuthValue
)

type mcpAddWizardModel struct {
	step         mcpAddWizardStep
	input        textinput.Model
	choiceCursor int
	initialName  string
	scopeSet     bool
	scope        mcpAddScope
	name         string
	transport    string
	command      string
	args         string
	url          string
	authName     string
	authValue    string
	done         bool
	cancelled    bool
	err          string
}

func newMCPAddWizardModel(initialName string, scope mcpAddScope) mcpAddWizardModel {
	input := textinput.New()
	input.Prompt = "› "
	input.SetWidth(72)
	m := mcpAddWizardModel{input: input, initialName: strings.TrimSpace(initialName), scope: scope, scopeSet: scope != ""}
	if m.scopeSet && m.initialName != "" {
		m.step = mcpAddWizardStepTransport
	} else if m.scopeSet {
		m.step = mcpAddWizardStepName
	} else {
		m.step = mcpAddWizardStepScope
	}
	m.prepareInput()
	return m
}

func (m mcpAddWizardModel) Init() tea.Cmd { return m.input.Focus() }

func (m mcpAddWizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.Keystroke() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.isChoiceStep() {
				m.moveChoice(-1)
				return m, nil
			}
		case "down", "j":
			if m.isChoiceStep() {
				m.moveChoice(1)
				return m, nil
			}
		case "enter":
			return m.advance()
		}
	}
	if m.isChoiceStep() {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m mcpAddWizardModel) View() tea.View {
	if m.done || m.cancelled {
		return tea.NewView("")
	}
	accent := lipgloss.NewStyle().Foreground(providerSelectAccentColor()).Bold(true)
	muted := lipgloss.NewStyle().Foreground(providerSelectMutedColor())
	var b strings.Builder
	b.WriteString(accent.Render("Add MCP server"))
	b.WriteString("\n")
	b.WriteString(m.progressText())
	b.WriteString("\n\n")
	b.WriteString(m.promptText())
	b.WriteString("\n")
	if m.isChoiceStep() {
		b.WriteString(m.choiceView())
	} else {
		b.WriteString(m.input.View())
	}
	if m.err != "" {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(providerSelectAccentColor()).Render(m.err))
	}
	b.WriteString("\n\n")
	if m.isChoiceStep() {
		b.WriteString(muted.Render("↑/↓ or j/k: choose • enter: select • esc/ctrl+c: cancel"))
	} else {
		b.WriteString(muted.Render("enter: continue • esc/ctrl+c: cancel"))
	}
	return tea.NewView(b.String())
}

func (m mcpAddWizardModel) isChoiceStep() bool {
	return m.step == mcpAddWizardStepScope || m.step == mcpAddWizardStepTransport
}

func (m *mcpAddWizardModel) moveChoice(delta int) {
	choices := m.choiceOptions()
	if len(choices) == 0 {
		return
	}
	m.choiceCursor = (m.choiceCursor + delta + len(choices)) % len(choices)
}

type mcpAddWizardChoice struct {
	value       string
	title       string
	description string
}

func (m mcpAddWizardModel) choiceOptions() []mcpAddWizardChoice {
	switch m.step {
	case mcpAddWizardStepScope:
		return []mcpAddWizardChoice{
			{value: string(mcpAddScopeProject), title: "Project", description: filepath.Join(mcpPwd(), ".hygge", "mcp.toml") + " — available in this workspace"},
			{value: string(mcpAddScopeGlobal), title: "Global", description: filepath.Join(mcpXDGConfig(), "hygge", "mcp.toml") + " — available across Hygge workspaces"},
		}
	case mcpAddWizardStepTransport:
		return []mcpAddWizardChoice{
			{value: "stdio", title: "stdio", description: "Run a local command over stdin/stdout"},
			{value: "http", title: "http", description: "Connect to a Streamable HTTP endpoint"},
			{value: "sse", title: "sse", description: "Connect to a server-sent events endpoint"},
		}
	default:
		return nil
	}
}

func (m mcpAddWizardModel) selectedChoiceValue() string {
	choices := m.choiceOptions()
	if len(choices) == 0 {
		return ""
	}
	idx := m.choiceCursor
	if idx < 0 || idx >= len(choices) {
		idx = 0
	}
	return choices[idx].value
}

func (m mcpAddWizardModel) choiceView() string {
	choices := m.choiceOptions()
	accent := lipgloss.NewStyle().Foreground(providerSelectAccentColor()).Bold(true)
	muted := lipgloss.NewStyle().Foreground(providerSelectMutedColor())
	var b strings.Builder
	for i, choice := range choices {
		if i > 0 {
			b.WriteByte('\n')
		}
		cursor := "  "
		title := choice.title
		if i == m.choiceCursor {
			cursor = "› "
			title = accent.Render(title)
		}
		b.WriteString(cursor)
		b.WriteString(title)
		b.WriteString("\n    ")
		b.WriteString(muted.Render(choice.description))
	}
	return b.String()
}

func (m mcpAddWizardModel) progressText() string {
	parts := []string{}
	if m.scope != "" {
		parts = append(parts, "scope: "+string(m.scope))
	}
	if m.name != "" || m.initialName != "" {
		name := m.name
		if name == "" {
			name = m.initialName
		}
		parts = append(parts, "server: "+name)
	}
	if m.transport != "" {
		parts = append(parts, "transport: "+m.transport)
	}
	if len(parts) == 0 {
		return "Configure where and how Hygge should start this MCP server."
	}
	return strings.Join(parts, " · ")
}

func (m mcpAddWizardModel) promptText() string {
	switch m.step {
	case mcpAddWizardStepScope:
		return "Where should this server be saved?"
	case mcpAddWizardStepName:
		return "Server name"
	case mcpAddWizardStepTransport:
		return "Transport: stdio, sse, or http (blank = stdio)"
	case mcpAddWizardStepCommand:
		return "Command / binary to execute"
	case mcpAddWizardStepArgs:
		return "Args (space-separated, blank for none)"
	case mcpAddWizardStepURL:
		return "URL"
	case mcpAddWizardStepAuthName:
		return "Auth header name (for example Authorization, blank to skip)"
	case mcpAddWizardStepAuthValue:
		return "Auth header value (hidden)"
	default:
		return ""
	}
}

func (m mcpAddWizardModel) advance() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	m.err = ""
	switch m.step {
	case mcpAddWizardStepScope:
		scope, err := parseMCPAddScope(m.selectedChoiceValue())
		if err != nil || scope == "" {
			m.err = "Choose project or global."
			return m, nil
		}
		m.scope = scope
		m.choiceCursor = 0
		if m.initialName != "" {
			m.step = mcpAddWizardStepTransport
		} else {
			m.step = mcpAddWizardStepName
		}
	case mcpAddWizardStepName:
		if value == "" {
			m.err = "Server name is required."
			return m, nil
		}
		m.name = value
		m.step = mcpAddWizardStepTransport
	case mcpAddWizardStepTransport:
		value = m.selectedChoiceValue()
		if !mcpAddValidTransport(value) {
			m.err = "Transport must be stdio, sse, or http."
			return m, nil
		}
		m.transport = value
		m.choiceCursor = 0
		if value == "stdio" {
			m.step = mcpAddWizardStepCommand
		} else {
			m.step = mcpAddWizardStepURL
		}
	case mcpAddWizardStepCommand:
		if value == "" {
			m.err = "Command is required for stdio transport."
			return m, nil
		}
		m.command = value
		m.step = mcpAddWizardStepArgs
	case mcpAddWizardStepArgs:
		m.args = value
		m.done = true
		return m, tea.Quit
	case mcpAddWizardStepURL:
		if value == "" {
			m.err = "URL is required."
			return m, nil
		}
		m.url = value
		m.step = mcpAddWizardStepAuthName
	case mcpAddWizardStepAuthName:
		m.authName = value
		if value == "" {
			m.done = true
			return m, tea.Quit
		}
		m.step = mcpAddWizardStepAuthValue
	case mcpAddWizardStepAuthValue:
		if value == "" {
			m.err = "Auth header value cannot be empty."
			return m, nil
		}
		m.authValue = value
		m.done = true
		return m, tea.Quit
	}
	m.prepareInput()
	return m, m.input.Focus()
}

func (m *mcpAddWizardModel) prepareInput() {
	m.input.Reset()
	m.input.EchoMode = textinput.EchoNormal
	m.input.Placeholder = ""
	if m.isChoiceStep() {
		return
	}
	switch m.step {
	case mcpAddWizardStepCommand:
		m.input.Placeholder = "npx"
	case mcpAddWizardStepArgs:
		m.input.Placeholder = "-y @modelcontextprotocol/server-filesystem ."
	case mcpAddWizardStepURL:
		m.input.Placeholder = "https://example.com/mcp"
	case mcpAddWizardStepAuthName:
		m.input.Placeholder = "Authorization"
	case mcpAddWizardStepAuthValue:
		m.input.EchoMode = textinput.EchoPassword
		m.input.Placeholder = "Bearer ..."
	}
	m.input.CursorEnd()
}

func (m mcpAddWizardModel) request() (mcpAddRequest, error) {
	name := strings.TrimSpace(m.name)
	if name == "" {
		name = strings.TrimSpace(m.initialName)
	}
	if name == "" {
		return mcpAddRequest{}, fmt.Errorf("server name is required")
	}
	transport := m.transport
	if transport == "" {
		transport = "stdio"
	}
	spec := mcp.AppendServerSpec{Name: name, Transport: transport}
	switch transport {
	case "stdio":
		if strings.TrimSpace(m.command) == "" {
			return mcpAddRequest{}, fmt.Errorf("command is required for stdio transport")
		}
		spec.Command = strings.TrimSpace(m.command)
		if args := strings.TrimSpace(m.args); args != "" {
			spec.Args = strings.Fields(args)
		}
	case "sse", "http":
		if strings.TrimSpace(m.url) == "" {
			return mcpAddRequest{}, fmt.Errorf("url is required for %s transport", transport)
		}
		spec.URL = strings.TrimSpace(m.url)
	default:
		return mcpAddRequest{}, fmt.Errorf("unknown transport %q; supported: stdio, sse, http", transport)
	}
	authHeaders, err := mcpAddResolveAuthHeaders(name, transport, m.authName, m.authValue)
	if err != nil {
		return mcpAddRequest{}, err
	}
	if len(authHeaders) > 0 {
		spec.Headers = map[string]string{strings.TrimSpace(m.authName): "$" + mcpAuthEnvVar(name, m.authName)}
	}
	return mcpAddRequest{scope: m.scope, scopePath: mcpAddScopePath(m.scope), spec: spec, authHeaders: authHeaders}, nil
}

func runMCPAddWizard(cmd *cobra.Command, initialName, scopeRaw string) (mcpAddRequest, error) {
	scope, err := parseMCPAddScope(scopeRaw)
	if err != nil {
		return mcpAddRequest{}, err
	}
	p := tea.NewProgram(newMCPAddWizardModel(initialName, scope), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(out(cmd)))
	finalModel, err := p.Run()
	if err != nil {
		return mcpAddRequest{}, fmt.Errorf("mcp add wizard: %w", err)
	}
	selected, ok := finalModel.(mcpAddWizardModel)
	if !ok || selected.cancelled || !selected.done {
		writeln(out(cmd), "Cancelled.")
		return mcpAddRequest{}, errMCPAddScopeCancelled
	}
	return selected.request()
}

// mcpXDGStateHome resolves the XDG state home for mcp-auth.json,
// honouring testOverrides.
func mcpXDGStateHome() string {
	if testOverrides != nil && testOverrides.XDGStateHome != "" {
		return testOverrides.XDGStateHome
	}
	if v, ok := os.LookupEnv("XDG_STATE_HOME"); ok && v != "" {
		return v
	}
	return filepath.Join(mcpHomeDir(), ".local", "state")
}

// mcpAuthEnvVar returns the canonical env-var name for a specific
// MCP server + header combination.  Example:
//
//	server "my-server", header "Authorization" → HYGGE_MCP_MY_SERVER_AUTHORIZATION
func mcpAuthEnvVar(serverName, headerName string) string {
	sanitize := func(s string) string {
		s = strings.ToUpper(s)
		var b strings.Builder
		for _, r := range s {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			} else {
				b.WriteRune('_')
			}
		}
		return b.String()
	}
	return "HYGGE_MCP_" + sanitize(serverName) + "_" + sanitize(headerName)
}

func mcpAuthEnvVarOwner(envVar string, opts mcp.AuthLoadOptions) (string, error) {
	store, err := mcp.LoadAuth(opts)
	if err != nil {
		return "", err
	}
	owners := make([]string, 0, len(store.Servers))
	for server, entry := range store.Servers {
		for header := range entry.Headers {
			if mcpAuthEnvVar(server, header) == envVar {
				owners = append(owners, server)
			}
		}
	}
	sort.Strings(owners)
	if len(owners) == 0 {
		return "", nil
	}
	return owners[0], nil
}

// mcpAddPromptLine prints prompt (when TTY) and reads a line from
// reader.  On EOF, returns the partial line with no error.
func mcpAddPromptLine(cmd *cobra.Command, reader *bufio.Reader, isTTY bool, prompt string) (string, error) {
	if isTTY {
		printf(out(cmd), "%s", prompt)
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
