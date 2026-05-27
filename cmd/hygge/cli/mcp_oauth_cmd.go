package cli

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/mcp"
)

// newMCPOAuthCmd builds `hygge mcp auth <name>`, a browser-based OAuth flow
// for configured HTTP/SSE MCP servers.
func newMCPOAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth <name>",
		Short: "Authenticate a configured MCP server with OAuth",
		Long: `Authenticate a configured HTTP/SSE MCP server using OAuth.

Hygge discovers the server's OAuth metadata, dynamically registers a local
client when needed, opens your browser, receives the loopback callback, and
stores the resulting tokens in mcp-auth.json. The matching server entry must
include oauth = true.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return die(cmd, "server name is required")
			}
			return runMCPAuth(cmd, name)
		},
	}
	return cmd
}

func runMCPAuth(cmd *cobra.Command, name string) error {
	cfgs, err := loadMCPConfigs()
	if err != nil {
		return err
	}
	var target *mcp.ServerConfig
	for i := range cfgs {
		if cfgs[i].Name == name {
			target = &cfgs[i]
			break
		}
	}
	if target == nil {
		return die(cmd, "no MCP server named %q (try `hygge mcp list`)", name)
	}
	if target.Transport != "http" && target.Transport != "sse" {
		return die(cmd, "MCP server %q uses %s transport; OAuth auth is only supported for http or sse", name, target.Transport)
	}
	if !target.OAuth.Enabled {
		return die(cmd, "MCP server %q does not have oauth = true in %s", name, target.Path)
	}

	baseCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(baseCtx, 5*time.Minute)
	defer cancel()
	httpClient := &http.Client{Timeout: 30 * time.Second}
	redirectURI := mcp.OAuthRedirectURI(target.OAuth.RedirectURI)

	printf(out(cmd), "Discovering OAuth metadata for %s...\n", name)
	endpoints, err := mcp.DiscoverOAuthEndpoints(ctx, httpClient, target.URL)
	if err != nil {
		return err
	}

	authOpts := mcp.AuthLoadOptions{HomeDir: mcpHomeDir(), XDGStateHome: mcpXDGStateHome()}
	store, err := mcp.LoadAuth(authOpts)
	if err != nil {
		return fmt.Errorf("load mcp-auth.json: %w", err)
	}
	entry, _ := store.GetAuth(name)

	clientID := target.OAuth.ClientID
	clientSecret := target.OAuth.ClientSecret
	if clientID == "" && entry.ClientInfo != nil && entry.ServerURL == target.URL && !clientSecretExpired(entry.ClientInfo) {
		clientID = entry.ClientInfo.ClientID
		clientSecret = entry.ClientInfo.ClientSecret
	}
	entry.ServerURL = target.URL
	if clientID == "" {
		printf(out(cmd), "Registering OAuth client...\n")
		registered, err := mcp.RegisterOAuthClient(ctx, httpClient, endpoints.RegistrationEndpoint, redirectURI, false)
		if err != nil {
			return err
		}
		clientID = registered.ClientID
		clientSecret = registered.ClientSecret
		entry.ClientInfo = &mcp.OAuthClientInfo{
			ClientID:              registered.ClientID,
			ClientSecret:          registered.ClientSecret,
			ClientIDIssuedAt:      secondsToMillis(registered.ClientIDIssuedAt),
			ClientSecretExpiresAt: secondsToMillis(registered.ClientSecretExpiresAt),
		}
		if err := mcp.SetAuth(name, entry, authOpts); err != nil {
			return fmt.Errorf("save registered OAuth client: %w", err)
		}
	}

	state, err := mcp.GenerateOAuthState()
	if err != nil {
		return fmt.Errorf("generate OAuth state: %w", err)
	}
	verifier, challenge, err := mcp.GeneratePKCEVerifier()
	if err != nil {
		return fmt.Errorf("generate PKCE verifier: %w", err)
	}
	entry.OAuthState = state
	entry.CodeVerifier = verifier
	if err := mcp.SetAuth(name, entry, authOpts); err != nil {
		return fmt.Errorf("save OAuth state: %w", err)
	}
	stateSaved := true
	defer func() {
		if stateSaved {
			clearMCPOAuthTransientState(name, authOpts)
		}
	}()

	callback, err := startMCPOAuthCallback(redirectURI, state)
	if err != nil {
		return err
	}
	defer callback.Close()

	authURL, err := mcp.BuildAuthorizationURL(*endpoints, clientID, redirectURI, target.OAuth.Scope, state, challenge)
	if err != nil {
		return err
	}
	printf(out(cmd), "Opening browser for %s OAuth...\n", name)
	if err := openBrowser(authURL); err != nil {
		printf(out(cmd), "Open this URL to continue:\n%s\n", authURL)
	}

	result, err := callback.Wait(ctx)
	if err != nil {
		if baseCtx.Err() != nil {
			return fmt.Errorf("OAuth authentication cancelled")
		}
		return err
	}
	if result.State != state {
		return fmt.Errorf("OAuth state mismatch - potential CSRF attack")
	}
	tokens, err := mcp.ExchangeOAuthCode(ctx, httpClient, *endpoints, clientID, clientSecret, redirectURI, verifier, result.Code)
	if err != nil {
		return err
	}
	entry.OAuthState = ""
	entry.CodeVerifier = ""
	entry.Tokens = &mcp.OAuthTokens{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenURL:     endpoints.TokenEndpoint,
		Scope:        tokens.Scope,
	}
	if tokens.ExpiresIn > 0 {
		entry.Tokens.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).UnixMilli()
	}
	if err := mcp.SetAuth(name, entry, authOpts); err != nil {
		return fmt.Errorf("save OAuth tokens: %w", err)
	}
	stateSaved = false
	authPath, _ := mcp.AuthPath(authOpts)
	printf(out(cmd), "✓ Authenticated MCP server %q. Tokens saved to %s\n", name, authPath)
	return nil
}

func clearMCPOAuthTransientState(name string, opts mcp.AuthLoadOptions) {
	store, err := mcp.LoadAuth(opts)
	if err != nil {
		return
	}
	entry, ok := store.GetAuth(name)
	if !ok {
		return
	}
	entry.OAuthState = ""
	entry.CodeVerifier = ""
	_ = mcp.SetAuth(name, entry, opts)
}

func clientSecretExpired(info *mcp.OAuthClientInfo) bool {
	return info != nil && info.ClientSecretExpiresAt > 0 && info.ClientSecretExpiresAt < time.Now().UnixMilli()
}

func secondsToMillis(v int64) int64 {
	if v <= 0 {
		return 0
	}
	return v * 1000
}

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL) // #nosec G204 -- fixed browser opener with OAuth URL argument.
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL) // #nosec G204 -- fixed browser opener with OAuth URL argument.
	default:
		cmd = exec.Command("xdg-open", rawURL) // #nosec G204 -- fixed browser opener with OAuth URL argument.
	}
	return cmd.Start()
}

type mcpOAuthCallbackResult struct {
	Code  string
	State string
}

type mcpOAuthCallbackServer struct {
	server *http.Server
	result chan mcpOAuthCallbackResult
	errs   chan error
}

func startMCPOAuthCallback(redirectURI, wantState string) (*mcpOAuthCallbackServer, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return nil, fmt.Errorf("parse OAuth redirect URI: %w", err)
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" {
		return nil, fmt.Errorf("OAuth redirect URI must use loopback host, got %q", u.Host)
	}
	path := u.Path
	if path == "" {
		path = "/mcp/oauth/callback"
	}
	resultCh := make(chan mcpOAuthCallbackResult, 1)
	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		state := q.Get("state")
		if state == "" {
			select {
			case errCh <- fmt.Errorf("missing OAuth state"):
			default:
			}
			http.Error(w, "Missing OAuth state", http.StatusBadRequest)
			return
		}
		if state != wantState {
			select {
			case errCh <- fmt.Errorf("invalid OAuth state"):
			default:
			}
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = e
			}
			errCh <- fmt.Errorf("OAuth authorization failed: %s", desc)
			_, _ = w.Write([]byte(oauthCallbackErrorHTML(desc))) // #nosec G705 -- error text is HTML-escaped in oauthCallbackErrorHTML.
			return
		}
		code := q.Get("code")
		if code == "" {
			select {
			case errCh <- fmt.Errorf("missing OAuth authorization code"):
			default:
			}
			http.Error(w, "Missing OAuth authorization code", http.StatusBadRequest)
			return
		}
		resultCh <- mcpOAuthCallbackResult{Code: code, State: state}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(oauthCallbackSuccessHTML()))
	})
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		return nil, fmt.Errorf("start OAuth callback listener: %w", err)
	}
	server := &http.Server{Addr: ln.Addr().String(), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	cb := &mcpOAuthCallbackServer{server: server, result: resultCh, errs: errCh}
	go func() {
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	return cb, nil
}

func (s *mcpOAuthCallbackServer) Wait(ctx context.Context) (mcpOAuthCallbackResult, error) {
	select {
	case result := <-s.result:
		return result, nil
	case err := <-s.errs:
		return mcpOAuthCallbackResult{}, err
	case <-ctx.Done():
		return mcpOAuthCallbackResult{}, ctx.Err()
	}
}

func (s *mcpOAuthCallbackServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
}

func oauthCallbackSuccessHTML() string {
	return `<!doctype html><html><head><title>Hygge - Authorization Successful</title></head><body><h1>Authorization Successful</h1><p>You can close this window and return to Hygge.</p><script>setTimeout(() => window.close(), 2000);</script></body></html>`
}

func oauthCallbackErrorHTML(err string) string {
	return `<!doctype html><html><head><title>Hygge - Authorization Failed</title></head><body><h1>Authorization Failed</h1><p>` + html.EscapeString(err) + `</p></body></html>`
}
