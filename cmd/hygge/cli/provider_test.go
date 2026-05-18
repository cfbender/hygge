package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/provider"
)

// authOptsFor returns LoadOptions for the hermetic home dir.
func authOptsFor(home string) auth.LoadOptions {
	return auth.LoadOptions{
		HomeDir:      home,
		XDGStateHome: filepath.Join(home, ".local", "state"),
	}
}

// TestProviderAuth_NonTTYStdin: piping a key on stdin saves the
// credential.  Verifies the file exists with mode 0600 and the key
// round-trips.
func TestProviderAuth_NonTTYStdin(t *testing.T) {
	home := hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("sk-pasted-key-1234\n"))
	root.SetArgs([]string{"provider", "auth", "anthropic"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Saved credential for anthropic") {
		t.Errorf("missing save confirmation:\n%s", got)
	}
	// Masked echo must not contain the full key.
	if strings.Contains(got, "sk-pasted-key-1234") {
		t.Errorf("unmasked key leaked to output:\n%s", got)
	}

	// File exists with mode 0600.
	p, err := auth.Path(authOptsFor(home))
	if err != nil {
		t.Fatalf("auth.Path: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode: got %04o, want 0600", mode)
	}

	// Credential round-trips.
	store, err := auth.Load(authOptsFor(home))
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	cred, ok := store.Get("anthropic")
	if !ok {
		t.Fatal("credential not stored")
	}
	if cred.APIKey != "sk-pasted-key-1234" {
		t.Errorf("APIKey: got %q, want %q", cred.APIKey, "sk-pasted-key-1234")
	}
	if cred.Type != auth.CredAPIKey {
		t.Errorf("Type: got %q, want %q", cred.Type, auth.CredAPIKey)
	}
}

// TestProviderAuth_NoArgPicksFromList: with no positional arg, the
// picker reads a provider selection from stdin. Followed by a piped
// API key.
func TestProviderAuth_NoArgPicksFromList(t *testing.T) {
	home := hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("anthropic\nsk-second-key-9999\n"))
	root.SetArgs([]string{"provider", "auth"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	store, err := auth.Load(authOptsFor(home))
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	cred, ok := store.Get("anthropic")
	if !ok {
		t.Fatalf("expected anthropic credential; store had: %v", store.List())
	}
	if cred.APIKey != "sk-second-key-9999" {
		t.Errorf("APIKey: got %q, want %q", cred.APIKey, "sk-second-key-9999")
	}
}

// TestProviderAuth_EmptyKeyRejected: piping an empty line rejects the
// save with a clear message.
func TestProviderAuth_EmptyKeyRejected(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("\n"))
	root.SetArgs([]string{"provider", "auth", "anthropic"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for empty key")
	}
	if !strings.Contains(out.String(), "empty API key") {
		t.Errorf("missing rejection message:\n%s", out.String())
	}
}

// TestProviderList_Empty: no credentials → friendly empty marker.
func TestProviderList_Empty(t *testing.T) {
	home := hermeticHome(t)
	// hermeticHome seeds a fake credential so runTUI tests pass.
	// `provider list` is the one place that asserts the store is empty.
	xdgState := filepath.Join(home, ".local", "state")
	if err := auth.Remove("anthropic", auth.LoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("auth.Remove: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"provider", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "(no providers configured") {
		t.Errorf("missing empty marker:\n%s", out.String())
	}
}

// TestProviderList_OneCredentialMasked: list shows the masked key,
// never the raw value.
func TestProviderList_OneCredentialMasked(t *testing.T) {
	home := hermeticHome(t)
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-abcdefghijklmnop"},
		authOptsFor(home)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"provider", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "anthropic") {
		t.Errorf("missing provider name:\n%s", got)
	}
	if !strings.Contains(got, "api_key") {
		t.Errorf("missing type column:\n%s", got)
	}
	masked := maskKey("sk-abcdefghijklmnop")
	if !strings.Contains(got, masked) {
		t.Errorf("missing masked key %q in:\n%s", masked, got)
	}
	if strings.Contains(got, "sk-abcdefghijklmnop") {
		t.Errorf("raw key leaked to output:\n%s", got)
	}
}

// TestProviderRemove_NoConfirm: -f skips the prompt and deletes the
// credential.
func TestProviderRemove_NoConfirm(t *testing.T) {
	home := hermeticHome(t)
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-x"},
		authOptsFor(home)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"provider", "remove", "anthropic", "--no-confirm"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Removed credential for anthropic") {
		t.Errorf("missing confirmation:\n%s", out.String())
	}

	store, err := auth.Load(authOptsFor(home))
	if err != nil {
		t.Fatalf("auth.Load: %v", err)
	}
	if _, ok := store.Get("anthropic"); ok {
		t.Error("credential still present after remove")
	}
}

// TestProviderRemove_Missing: removing a missing provider exits 0 with
// the friendly marker.
func TestProviderRemove_Missing(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"provider", "remove", "nonexistent", "--no-confirm"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "(no credential for nonexistent") {
		t.Errorf("missing 'no credential' marker:\n%s", out.String())
	}
}

// TestProviderRemove_ConfirmYes: piped "y\n" confirms the deletion.
func TestProviderRemove_ConfirmYes(t *testing.T) {
	home := hermeticHome(t)
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-x"},
		authOptsFor(home)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("y\n"))
	root.SetArgs([]string{"provider", "remove", "anthropic"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Removed credential") {
		t.Errorf("missing removal confirmation:\n%s", out.String())
	}
}

// TestProviderRemove_ConfirmNo: piped "n\n" cancels the deletion.
func TestProviderRemove_ConfirmNo(t *testing.T) {
	home := hermeticHome(t)
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-x"},
		authOptsFor(home)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader("n\n"))
	root.SetArgs([]string{"provider", "remove", "anthropic"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Cancelled") {
		t.Errorf("missing cancel message:\n%s", out.String())
	}
	// Still present.
	store, _ := auth.Load(authOptsFor(home))
	if _, ok := store.Get("anthropic"); !ok {
		t.Error("credential removed despite 'n' answer")
	}
}

// TestMaskKey covers the masking helper directly.
func TestMaskKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "***"},
		{"short", "***"},
		{"12345678", "***"},
		{"sk-abcdef", "sk-***cdef"},
		{"sk-abcdefghijklmnop", "sk-***mnop"},
	}
	for _, c := range cases {
		if got := maskKey(c.in); got != c.want {
			t.Errorf("maskKey(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

// TestProviderEnvVar covers the env-var map directly.
func TestProviderEnvVar(t *testing.T) {
	cases := map[string]string{
		"anthropic":  "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
		"mistral":    "MISTRAL_API_KEY",
		"groq":       "GROQ_API_KEY",
		"deepseek":   "DEEPSEEK_API_KEY",
		"google":     "GOOGLE_API_KEY",
		"gemini":     "GOOGLE_API_KEY",
		"xai":        "XAI_API_KEY",
		"unknown":    "",
		"":           "",
	}
	for in, want := range cases {
		if got := providerEnvVar(in); got != want {
			t.Errorf("providerEnvVar(%q): got %q, want %q", in, got, want)
		}
	}
}

// TestKnownProviders sanity-checks the picker enumeration.
func TestKnownProviders(t *testing.T) {
	names := knownProviders()
	if len(names) <= 8 {
		t.Errorf("knownProviders returned %d entries; want Catwalk provider list, not the legacy 8-provider cap", len(names))
	}
	for _, name := range []string{"anthropic", "openai", "openrouter"} {
		if !providerKnownContains(name) {
			t.Errorf("%s missing from knownProviders", name)
		}
	}
}

// TestOpenAIRegistered confirms the openai shim is wired into the CLI via
// the blank import in common.go.  Without this guard, removing the import
// would silently break `hygge config set model.provider = openai`.
func TestOpenAIRegistered(t *testing.T) {
	f, err := provider.Get("openai")
	if err != nil {
		t.Fatalf("provider.Get(openai): %v", err)
	}
	if f == nil {
		t.Fatal("factory is nil")
	}
	if providerEnvVar("openai") != "OPENAI_API_KEY" {
		t.Errorf("openai env var mapping missing")
	}
}

// TestOpenRouterRegistered confirms the openrouter shim is wired into the
// CLI via the blank import in common.go.  Without this guard, removing
// the import would silently break `hygge config set model.provider =
// openrouter`.
func TestOpenRouterRegistered(t *testing.T) {
	f, err := provider.Get("openrouter")
	if err != nil {
		t.Fatalf("provider.Get(openrouter): %v", err)
	}
	if f == nil {
		t.Fatal("factory is nil")
	}
	if providerEnvVar("openrouter") != "OPENROUTER_API_KEY" {
		t.Errorf("openrouter env var mapping missing")
	}
}
