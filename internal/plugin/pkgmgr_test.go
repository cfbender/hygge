package plugin_test

import (
	"context"
	"testing"

	"github.com/cfbender/hygge/internal/plugin"
)

// TestParseSource_valid tests all valid source URI forms.
func TestParseSource_valid(t *testing.T) {
	cases := []struct {
		uri      string
		kind     plugin.SourceKind
		user     string
		repo     string
		ref      string
		isBranch bool
	}{
		{
			uri:  "github:cfbender/hygge-policy-guard",
			kind: plugin.SourceGitHub,
			user: "cfbender",
			repo: "hygge-policy-guard",
		},
		{
			uri:  "github:cfbender/hygge-policy-guard@v1.2.3",
			kind: plugin.SourceGitHub,
			user: "cfbender",
			repo: "hygge-policy-guard",
			ref:  "v1.2.3",
		},
		{
			uri:  "github:cfbender/hygge-policy-guard@abcd1234",
			kind: plugin.SourceGitHub,
			user: "cfbender",
			repo: "hygge-policy-guard",
			ref:  "abcd1234",
		},
		{
			uri:      "github:cfbender/hygge-policy-guard#main",
			kind:     plugin.SourceGitHub,
			user:     "cfbender",
			repo:     "hygge-policy-guard",
			ref:      "main",
			isBranch: true,
		},
		{
			uri:  "local:/Users/cfb/code/my-plugin",
			kind: plugin.SourceLocal,
		},
	}

	for _, tc := range cases {
		src, err := plugin.ParseSource(tc.uri)
		if err != nil {
			t.Errorf("ParseSource(%q): unexpected error: %v", tc.uri, err)
			continue
		}
		if src.Kind != tc.kind {
			t.Errorf("ParseSource(%q).Kind = %q, want %q", tc.uri, src.Kind, tc.kind)
		}
		if tc.kind == plugin.SourceGitHub {
			if src.User != tc.user {
				t.Errorf("ParseSource(%q).User = %q, want %q", tc.uri, src.User, tc.user)
			}
			if src.Repo != tc.repo {
				t.Errorf("ParseSource(%q).Repo = %q, want %q", tc.uri, src.Repo, tc.repo)
			}
			if src.Ref != tc.ref {
				t.Errorf("ParseSource(%q).Ref = %q, want %q", tc.uri, src.Ref, tc.ref)
			}
			if src.Branch != tc.isBranch {
				t.Errorf("ParseSource(%q).Branch = %v, want %v", tc.uri, src.Branch, tc.isBranch)
			}
		}
		if src.Raw != tc.uri {
			t.Errorf("ParseSource(%q).Raw = %q, want %q", tc.uri, src.Raw, tc.uri)
		}
	}
}

// TestParseSource_invalid tests that invalid URIs return errors.
func TestParseSource_invalid(t *testing.T) {
	cases := []string{
		"",
		"no-scheme",
		"npm:some-package",
		"github:",
		"github:onlyone",
		"github:/norepo",
		"github:user/",
		"local:relative/path",
	}

	for _, uri := range cases {
		_, err := plugin.ParseSource(uri)
		if err == nil {
			t.Errorf("ParseSource(%q): expected error, got nil", uri)
		}
	}
}

// TestSource_CacheDirName checks that cache dir names are deterministic and
// filesystem-safe.
func TestSource_CacheDirName(t *testing.T) {
	cases := []struct {
		uri      string
		expected string // only prefix for github cases
	}{
		{"github:cfbender/hygge-policy-guard", "github-cfbender-hygge-policy-guard-default"},
		{"github:cfbender/hygge-policy-guard@v1.2.3", "github-cfbender-hygge-policy-guard-v1-2-3"},
		{"github:cfbender/hygge-policy-guard#main", "github-cfbender-hygge-policy-guard-main"},
	}

	for _, tc := range cases {
		src, err := plugin.ParseSource(tc.uri)
		if err != nil {
			t.Fatalf("ParseSource(%q): %v", tc.uri, err)
		}
		got := src.CacheDirName()
		if got != tc.expected {
			t.Errorf("CacheDirName(%q) = %q, want %q", tc.uri, got, tc.expected)
		}
	}
}

// TestSource_CloneURL checks the HTTPS clone URL generation.
func TestSource_CloneURL(t *testing.T) {
	src, _ := plugin.ParseSource("github:cfbender/hygge-policy-guard")
	want := "https://github.com/cfbender/hygge-policy-guard.git"
	if got := src.CloneURL(); got != want {
		t.Errorf("CloneURL() = %q, want %q", got, want)
	}

	local, _ := plugin.ParseSource("local:/tmp/my-plugin")
	if got := local.CloneURL(); got != "" {
		t.Errorf("CloneURL() for local = %q, want empty", got)
	}
}

func TestPackageManager_ResolveGitHubUsesInjectedGitRunner(t *testing.T) {
	runner := &fakeGitRunner{}
	pm := plugin.NewPackageManagerWithGitRunner(t.TempDir(), runner)
	src, err := plugin.ParseSource("github:cfbender/hygge-policy-guard#main")
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	if _, err := pm.Resolve(context.Background(), src); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %#v, want clone + checkout", runner.calls)
	}
	clone := runner.calls[0]
	if len(clone.args) != 4 || clone.args[0] != "clone" || clone.args[1] != "--depth=1" || clone.args[2] != src.CloneURL() {
		t.Fatalf("clone args = %#v", clone.args)
	}
	checkout := runner.calls[1]
	if len(checkout.args) != 4 || checkout.args[0] != "-C" || checkout.args[2] != "checkout" || checkout.args[3] != "main" {
		t.Fatalf("checkout args = %#v", checkout.args)
	}
}

type fakeGitRunner struct {
	calls []fakeGitCall
}

type fakeGitCall struct {
	dir  string
	args []string
}

func (f *fakeGitRunner) Run(_ context.Context, dir string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, fakeGitCall{dir: dir, args: append([]string(nil), args...)})
	return nil, nil
}
