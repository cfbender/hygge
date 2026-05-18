package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/auth"
)

func TestModels_PrintsGroupedCatalog(t *testing.T) {
	home := hermeticHome(t)
	withCatalogFixture(t)
	seedModelsAuth(t, home, "anthropic")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"models", "--provider", "anthropic"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"Models", "anthropic", "Claude Sonnet 4.5", "200K ctx", "reasoning"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestModels_OnlyShowsAuthenticatedProviders(t *testing.T) {
	home := hermeticHome(t)
	withCatalogFixture(t)
	seedModelsAuth(t, home, "anthropic")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"models"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "anthropic") {
		t.Fatalf("expected authenticated anthropic provider in output:\n%s", got)
	}
	if strings.Contains(got, "openai") || strings.Contains(got, "o3-mini") {
		t.Fatalf("unauthenticated openai provider leaked into models output:\n%s", got)
	}
	if !strings.Contains(got, "1 configured providers") {
		t.Fatalf("metadata should count filtered providers, got:\n%s", got)
	}
}

func TestModels_NoAuthenticatedProviders_PrintsHint(t *testing.T) {
	home := hermeticHome(t)
	withCatalogFixture(t)
	clearModelsAuth(t, home, "anthropic")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"models"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"No configured or authenticated providers", "hygge provider auth", "hygge catalog list"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestModels_UnauthenticatedProviderFilter_Errors(t *testing.T) {
	home := hermeticHome(t)
	withCatalogFixture(t)
	seedModelsAuth(t, home, "anthropic")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"models", "--provider", "openai"})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected unauthenticated provider filter to error")
	}
	if !strings.Contains(buf.String(), "not configured or authenticated") {
		t.Fatalf("missing unauthenticated provider message:\n%s", buf.String())
	}
}

func TestModels_LimitShowsMoreHint(t *testing.T) {
	home := hermeticHome(t)
	withCatalogFixture(t)
	seedModelsAuth(t, home, "anthropic")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"models", "--limit", "1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := buf.String()
	if !strings.Contains(got, "more") {
		t.Fatalf("expected limit hint in output:\n%s", got)
	}
}

func TestModels_UnknownProvider_Errors(t *testing.T) {
	home := hermeticHome(t)
	withCatalogFixture(t)
	seedModelsAuth(t, home, "anthropic")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"models", "missing-provider"})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected error for unknown provider")
	}
}

func seedModelsAuth(t *testing.T, home, providerName string) {
	t.Helper()
	if err := auth.Set(providerName,
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-test-models", AddedAt: time.Now()},
		authOptsFor(home)); err != nil {
		t.Fatalf("seed auth: %v", err)
	}
}

func clearModelsAuth(t *testing.T, home, providerName string) {
	t.Helper()
	if err := auth.Remove(providerName, authOptsFor(home)); err != nil {
		t.Fatalf("clear auth: %v", err)
	}
}
