// Package cli — `hygge mcp add` subcommand.
//
// `hygge mcp add [name]` runs an interactive (or non-interactive) flow
// to configure a new MCP server and persists the result:
//
//   - Server config (name, transport, command/url, etc.) is written to
//     the user-scope mcp.toml: $XDG_CONFIG_HOME/hygge/mcp.toml.
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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/mcp"
)

// newMCPAddCmd builds `hygge mcp add [name]`.
func newMCPAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [name]",
		Short: "Add a new MCP server to the user config",
		Long: `Interactively configure a new MCP server and persist it to the user config.

Server configuration is written to $XDG_CONFIG_HOME/hygge/mcp.toml.
Auth material (API keys, bearer tokens) is stored separately in
$XDG_STATE_HOME/hygge/mcp-auth.json (mode 0600) and referenced via
environment-variable placeholders in the config file.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			isTTY := term.IsTerminal(int(os.Stdin.Fd()))
			reader := bufio.NewReader(cmd.InOrStdin())

			// --- 1. Resolve name ---
			var name string
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}
			if name == "" {
				var err error
				name, err = mcpAddPromptLine(cmd, reader, isTTY, "Server name: ")
				if err != nil {
					return fmt.Errorf("read name: %w", err)
				}
				name = strings.TrimSpace(name)
			}
			if name == "" {
				return die(cmd, "server name is required")
			}

			// --- 2. Transport ---
			transportRaw, err := mcpAddPromptLine(cmd, reader, isTTY, "Transport [stdio/sse/http] (default: stdio): ")
			if err != nil {
				return fmt.Errorf("read transport: %w", err)
			}
			transport := strings.ToLower(strings.TrimSpace(transportRaw))
			if transport == "" {
				transport = "stdio"
			}
			switch transport {
			case "stdio", "sse", "http":
			default:
				return die(cmd, "unknown transport %q; supported: stdio, sse, http", transport)
			}

			spec := mcp.AppendServerSpec{
				Name:      name,
				Transport: transport,
			}

			// --- 3. Transport-specific fields ---
			switch transport {
			case "stdio":
				cmdStr, err := mcpAddPromptLine(cmd, reader, isTTY, "Command (binary to execute): ")
				if err != nil {
					return fmt.Errorf("read command: %w", err)
				}
				cmdStr = strings.TrimSpace(cmdStr)
				if cmdStr == "" {
					return die(cmd, "command is required for stdio transport")
				}
				spec.Command = cmdStr

				argsRaw, err := mcpAddPromptLine(cmd, reader, isTTY, "Args (space-separated, or blank): ")
				if err != nil {
					return fmt.Errorf("read args: %w", err)
				}
				argsRaw = strings.TrimSpace(argsRaw)
				if argsRaw != "" {
					spec.Args = strings.Fields(argsRaw)
				}

			case "sse", "http":
				urlStr, err := mcpAddPromptLine(cmd, reader, isTTY, "URL: ")
				if err != nil {
					return fmt.Errorf("read url: %w", err)
				}
				urlStr = strings.TrimSpace(urlStr)
				if urlStr == "" {
					return die(cmd, "url is required for %s transport", transport)
				}
				spec.URL = urlStr
			}

			// --- 4. Optional auth header ---
			// Auth secrets must not go into mcp.toml.  If the user
			// supplies a header name + value here, we:
			//   a) store the value in mcp-auth.json under the server name.
			//   b) write a $VAR placeholder in the mcp.toml headers map.
			//
			// Variable name convention: HYGGE_MCP_<UPPER_NAME>_<UPPER_HEADER>
			// e.g. server "claude", header "Authorization" →
			//        HYGGE_MCP_CLAUDE_AUTHORIZATION
			authHeaders := map[string]string{} // header name → literal secret value

			if transport == "sse" || transport == "http" {
				headerNameRaw, err := mcpAddPromptLine(cmd, reader, isTTY, "Auth header name (e.g. Authorization, or blank to skip): ")
				if err != nil {
					return fmt.Errorf("read auth header name: %w", err)
				}
				headerName := strings.TrimSpace(headerNameRaw)
				if headerName != "" {
					var headerVal string
					if isTTY {
						printf(out(cmd), "Value for %s (hidden input): ", headerName)
						raw, readErr := term.ReadPassword(int(os.Stdin.Fd()))
						if readErr != nil {
							return fmt.Errorf("read auth header value: %w", readErr)
						}
						writeln(out(cmd)) // term.ReadPassword doesn't echo a newline
						headerVal = strings.TrimSpace(string(raw))
					} else {
						headerVal, err = mcpAddPromptLine(cmd, reader, false, "")
						if err != nil {
							return fmt.Errorf("read auth header value: %w", err)
						}
						headerVal = strings.TrimSpace(headerVal)
					}
					if headerVal == "" {
						return die(cmd, "auth header value cannot be empty")
					}
					authOpts := mcp.AuthLoadOptions{HomeDir: mcpHomeDir(), XDGStateHome: mcpXDGStateHome()}
					envVar := mcpAuthEnvVar(name, headerName)
					if owner, err := mcpAuthEnvVarOwner(envVar, authOpts); err != nil {
						return fmt.Errorf("inspect mcp-auth.json: %w", err)
					} else if owner != "" && owner != name {
						return die(cmd, "auth placeholder %s would collide with existing MCP server %q; choose a less ambiguous server name", envVar, owner)
					}
					authHeaders[headerName] = headerVal
					// Write a $VAR placeholder in the mcp.toml headers map.
					if spec.Headers == nil {
						spec.Headers = map[string]string{}
					}
					spec.Headers[headerName] = "$" + envVar
				}
			}

			// --- 5. Determine user mcp.toml path ---
			xdgConfig := mcpXDGConfig()
			mcpTOMLPath := filepath.Join(xdgConfig, "hygge", "mcp.toml")

			// --- 6. Write server config to mcp.toml ---
			if err := mcp.AppendServer(mcp.AppendServerOptions{
				Path:   mcpTOMLPath,
				Server: spec,
			}); err != nil {
				return fmt.Errorf("write mcp.toml: %w", err)
			}
			printf(out(cmd), "Server %q added to %s\n", name, mcpTOMLPath)

			// --- 7. Persist auth if provided ---
			if len(authHeaders) > 0 {
				authOpts := mcp.AuthLoadOptions{
					HomeDir:      mcpHomeDir(),
					XDGStateHome: mcpXDGStateHome(),
				}
				authEntry := mcp.AuthEntry{
					Headers: authHeaders,
				}
				if err := mcp.SetAuth(name, authEntry, authOpts); err != nil {
					return fmt.Errorf("write mcp-auth.json: %w", err)
				}
				authPath, _ := mcp.AuthPath(authOpts)
				printf(out(cmd), "Auth header(s) for %q saved to %s\n", name, authPath)
				printf(out(cmd), "Hygge will source these values from mcp-auth.json at startup.\n")
			}

			return nil
		},
	}
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
