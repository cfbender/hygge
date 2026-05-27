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

	"github.com/cfbender/hygge/internal/mcp"
)

func writeMCPTomlAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPList_NoServers(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "no MCP servers configured") {
		t.Errorf("expected empty-state marker, got:\n%s", buf.String())
	}
}

func TestMCPList_WithDisabledServer(t *testing.T) {
	home := hermeticHome(t)
	writeMCPTomlAt(t, filepath.Join(home, ".agents", "mcp.toml"), `
[[servers]]
name = "github"
command = "echo"
enabled = false
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "github") {
		t.Errorf("missing server name:\n%s", got)
	}
	if !strings.Contains(got, "disabled") {
		t.Errorf("expected disabled status:\n%s", got)
	}
}

func TestMCPList_FailedServerReported(t *testing.T) {
	home := hermeticHome(t)
	// Point at a non-existent command so Initialize fails.
	writeMCPTomlAt(t, filepath.Join(home, ".agents", "mcp.toml"), `
[[servers]]
name = "broken"
command = "/nonexistent/mcp-binary-that-does-not-exist"
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "broken") {
		t.Errorf("missing server name:\n%s", got)
	}
	if !strings.Contains(got, "failed") {
		t.Errorf("expected failed status:\n%s", got)
	}
}

func TestMCPPing_UnknownServer(t *testing.T) {
	hermeticHome(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "ping", "ghost"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unknown server")
	}
	if !strings.Contains(buf.String(), "no MCP server named") {
		t.Errorf("missing error message:\n%s", buf.String())
	}
}

func TestMCPPing_DisabledServer(t *testing.T) {
	home := hermeticHome(t)
	writeMCPTomlAt(t, filepath.Join(home, ".agents", "mcp.toml"), `
[[servers]]
name = "off"
command = "echo"
enabled = false
`)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "ping", "off"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for disabled server")
	}
	if !strings.Contains(buf.String(), "disabled") {
		t.Errorf("missing disabled message:\n%s", buf.String())
	}
}

func TestMCPDoctor_NoFiles(t *testing.T) {
	hermeticHome(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "doctor"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	// Every layer reports "absent".
	if !strings.Contains(got, "absent") {
		t.Errorf("expected absent status:\n%s", got)
	}
	if !strings.Contains(got, "No mcp.toml") {
		t.Errorf("expected no-files note:\n%s", got)
	}
}

func TestMCPDoctor_ReportsInvalidFile(t *testing.T) {
	home := hermeticHome(t)
	writeMCPTomlAt(t, filepath.Join(home, ".agents", "mcp.toml"), `
[[servers]]
name = "bogus"
transport = "lasers"
command = "true"
`)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "doctor"})
	_ = root.Execute()
	got := buf.String()
	if !strings.Contains(got, "invalid") && !strings.Contains(got, "unknown transport") {
		t.Errorf("expected invalid-transport diagnostic:\n%s", got)
	}
}

func TestMCPDoctorDoesNotRefreshOAuth(t *testing.T) {
	home := hermeticHome(t)
	xdgState := filepath.Join(home, ".local", "state")
	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer refreshServer.Close()
	writeMCPTomlAt(t, filepath.Join(home, ".agents", "mcp.toml"), `
[[servers]]
name = "oauth-srv"
transport = "http"
url = "https://mcp.example.com/mcp"
oauth = true
`)
	if err := mcp.SetAuth("oauth-srv", mcp.AuthEntry{Tokens: &mcp.OAuthTokens{
		AccessToken:  "old-access",
		RefreshToken: "refresh-token",
		TokenURL:     refreshServer.URL,
		ExpiresAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Add(-time.Minute).UnixMilli(),
	}}, mcp.AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "doctor"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := refreshCalls.Load(); got != 0 {
		t.Fatalf("refresh endpoint called %d times, want 0", got)
	}
}

func TestMCPTools_NoServers(t *testing.T) {
	hermeticHome(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"mcp", "tools"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Just the header line — no fatal error.
	if !strings.Contains(buf.String(), "SERVER") {
		t.Errorf("expected header in output:\n%s", buf.String())
	}
}
