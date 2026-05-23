package cli

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

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

func TestAuthMethodOptions_AreProviderAware(t *testing.T) {
	openCodeMethods := authMethodOptions("opencode-go")
	if len(openCodeMethods) != 1 || openCodeMethods[0].method != authMethodAPIKey {
		t.Fatalf("opencode-go methods = %#v, want only API key", openCodeMethods)
	}
	for _, method := range openCodeMethods {
		if strings.Contains(strings.ToLower(method.title), "oauth") || strings.Contains(strings.ToLower(method.description), "chatgpt") {
			t.Fatalf("opencode-go should not mention OAuth/ChatGPT: %#v", method)
		}
	}

	openAIMethods := authMethodOptions("openai")
	if len(openAIMethods) != 2 {
		t.Fatalf("openai methods count = %d, want 2", len(openAIMethods))
	}
	if openAIMethods[1].method != authMethodOAuth {
		t.Fatalf("openai second method = %q, want oauth", openAIMethods[1].method)
	}
}

func TestPickAuthMethodFromLine_RejectsUnavailableOAuth(t *testing.T) {
	_, err := pickAuthMethodFromLine(bufio.NewReader(strings.NewReader("2\n")), authMethodOptions("opencode-go"))
	if err == nil {
		t.Fatalf("expected unavailable OAuth selection to error")
	}

	got, err := pickAuthMethodFromLine(bufio.NewReader(strings.NewReader("2\n")), authMethodOptions("openai"))
	if err != nil {
		t.Fatalf("openai oauth selection: %v", err)
	}
	if got != authMethodOAuth {
		t.Fatalf("method got %q, want oauth", got)
	}
}

func TestPickProviderFromLine_NumberAndName(t *testing.T) {
	names := []string{"anthropic", "openai"}

	got, err := pickProviderFromLine(bufio.NewReader(strings.NewReader("2\n")), names)
	if err != nil {
		t.Fatalf("pick number: %v", err)
	}
	if got != "openai" {
		t.Fatalf("pick number got %q, want openai", got)
	}

	got, err = pickProviderFromLine(bufio.NewReader(strings.NewReader("custom\n")), names)
	if err != nil {
		t.Fatalf("pick name: %v", err)
	}
	if got != "custom" {
		t.Fatalf("pick name got %q, want custom", got)
	}
}

func TestProviderSelectModel_EnterChoosesCurrentItem(t *testing.T) {
	m := newProviderSelectModelForTest([]string{"anthropic", "openai"})
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := updated.(providerSelectModel)
	if got.choice != "anthropic" {
		t.Fatalf("choice got %q, want anthropic", got.choice)
	}
	if got.cancelled {
		t.Fatalf("enter should not mark selection cancelled")
	}
}

func TestProviderSelectModel_CancelKeys(t *testing.T) {
	for _, msg := range []tea.KeyPressMsg{
		tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}),
		tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c", Mod: uv.ModCtrl}),
	} {
		m := newProviderSelectModelForTest([]string{"anthropic"})
		updated, _ := m.Update(msg)
		got := updated.(providerSelectModel)
		if !got.cancelled {
			t.Fatalf("%s should cancel", msg.Keystroke())
		}
	}
}

func newProviderSelectModelForTest(names []string) providerSelectModel {
	items := make([]list.Item, 0, len(names))
	for _, name := range names {
		items = append(items, providerSelectItem{name: name})
	}
	delegate := list.NewDefaultDelegate()
	return providerSelectModel{list: list.New(items, delegate, 80, 10)}
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
		"gemini":     "GOOGLE_API_KEY",
		"google":     "",
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

// TestBuildNamedStubProvider_ErrAuthWhenNoCredential confirms that
// buildNamedStubProvider returns provider.ErrAuth for providers with a
// known canonical env var when neither opts["api_key"] nor the env var
// is set.  This preserves the bootstrap auth-fallback path
// (errors.Is(err, provider.ErrAuth) → stubProvider) for providers
// whose env var the CLI knows about.
func TestBuildNamedStubProvider_ErrAuthWhenNoCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	for _, name := range []string{"anthropic", "openai", "openrouter"} {
		_, err := buildNamedStubProvider(name, map[string]any{})
		if !errors.Is(err, provider.ErrAuth) {
			t.Errorf("%s with no credential: want ErrAuth, got %v", name, err)
		}
	}
}

// TestBuildNamedStubProvider_SucceedWithCredential confirms that
// buildNamedStubProvider succeeds when opts["api_key"] or the env var
// is populated (as bootstrap does after resolveProviderOptionsFor
// injects the auth-store credential).
func TestBuildNamedStubProvider_SucceedWithCredential(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	for _, tc := range []struct {
		name   string
		envVar string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
	} {
		// Via opts["api_key"].
		p, err := buildNamedStubProvider(tc.name, map[string]any{"api_key": "sk-test-key"})
		if err != nil {
			t.Errorf("%s with opts api_key: unexpected error: %v", tc.name, err)
		}
		if p == nil {
			t.Errorf("%s with opts api_key: got nil provider", tc.name)
		} else if p.Name() != tc.name {
			t.Errorf("%s: Name() = %q, want %q", tc.name, p.Name(), tc.name)
		}

		// Via env var.
		t.Setenv(tc.envVar, "sk-env-key")
		p2, err := buildNamedStubProvider(tc.name, map[string]any{})
		if err != nil {
			t.Errorf("%s with env var: unexpected error: %v", tc.name, err)
		}
		if p2 == nil {
			t.Errorf("%s with env var: got nil provider", tc.name)
		}
		t.Setenv(tc.envVar, "")
	}
}

// TestBuildNamedStubProvider_UnknownReturnsStub confirms that
// buildNamedStubProvider accepts any provider name, returning a stub
// without error for names that have no known canonical env var.
// Fantasy/Catwalk performs auth and capability validation at runtime.
func TestBuildNamedStubProvider_UnknownReturnsStub(t *testing.T) {
	p, err := buildNamedStubProvider("some_future_provider_xyz", map[string]any{})
	if err != nil {
		t.Fatalf("unknown provider with no env var: want nil error, got %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider stub")
	}
	if p.Name() != "some_future_provider_xyz" {
		t.Errorf("Name() = %q, want %q", p.Name(), "some_future_provider_xyz")
	}
}

// TestRequireAnyKey exercises the credential-check helper used by
// buildNamedStubProvider.
func TestRequireAnyKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	// No opts, no env var → ErrAuth.
	if err := requireAnyKey(nil, "ANTHROPIC_API_KEY"); !errors.Is(err, provider.ErrAuth) {
		t.Errorf("no credential: want ErrAuth, got %v", err)
	}

	// Empty string in opts["api_key"] → ErrAuth.
	if err := requireAnyKey(map[string]any{"api_key": ""}, "ANTHROPIC_API_KEY"); !errors.Is(err, provider.ErrAuth) {
		t.Errorf("empty opts api_key: want ErrAuth, got %v", err)
	}

	// Non-string value in opts["api_key"] → ErrAuth.
	if err := requireAnyKey(map[string]any{"api_key": 42}, "ANTHROPIC_API_KEY"); !errors.Is(err, provider.ErrAuth) {
		t.Errorf("non-string opts api_key: want ErrAuth, got %v", err)
	}

	// Valid string in opts["api_key"] → nil.
	if err := requireAnyKey(map[string]any{"api_key": "sk-test"}, "ANTHROPIC_API_KEY"); err != nil {
		t.Errorf("valid opts api_key: want nil, got %v", err)
	}

	// Env var set → nil even with no opts.
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	if err := requireAnyKey(nil, "OPENAI_API_KEY"); err != nil {
		t.Errorf("env var set: want nil, got %v", err)
	}
}
