package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/mcp"
)

// mcpAddInput concatenates lines with "\n" to simulate piped stdin.
func mcpAddInput(lines ...string) *bytes.Buffer {
	return bytes.NewBufferString(strings.Join(lines, "\n") + "\n")
}

// ---------------------------------------------------------------------------
// Appears in help
// ---------------------------------------------------------------------------

func TestMCPAddAppearsInHelp(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "--help"})
	_ = root.Execute()
	if !strings.Contains(buf.String(), "add") {
		t.Errorf("'add' not found in mcp help output:\n%s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Stdio server — name as positional arg
// ---------------------------------------------------------------------------

func TestMCPAdd_StdioWithPositionalName(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"stdio",   // transport
		"mcp-bin", // command
		"",        // args (blank)
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "my-server", "--scope", "global"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "my-server") {
		t.Errorf("server name not in output:\n%s", buf.String())
	}

	// Verify the file was written.
	mcpTOML := filepath.Join(home, ".config", "hygge", "mcp.toml")
	data, err := os.ReadFile(mcpTOML) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("mcp.toml not written: %v", err)
	}
	if !strings.Contains(string(data), `name = "my-server"`) {
		t.Errorf("missing server name in mcp.toml:\n%s", data)
	}
	if !strings.Contains(string(data), `command = "mcp-bin"`) {
		t.Errorf("missing command in mcp.toml:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// Stdio server — name prompted (no positional arg)
// ---------------------------------------------------------------------------

func TestMCPAdd_ProjectScopeWritesProjectConfig(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"stdio", // transport
		"echo",  // command
		"",      // args blank
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "project-server", "--scope", "project"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	projectMCP := filepath.Join(home, ".hygge", "mcp.toml")
	data, err := os.ReadFile(projectMCP) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("project mcp.toml not written: %v", err)
	}
	if !strings.Contains(string(data), `name = "project-server"`) {
		t.Errorf("missing server name in project mcp.toml:\n%s", data)
	}
	globalMCP := filepath.Join(home, ".config", "hygge", "mcp.toml")
	if _, err := os.Stat(globalMCP); err == nil {
		t.Fatalf("global mcp.toml should not be written for project scope")
	}
	if !strings.Contains(buf.String(), "project config") {
		t.Errorf("success output should mention project scope:\n%s", buf.String())
	}
}

func TestMCPAdd_StdioWithPromptedName(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"prompted-server", // name prompt
		"stdio",           // transport
		"echo",            // command
		"",                // args blank
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "--scope", "global"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	mcpTOML := filepath.Join(home, ".config", "hygge", "mcp.toml")
	data, err := os.ReadFile(mcpTOML) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("mcp.toml not written: %v", err)
	}
	if !strings.Contains(string(data), "prompted-server") {
		t.Errorf("missing server name:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// HTTP server with auth header
// ---------------------------------------------------------------------------

func TestMCPAdd_HTTPWithAuth(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"http",                     // transport
		"https://api.example.com/", // url
		"header",                   // auth method
		"Authorization",            // auth header name
		"Bearer super-secret-tok",  // auth header value (non-TTY path)
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "remote-srv", "--scope", "global"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	mcpTOML := filepath.Join(home, ".config", "hygge", "mcp.toml")
	data, err := os.ReadFile(mcpTOML) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("mcp.toml not written: %v", err)
	}
	// The mcp.toml should contain a $VAR reference, not the literal secret.
	if strings.Contains(string(data), "super-secret-tok") {
		t.Errorf("mcp.toml contains literal secret; should use $VAR reference:\n%s", data)
	}
	if !strings.Contains(string(data), "$HYGGE_MCP_REMOTE_SRV_AUTHORIZATION") {
		t.Errorf("mcp.toml missing env-var placeholder:\n%s", data)
	}

	// The mcp-auth.json should contain the literal secret.
	xdgState := filepath.Join(home, ".local", "state")
	authOpts := mcp.AuthLoadOptions{
		HomeDir:      home,
		XDGStateHome: xdgState,
	}
	store, err := mcp.LoadAuth(authOpts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	entry, ok := store.GetAuth("remote-srv")
	if !ok {
		t.Fatal("remote-srv not found in mcp-auth.json")
	}
	if entry.Headers["Authorization"] != "Bearer super-secret-tok" {
		t.Errorf("Authorization header: got %q", entry.Headers["Authorization"])
	}
	if strings.Contains(buf.String(), "export ") || strings.Contains(buf.String(), "<your-value>") {
		t.Errorf("output should not tell user to re-export stored secret:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "source these values from mcp-auth.json") {
		t.Errorf("output should explain auth store usage:\n%s", buf.String())
	}
}

func TestMCPAdd_HTTPWithOAuthFlag(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"http",
		"https://api.example.com/mcp",
		"none",
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "remote-oauth", "--scope", "global", "--oauth"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	mcpTOML := filepath.Join(home, ".config", "hygge", "mcp.toml")
	data, err := os.ReadFile(mcpTOML) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("mcp.toml not written: %v", err)
	}
	if !strings.Contains(string(data), "oauth = true") {
		t.Fatalf("mcp.toml missing oauth=true:\n%s", data)
	}
}

func TestMCPAuthEnvLookupSourcesStoredHeaders(t *testing.T) {
	home := hermeticHome(t)
	xdgConfig := filepath.Join(home, ".config")
	xdgState := filepath.Join(home, ".local", "state")

	writeStoredAuthServer(t, home, xdgConfig, xdgState)

	cfgs, err := mcp.LoadConfigs(mcp.LoadOptions{
		HomeDir:       home,
		XDGConfigHome: xdgConfig,
		EnvLookup:     mcpAuthEnvLookup(bootstrapOptions{HomeDir: home, XDGStateHome: xdgState}),
	})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("got %d configs, want 1", len(cfgs))
	}
	if got := cfgs[0].Headers["Authorization"]; got != "Bearer stored-token" {
		t.Fatalf("Authorization header = %q, want stored token", got)
	}
}

func TestApplyMCPOAuthInjectsBearerHeader(t *testing.T) {
	home := hermeticHome(t)
	xdgConfig := filepath.Join(home, ".config")
	xdgState := filepath.Join(home, ".local", "state")

	mcpTOML := filepath.Join(xdgConfig, "hygge", "mcp.toml")
	if err := mcp.AppendServer(mcp.AppendServerOptions{
		Path: mcpTOML,
		Server: mcp.AppendServerSpec{
			Name:      "oauth-srv",
			Transport: "http",
			URL:       "https://mcp.example.com/mcp",
			OAuth:     true,
		},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}
	if err := mcp.SetAuth("oauth-srv", mcp.AuthEntry{OAuth: &mcp.OAuthCredential{AccessToken: "oauth-token"}}, mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	cfgs, err := mcp.LoadConfigs(mcp.LoadOptions{HomeDir: home, XDGConfigHome: xdgConfig})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	fixedNow := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfgs = applyMCPOAuth(cfgs, mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}, fixedNow)
	if !cfgs[0].OAuth.Enabled {
		t.Fatal("OAuth not enabled from config")
	}
	if got := cfgs[0].Headers["Authorization"]; got != "Bearer oauth-token" {
		t.Fatalf("Authorization = %q, want OAuth bearer token", got)
	}
}

func TestApplyMCPOAuthSkipsDisabledServers(t *testing.T) {
	home := hermeticHome(t)
	xdgState := filepath.Join(home, ".local", "state")
	authOpts := mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfgs := []mcp.ServerConfig{{
		Name:      "disabled-oauth",
		Transport: "http",
		URL:       "https://mcp.example.com/mcp",
		OAuth:     mcp.OAuthConfig{Enabled: true},
		Enabled:   false,
	}}
	if err := mcp.SetAuth("disabled-oauth", mcp.AuthEntry{Tokens: &mcp.OAuthTokens{
		AccessToken:  "old-access",
		RefreshToken: "refresh-token",
		TokenURL:     "https://127.0.0.1:1/token",
		ExpiresAt:    now.Add(-time.Minute).UnixMilli(),
	}}, authOpts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	got := applyMCPOAuth(cfgs, authOpts, now)
	if _, ok := got[0].Headers["Authorization"]; ok {
		t.Fatalf("disabled server received Authorization header: %#v", got[0].Headers)
	}
}

func TestApplyMCPOAuthLoadOnlyDoesNotRefreshExpiredToken(t *testing.T) {
	home := hermeticHome(t)
	xdgState := filepath.Join(home, ".local", "state")
	authOpts := mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer refreshServer.Close()
	cfgs := []mcp.ServerConfig{{
		Name:      "oauth-srv",
		Transport: "http",
		URL:       "https://mcp.example.com/mcp",
		OAuth:     mcp.OAuthConfig{Enabled: true},
		Enabled:   true,
	}}
	if err := mcp.SetAuth("oauth-srv", mcp.AuthEntry{Tokens: &mcp.OAuthTokens{
		AccessToken:  "old-access",
		RefreshToken: "refresh-token",
		TokenURL:     refreshServer.URL,
		ExpiresAt:    now.Add(-time.Minute).UnixMilli(),
	}}, authOpts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	got := applyMCPOAuthLoadOnly(cfgs, authOpts)
	if got[0].Headers["Authorization"] != "Bearer old-access" {
		t.Fatalf("Authorization = %q, want stored access token", got[0].Headers["Authorization"])
	}
	if got := refreshCalls.Load(); got != 0 {
		t.Fatalf("refresh endpoint called %d times, want 0", got)
	}
}

func TestPrepareAsyncMCPUsesStoredAuthHeaders(t *testing.T) {
	home := hermeticHome(t)
	xdgConfig := filepath.Join(home, ".config")
	xdgState := filepath.Join(home, ".local", "state")
	writeStoredAuthServer(t, home, xdgConfig, xdgState)

	cfgs, statuses := prepareAsyncMCP(bootstrapOptions{HomeDir: home, XDGStateHome: xdgState}, xdgConfig)
	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}
	if len(cfgs) != 1 {
		t.Fatalf("got %d configs, want 1", len(cfgs))
	}
	if got := cfgs[0].Headers["Authorization"]; got != "Bearer stored-token" {
		t.Fatalf("async Authorization header = %q, want stored token", got)
	}
}

func writeStoredAuthServer(t *testing.T, home, xdgConfig, xdgState string) {
	t.Helper()
	mcpTOML := filepath.Join(xdgConfig, "hygge", "mcp.toml")
	if err := mcp.AppendServer(mcp.AppendServerOptions{
		Path: mcpTOML,
		Server: mcp.AppendServerSpec{
			Name:      "remote-srv",
			Transport: "http",
			URL:       "https://api.example.com/mcp",
			Headers:   map[string]string{"Authorization": "$HYGGE_MCP_REMOTE_SRV_AUTHORIZATION"},
		},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}
	if err := mcp.SetAuth("remote-srv", mcp.AuthEntry{Headers: map[string]string{"Authorization": "Bearer stored-token"}}, mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SSE server without auth header
// ---------------------------------------------------------------------------

func TestMCPAdd_SSENoAuth(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"sse",                     // transport
		"https://sse.example.com", // url
		"none",                    // auth method
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "sse-server", "--scope", "global"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	// mcp-auth.json should not be created (no secrets).
	xdgState := filepath.Join(home, ".local", "state")
	authPath := filepath.Join(xdgState, "hygge", "mcp-auth.json")
	if _, err := os.Stat(authPath); err == nil {
		t.Error("mcp-auth.json should not be created when no auth is provided")
	}
}

// ---------------------------------------------------------------------------
// Default transport (blank → stdio)
// ---------------------------------------------------------------------------

func TestMCPAdd_DefaultTransportIsStdio(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"", // transport blank → default stdio
		"my-binary",
		"",
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "default-transport", "--scope", "global"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	mcpTOML := filepath.Join(home, ".config", "hygge", "mcp.toml")
	data, _ := os.ReadFile(mcpTOML) //nolint:gosec // test-controlled temp path
	// transport = "stdio" is the default and should be omitted.
	if strings.Contains(string(data), `transport =`) {
		t.Errorf("transport field present for stdio default:\n%s", data)
	}
	if !strings.Contains(string(data), "default-transport") {
		t.Errorf("server name missing:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// Error: missing scope in noninteractive mode
// ---------------------------------------------------------------------------

func TestMCPAdd_ErrorMissingScopeNonInteractive(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"stdio",
		"echo",
		"",
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "needs-scope"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected missing scope error")
	}
	if !strings.Contains(buf.String(), "--scope is required") {
		t.Fatalf("missing scope guidance:\n%s", buf.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "hygge", "mcp.toml")); err == nil {
		t.Fatal("global mcp.toml should not be written when scope is missing")
	}
	if _, err := os.Stat(filepath.Join(home, ".hygge", "mcp.toml")); err == nil {
		t.Fatal("project mcp.toml should not be written when scope is missing")
	}
}

func TestMCPAdd_ErrorInvalidScope(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	root.SetIn(mcpAddInput("stdio", "echo", ""))
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "bad-scope", "--scope", "team"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected invalid scope error")
	}
	if !strings.Contains(buf.String(), "invalid --scope") {
		t.Fatalf("missing invalid scope message:\n%s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Error: missing name
// ---------------------------------------------------------------------------

func TestMCPAdd_ErrorMissingName(t *testing.T) {
	hermeticHome(t)

	// Empty name input.
	input := mcpAddInput("")

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "--scope", "global"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for missing server name")
	}
	if !strings.Contains(buf.String(), "server name is required") {
		t.Errorf("missing error message:\n%s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Error: unknown transport
// ---------------------------------------------------------------------------

func TestMCPAdd_ErrorUnknownTransport(t *testing.T) {
	hermeticHome(t)

	input := mcpAddInput("lasers") // bad transport

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "srv", "--scope", "global"})
	if err := root.Execute(); err == nil {
		t.Error("expected error for unknown transport")
	}
	if !strings.Contains(buf.String(), "unknown transport") {
		t.Errorf("missing error message:\n%s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Stdio with args
// ---------------------------------------------------------------------------

func TestMCPAdd_StdioWithArgs(t *testing.T) {
	home := hermeticHome(t)

	input := mcpAddInput(
		"stdio",
		"mcp-tool",
		"--verbose --port 3000", // args
	)

	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "with-args", "--scope", "global"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	mcpTOML := filepath.Join(home, ".config", "hygge", "mcp.toml")
	data, _ := os.ReadFile(mcpTOML) //nolint:gosec // test-controlled temp path
	if !strings.Contains(string(data), "--verbose") {
		t.Errorf("args missing from mcp.toml:\n%s", data)
	}
	if !strings.Contains(string(data), "3000") {
		t.Errorf("port arg missing from mcp.toml:\n%s", data)
	}
}

// ---------------------------------------------------------------------------
// Duplicate server name rejected
// ---------------------------------------------------------------------------

func TestMCPAdd_DuplicateServerRejected(t *testing.T) {
	home := hermeticHome(t)

	addServer := func() error {
		input := mcpAddInput("stdio", "echo", "")
		root := NewRootCmd()
		root.SetIn(input)
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"mcp", "add", "dup-srv", "--scope", "global"})
		return root.Execute()
	}

	if err := addServer(); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := addServer(); err == nil {
		t.Error("expected error adding duplicate server")
	}
	_ = home
}

// ---------------------------------------------------------------------------
// mcpAuthEnvVar helper
// ---------------------------------------------------------------------------

func TestMCPAdd_RejectsAuthPlaceholderCollision(t *testing.T) {
	home := hermeticHome(t)
	xdgState := filepath.Join(home, ".local", "state")
	if err := mcp.SetAuth("foo-bar", mcp.AuthEntry{Headers: map[string]string{"Authorization": "Bearer first"}}, mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	input := mcpAddInput(
		"http",
		"https://api.example.com/mcp",
		"header",
		"Authorization",
		"Bearer second",
	)
	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "foo_bar", "--scope", "global"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(buf.String(), "would collide with existing MCP server") {
		t.Fatalf("collision message missing:\n%s", buf.String())
	}
}

func TestMCPAuthEnvVar(t *testing.T) {
	cases := []struct {
		server, header, want string
	}{
		{"my-server", "Authorization", "HYGGE_MCP_MY_SERVER_AUTHORIZATION"},
		{"foo", "X-API-Key", "HYGGE_MCP_FOO_X_API_KEY"},
		{"a b", "Header Name", "HYGGE_MCP_A_B_HEADER_NAME"},
	}
	for _, c := range cases {
		got := mcpAuthEnvVar(c.server, c.header)
		if got != c.want {
			t.Errorf("mcpAuthEnvVar(%q, %q) = %q, want %q", c.server, c.header, got, c.want)
		}
	}
}

func TestMCPAddWizard_StdioProjectFlow(t *testing.T) {
	hermeticHome(t)

	m := newMCPAddWizardModel("", "")
	updated, _ := m.advance()
	m = updated.(mcpAddWizardModel)
	if m.step != mcpAddWizardStepName || m.scope != mcpAddScopeProject {
		t.Fatalf("after scope: step=%v scope=%q", m.step, m.scope)
	}

	m.input.SetValue("wizard-srv")
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	if m.transport != "stdio" || m.step != mcpAddWizardStepCommand {
		t.Fatalf("after transport: step=%v transport=%q", m.step, m.transport)
	}

	m.input.SetValue("echo")
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	m.input.SetValue("hello world")
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	if !m.done {
		t.Fatal("wizard should be done after stdio args")
	}
	req, err := m.request()
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if req.scope != mcpAddScopeProject {
		t.Fatalf("scope = %q, want project", req.scope)
	}
	if req.spec.Name != "wizard-srv" || req.spec.Command != "echo" {
		t.Fatalf("bad spec: %+v", req.spec)
	}
	if got := strings.Join(req.spec.Args, " "); got != "hello world" {
		t.Fatalf("args = %q", got)
	}
}

func TestMCPAddWizard_HTTPAuthFlow(t *testing.T) {
	hermeticHome(t)

	m := newMCPAddWizardModel("remote", mcpAddScopeGlobal)
	if m.step != mcpAddWizardStepTransport {
		t.Fatalf("initial step = %v, want transport", m.step)
	}
	m.moveChoice(1) // http
	updated, _ := m.advance()
	m = updated.(mcpAddWizardModel)
	m.input.SetValue("https://api.example.com/mcp")
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	m.moveChoice(1) // header
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	m.input.SetValue("Authorization")
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	if m.step != mcpAddWizardStepAuthValue || m.input.EchoMode != textinput.EchoPassword {
		t.Fatalf("auth value step/echo not set: step=%v echo=%v", m.step, m.input.EchoMode)
	}
	m.input.SetValue("Bearer token")
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)

	req, err := m.request()
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if req.spec.Headers["Authorization"] != "$HYGGE_MCP_REMOTE_AUTHORIZATION" {
		t.Fatalf("headers = %#v", req.spec.Headers)
	}
	if req.authHeaders["Authorization"] != "Bearer token" {
		t.Fatalf("auth headers = %#v", req.authHeaders)
	}
}

func TestMCPAddWizard_ChoiceStepsUseSelection(t *testing.T) {
	hermeticHome(t)

	m := newMCPAddWizardModel("", "")
	if !m.isChoiceStep() || m.selectedChoiceValue() != "project" {
		t.Fatalf("initial choice = %q, choice step=%v", m.selectedChoiceValue(), m.isChoiceStep())
	}
	if view := m.View().Content; !strings.Contains(view, "›") || !strings.Contains(view, "Project") || strings.Contains(view, "Type project") {
		t.Fatalf("scope choice view not rendered as selectable options:\n%s", view)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(mcpAddWizardModel)
	if got := m.selectedChoiceValue(); got != "global" {
		t.Fatalf("after down selected %q, want global", got)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(mcpAddWizardModel)
	m.input.SetValue("choice-srv")
	updated, _ = m.advance()
	m = updated.(mcpAddWizardModel)
	if !m.isChoiceStep() || m.step != mcpAddWizardStepTransport {
		t.Fatalf("transport step should be selectable, step=%v choice=%v", m.step, m.isChoiceStep())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(mcpAddWizardModel)
	if got := m.selectedChoiceValue(); got != "http" {
		t.Fatalf("after down selected transport %q, want http", got)
	}
}

// ---------------------------------------------------------------------------
// Partial-state guard: auth write failure must not create mcp.toml
// ---------------------------------------------------------------------------

// TestMCPAdd_AuthFailureDoesNotWriteMCPTOML verifies that when auth
// persistence fails (e.g. unwritable state dir), the mcp.toml file is
// NOT created — preserving atomicity between the two writes.
func TestMCPAdd_AuthFailureDoesNotWriteMCPTOML(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("chmod 000 has no effect when running as root")
	}
	home := hermeticHome(t)

	// Make the XDG state directory unwritable so SetAuth fails.
	stateDir := filepath.Join(home, ".local", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.Chmod(stateDir, 0o444); err != nil { //nolint:gosec // test intentionally makes directory unwritable
		t.Fatalf("chmod state: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) }) //nolint:gosec // restore temp directory permissions for cleanup

	input := mcpAddInput(
		"http",
		"https://api.example.com/mcp",
		"Authorization",
		"Bearer secret",
	)
	root := NewRootCmd()
	root.SetIn(input)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "add", "partial-srv", "--scope", "global"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when auth write fails")
	}

	// mcp.toml must NOT have been written.
	mcpTOML := filepath.Join(home, ".config", "hygge", "mcp.toml")
	if _, statErr := os.Stat(mcpTOML); statErr == nil {
		t.Error("mcp.toml was written despite auth failure; partial state created")
	}
}
