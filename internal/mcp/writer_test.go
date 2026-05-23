package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendServer_Stdio(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")

	if err := AppendServer(AppendServerOptions{
		Path: p,
		Server: AppendServerSpec{
			Name:      "test-stdio",
			Transport: "stdio",
			Command:   "mcp-server",
			Args:      []string{"--port", "8080"},
			Env:       map[string]string{"FOO": "bar"},
		},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}

	// Re-parse and verify.
	cfgs, err := LoadConfigs(LoadOptions{HomeDir: dir, XDGConfigHome: dir})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	// The file is not in the standard discovery path.  Read it directly.
	data, _ := os.ReadFile(p) //nolint:gosec // test-controlled temp path
	if !strings.Contains(string(data), `name = "test-stdio"`) {
		t.Errorf("missing name in output:\n%s", data)
	}
	if !strings.Contains(string(data), `command = "mcp-server"`) {
		t.Errorf("missing command in output:\n%s", data)
	}
	if !strings.Contains(string(data), `"--port"`) {
		t.Errorf("missing args in output:\n%s", data)
	}
	if !strings.Contains(string(data), `"8080"`) {
		t.Errorf("missing args in output:\n%s", data)
	}
	if !strings.Contains(string(data), `FOO`) {
		t.Errorf("missing env key in output:\n%s", data)
	}
	_ = cfgs
}

func TestAppendServer_QuotesEnvKeysAndEscapesValues(t *testing.T) {
	dir := t.TempDir()
	mcpTOML := filepath.Join(dir, "hygge", "mcp.toml")

	if err := AppendServer(AppendServerOptions{
		Path: mcpTOML,
		Server: AppendServerSpec{
			Name:      "quoted-env",
			Transport: "stdio",
			Command:   "env-server",
			Env:       map[string]string{"X.API KEY": "line1\nline2\tend"},
		},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}

	data, err := os.ReadFile(mcpTOML) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read mcp.toml: %v", err)
	}
	if !strings.Contains(string(data), `"X.API KEY"`) {
		t.Fatalf("env key was not quoted:\n%s", data)
	}
	cfgs, err := LoadConfigs(LoadOptions{XDGConfigHome: dir})
	if err != nil {
		t.Fatalf("LoadConfigs should parse quoted env key/value: %v\n%s", err, data)
	}
	if got := cfgs[0].Env["X.API KEY"]; got != "line1\nline2\tend" {
		t.Fatalf("env value = %q, want escaped value round-trip", got)
	}
}

func TestAppendServer_StdioDefaultTransport(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")

	if err := AppendServer(AppendServerOptions{
		Path: p,
		Server: AppendServerSpec{
			Name:      "default-transport",
			Transport: "", // should default to stdio
			Command:   "echo",
		},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}

	data, _ := os.ReadFile(p) //nolint:gosec // test-controlled temp path
	// "stdio" is the default; the writer omits the transport field.
	if strings.Contains(string(data), `transport =`) {
		t.Errorf("transport field should be omitted for stdio default:\n%s", data)
	}
}

func TestAppendServer_URL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")

	if err := AppendServer(AppendServerOptions{
		Path: p,
		Server: AppendServerSpec{
			Name:      "remote-http",
			Transport: "http",
			URL:       "https://example.com/mcp",
			Headers: map[string]string{
				"Authorization": "$MY_TOKEN",
			},
		},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}

	data, _ := os.ReadFile(p) //nolint:gosec // test-controlled temp path
	if !strings.Contains(string(data), `transport = "http"`) {
		t.Errorf("missing transport:\n%s", data)
	}
	if !strings.Contains(string(data), `url = "https://example.com/mcp"`) {
		t.Errorf("missing url:\n%s", data)
	}
	if !strings.Contains(string(data), `$MY_TOKEN`) {
		t.Errorf("missing header placeholder:\n%s", data)
	}
}

func TestAppendServer_SSE(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")

	if err := AppendServer(AppendServerOptions{
		Path: p,
		Server: AppendServerSpec{
			Name:      "sse-srv",
			Transport: "sse",
			URL:       "https://sse.example.com/events",
		},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}

	data, _ := os.ReadFile(p) //nolint:gosec // test-controlled temp path
	if !strings.Contains(string(data), `transport = "sse"`) {
		t.Errorf("missing sse transport:\n%s", data)
	}
}

func TestAppendServer_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")

	// Write a first server.
	if err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "first", Transport: "stdio", Command: "cmd1"},
	}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	// Write a second server.
	if err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "second", Transport: "stdio", Command: "cmd2"},
	}); err != nil {
		t.Fatalf("second append: %v", err)
	}

	data, _ := os.ReadFile(p) //nolint:gosec // test-controlled temp path
	if strings.Count(string(data), "[[servers]]") != 2 {
		t.Errorf("expected 2 [[servers]] blocks:\n%s", data)
	}
	if !strings.Contains(string(data), "first") || !strings.Contains(string(data), "second") {
		t.Errorf("missing server names:\n%s", data)
	}
}

func TestAppendServer_DuplicateNameError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")

	if err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "dup", Transport: "stdio", Command: "cmd"},
	}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "dup", Transport: "stdio", Command: "cmd2"},
	}); err == nil {
		t.Error("expected error for duplicate server name")
	}
}

func TestAppendServer_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "subdir", "mcp.toml")
	if err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "srv", Transport: "stdio", Command: "echo"},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestAppendServer_ValidationMissingName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")
	err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "", Transport: "stdio", Command: "echo"},
	})
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestAppendServer_ValidationMissingCommand(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")
	err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "srv", Transport: "stdio", Command: ""},
	})
	if err == nil {
		t.Error("expected error for missing command with stdio transport")
	}
}

func TestAppendServer_ValidationMissingURL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")
	err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "srv", Transport: "http", URL: ""},
	})
	if err == nil {
		t.Error("expected error for missing url with http transport")
	}
}

func TestAppendServer_WrittenFileIsReadableByLoadConfigs(t *testing.T) {
	// Write a server and verify that LoadConfigs can parse the output file.
	dir := t.TempDir()
	// The UserHygge layer lives at $XDGConfig/hygge/mcp.toml.
	// Set up XDGConfigHome = dir so the file lands at dir/hygge/mcp.toml.
	mcpTOML := filepath.Join(dir, "hygge", "mcp.toml")

	if err := AppendServer(AppendServerOptions{
		Path:   mcpTOML,
		Server: AppendServerSpec{Name: "parseable", Transport: "stdio", Command: "true", Args: []string{"--flag"}},
	}); err != nil {
		t.Fatalf("AppendServer: %v", err)
	}

	cfgs, err := LoadConfigs(LoadOptions{XDGConfigHome: dir})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("want 1 server, got %d", len(cfgs))
	}
	c := cfgs[0]
	if c.Name != "parseable" {
		t.Errorf("name: got %q", c.Name)
	}
	if c.Command != "true" {
		t.Errorf("command: got %q", c.Command)
	}
	if len(c.Args) != 1 || c.Args[0] != "--flag" {
		t.Errorf("args: got %v", c.Args)
	}
}

// TestAppendServer_CorruptTOMLReturnsError verifies that a corrupt
// (unparseable) existing mcp.toml causes AppendServer to return an
// error rather than silently skipping the duplicate check.
func TestAppendServer_CorruptTOMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mcp.toml")
	if err := os.WriteFile(p, []byte("this is not valid toml ][[["), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	err := AppendServer(AppendServerOptions{
		Path:   p,
		Server: AppendServerSpec{Name: "srv", Transport: "stdio", Command: "echo"},
	})
	if err == nil {
		t.Fatal("expected error for corrupt mcp.toml, got nil")
	}
	if !strings.Contains(err.Error(), p) {
		t.Errorf("error should mention path %q: %v", p, err)
	}
}

func TestSortedStringKeys_Stable(t *testing.T) {
	m := map[string]string{"z": "1", "a": "2", "m": "3"}
	got := sortedStringKeys(m)
	want := []string{"a", "m", "z"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
