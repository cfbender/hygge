package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigs_NoFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	configs, err := LoadConfigs(LoadOptions{
		HomeDir: home,
		Pwd:     pwd,
	})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}
}

func TestLoadConfigs_BasicFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()

	// .git marks project root so walk-up stops here.
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	copyTestdata(t, "testdata/mcp_basic.toml", filepath.Join(pwd, ".agents", "mcp.toml"))

	configs, err := LoadConfigs(LoadOptions{
		HomeDir:   home,
		Pwd:       pwd,
		EnvLookup: func(k string) string { return map[string]string{"GITHUB_TOKEN": "secret123"}[k] },
	})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 server, got %d", len(configs))
	}
	c := configs[0]
	if c.Name != "github" {
		t.Fatalf("Name: got %q", c.Name)
	}
	if c.Command != "mcp-server-github" {
		t.Fatalf("Command: got %q", c.Command)
	}
	if len(c.Args) != 2 || c.Args[0] != "--token" || c.Args[1] != "secret123" {
		t.Fatalf("Args interpolation failed: %v", c.Args)
	}
	if c.Env["GITHUB_API_URL"] != "https://api.github.com" {
		t.Fatalf("Env: %v", c.Env)
	}
	if !c.Enabled {
		t.Fatalf("Enabled should default true")
	}
	if c.PermissionCategory != "mcp" {
		t.Fatalf("PermissionCategory default got %q", c.PermissionCategory)
	}
	if c.Source != SourceProjectAgents {
		t.Fatalf("Source: got %v", c.Source)
	}
}

func TestLoadConfigs_TwoServers(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	copyTestdata(t, "testdata/mcp_two_servers.toml", filepath.Join(pwd, ".agents", "mcp.toml"))

	configs, err := LoadConfigs(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(configs))
	}
	got := map[string]ServerConfig{}
	for _, c := range configs {
		got[c.Name] = c
	}
	if g, ok := got["github"]; !ok || !g.Enabled {
		t.Fatalf("github missing or disabled: %+v", g)
	}
	pg, ok := got["postgres"]
	if !ok {
		t.Fatal("postgres missing")
	}
	if pg.Enabled {
		t.Fatalf("postgres should be disabled")
	}
	if pg.PermissionCategory != "network" {
		t.Fatalf("postgres permission_category: got %q", pg.PermissionCategory)
	}
}

func TestLoadConfigs_WalkUpFromSubdir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	root := t.TempDir()
	sub := filepath.Join(root, "src", "deeply", "nested")
	mustMkdirAll(t, sub)
	mustMkdirAll(t, filepath.Join(root, ".git"))
	mustMkdirAll(t, filepath.Join(root, ".hygge"))
	copyTestdata(t, "testdata/mcp_basic.toml", filepath.Join(root, ".hygge", "mcp.toml"))

	configs, err := LoadConfigs(LoadOptions{
		HomeDir:   home,
		Pwd:       sub,
		EnvLookup: func(_ string) string { return "tok" },
	})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 server, got %d", len(configs))
	}
	if configs[0].Source != SourceProjectHygge {
		t.Fatalf("Source: got %v want project/hygge", configs[0].Source)
	}
}

func TestLoadConfigs_ProjectHyggeOverridesAgents(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	mustMkdirAll(t, filepath.Join(pwd, ".hygge"))

	// Both layers define `github`, but with different commands.
	writeFile(t, filepath.Join(pwd, ".agents", "mcp.toml"), `
[[servers]]
name = "github"
command = "from-agents"
`)
	writeFile(t, filepath.Join(pwd, ".hygge", "mcp.toml"), `
[[servers]]
name = "github"
command = "from-hygge"
`)

	configs, err := LoadConfigs(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 server (deduped), got %d", len(configs))
	}
	if configs[0].Command != "from-hygge" {
		t.Fatalf("expected hygge layer to win, got %q", configs[0].Command)
	}
	if configs[0].Source != SourceProjectHygge {
		t.Fatalf("Source: got %v", configs[0].Source)
	}
}

func TestLoadConfigs_UserLayerOverriddenByProject(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mustMkdirAll(t, filepath.Join(home, ".agents"))
	writeFile(t, filepath.Join(home, ".agents", "mcp.toml"), `
[[servers]]
name = "github"
command = "user-level"
`)
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	writeFile(t, filepath.Join(pwd, ".agents", "mcp.toml"), `
[[servers]]
name = "github"
command = "project-level"
`)

	configs, err := LoadConfigs(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 server, got %d", len(configs))
	}
	if configs[0].Command != "project-level" {
		t.Fatalf("project should win; got %q", configs[0].Command)
	}
}

func TestLoadConfigs_BadTransport(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	copyTestdata(t, "testdata/mcp_bad_transport.toml", filepath.Join(pwd, ".agents", "mcp.toml"))

	_, err := LoadConfigs(LoadOptions{HomeDir: home, Pwd: pwd})
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
	if !strings.Contains(err.Error(), "unknown transport") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigs_InvalidPermissionCategoryFallsBack(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	writeFile(t, filepath.Join(pwd, ".agents", "mcp.toml"), `
[[servers]]
name = "weird"
command = "true"
permission_category = "rocket-launch"
`)

	configs, err := LoadConfigs(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 server, got %d", len(configs))
	}
	if configs[0].PermissionCategory != "mcp" {
		t.Fatalf("expected fallback to \"mcp\", got %q", configs[0].PermissionCategory)
	}
}

func TestLoadConfigs_NameRequired(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	writeFile(t, filepath.Join(pwd, ".agents", "mcp.toml"), `
[[servers]]
command = "true"
`)
	_, err := LoadConfigs(LoadOptions{HomeDir: home, Pwd: pwd})
	if err == nil {
		t.Fatal("expected error when name missing")
	}
}

func TestLoadConfigs_CommandRequired(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	writeFile(t, filepath.Join(pwd, ".agents", "mcp.toml"), `
[[servers]]
name = "noop"
`)
	_, err := LoadConfigs(LoadOptions{HomeDir: home, Pwd: pwd})
	if err == nil {
		t.Fatal("expected error when command missing")
	}
}

func TestLoadConfigs_DollarVarInterpolation(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	mustMkdirAll(t, filepath.Join(pwd, ".git"))
	mustMkdirAll(t, filepath.Join(pwd, ".agents"))
	writeFile(t, filepath.Join(pwd, ".agents", "mcp.toml"), `
[[servers]]
name = "x"
command = "$TOOL_BIN"
args = ["--token", "$TOKEN", "--alt", "${TOKEN}"]
env = { API = "$API_URL" }
dir = "$WORKDIR"
`)
	env := map[string]string{
		"TOOL_BIN": "/usr/local/bin/mcp",
		"TOKEN":    "abc",
		"API_URL":  "https://api.example.com",
		"WORKDIR":  "/tmp/work",
	}
	configs, err := LoadConfigs(LoadOptions{
		HomeDir:   home,
		Pwd:       pwd,
		EnvLookup: func(k string) string { return env[k] },
	})
	if err != nil {
		t.Fatalf("LoadConfigs: %v", err)
	}
	c := configs[0]
	if c.Command != "/usr/local/bin/mcp" {
		t.Fatalf("Command: %q", c.Command)
	}
	if c.Args[1] != "abc" || c.Args[3] != "abc" {
		t.Fatalf("Args: %v", c.Args)
	}
	if c.Env["API"] != "https://api.example.com" {
		t.Fatalf("Env: %v", c.Env)
	}
	if c.Dir != "/tmp/work" {
		t.Fatalf("Dir: %q", c.Dir)
	}
}

func TestSource_String(t *testing.T) {
	t.Parallel()
	cases := map[Source]string{
		SourceUserAgents:    "user/.agents",
		SourceUserHygge:     "user/hygge",
		SourceProjectAgents: "project/.agents",
		SourceProjectHygge:  "project/hygge",
		Source(99):          "unknown(99)",
	}
	for src, want := range cases {
		if got := src.String(); got != want {
			t.Fatalf("%v.String() = %q, want %q", src, got, want)
		}
	}
}

// --- helpers ---------------------------------------------------------

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { //nolint:gosec // test fixture; path comes from t.TempDir
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func copyTestdata(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src) //nolint:gosec // test fixture; src is a static testdata path
	if err != nil {
		t.Fatalf("ReadFile %s: %v", src, err)
	}
	writeFile(t, dst, string(data))
}
