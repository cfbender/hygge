package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/bus"
)

func setupGrepTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(p, c string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	mustWrite("a.go", "package a\nfunc Alpha() {}\nfunc Beta() {}\n")
	mustWrite("b.go", "package b\nfunc Alpha() {}\n")
	mustWrite("readme.md", "Alpha is great\n")
	mustWrite("node_modules/skip.go", "func Alpha() {}\n")
	mustWrite(".git/HEAD", "ref: refs/heads/main\n")
	return dir
}

func TestGrep_HappyPathWalk(t *testing.T) {
	dir := setupGrepTree(t)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGrepTool()
	gt.lookPath = func(string) (string, error) { return "", errors.New("not found") }

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"Alpha"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	// Three matches: a.go, b.go, readme.md.  node_modules excluded, .git
	// excluded.
	for _, want := range []string{"a.go:2:", "b.go:2:", "readme.md:1:"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %q in:\n%s", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "node_modules") {
		t.Errorf("node_modules should be excluded:\n%s", res.Content)
	}
	if strings.Contains(res.Content, ".git") {
		t.Errorf(".git should be excluded:\n%s", res.Content)
	}
	if res.Metadata["used_rg"].(bool) {
		t.Errorf("used_rg: expected false")
	}
}

func TestGrep_RgAndFallbackAgree(t *testing.T) {
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("rg not installed; skipping cross-backend equivalence")
	}

	dir := setupGrepTree(t)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	args := json.RawMessage(`{"pattern":"Alpha","include":"*.go"}`)

	gtRg := newGrepTool()
	gtRg.lookPath = func(s string) (string, error) {
		if s == "rg" {
			return rgPath, nil
		}
		return "", errors.New("not found")
	}
	resRg, err := gtRg.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("rg Execute: %v", err)
	}

	gtFallback := newGrepTool()
	gtFallback.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	resFb, err := gtFallback.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("fallback Execute: %v", err)
	}

	if !resRg.Metadata["used_rg"].(bool) {
		t.Errorf("rg path should have used_rg=true")
	}
	if resFb.Metadata["used_rg"].(bool) {
		t.Errorf("fallback should have used_rg=false")
	}

	// Both should agree on the set of "path:line:" prefixes.
	prefRg := matchPrefixes(resRg.Content)
	prefFb := matchPrefixes(resFb.Content)
	if !stringSetsEqual(prefRg, prefFb) {
		t.Errorf("prefixes differ: rg=%v fb=%v\n--rg--\n%s\n--fb--\n%s",
			prefRg, prefFb, resRg.Content, resFb.Content)
	}
}

func matchPrefixes(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		// path:line:text — keep "path:line"
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		// In rg output paths might be slightly different (relative vs
		// absolute); normalise to basename:line.
		base := filepath.Base(parts[0])
		out[base+":"+parts[1]] = struct{}{}
	}
	return out
}

func stringSetsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func TestGrep_NoMatches(t *testing.T) {
	dir := setupGrepTree(t)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGrepTool()
	gt.lookPath = func(string) (string, error) { return "", errors.New("nope") }

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"NoSuchSymbol"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError on no matches: %+v", res)
	}
	if !strings.Contains(res.Content, "no matches") {
		t.Errorf("Content: %q", res.Content)
	}
	if res.Metadata["matches"].(int) != 0 {
		t.Errorf("matches: %v", res.Metadata["matches"])
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	dir := setupGrepTree(t)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGrepTool()

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"(unbalanced"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for invalid regex")
	}
}

func TestGrep_InvalidArgs(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGrepTool()

	cases := []string{`{}`, `{"pattern":""}`, `{"pattern":"x","extra":1}`, `not json`}
	for _, args := range cases {
		_, err := gt.Execute(context.Background(), json.RawMessage(args), ec)
		if err == nil {
			t.Errorf("expected error for %s", args)
			continue
		}
		var te *ToolError
		if !errors.As(err, &te) || te.Code != CodeInvalidArgs {
			t.Errorf("want invalid_args for %s, got %v", args, err)
		}
	}
}

func TestGrep_PermissionDenied(t *testing.T) {
	dir := setupGrepTree(t)
	outside := t.TempDir()
	e, b := builtinTestEngine(t, denyAll)
	ec := newExecContext(b, e, dir)
	gt := newGrepTool()

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"Alpha","path":"`+outside+`"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for deny")
	}
}

func TestGrep_PermissionTarget(t *testing.T) {
	dir := setupGrepTree(t)
	outside := t.TempDir()
	rec := newRecordingResponder(bus.PermissionReplied{Decision: "allow", Scope: "once"})
	e, b := builtinTestEngine(t, rec.decide)
	ec := newExecContext(b, e, dir)
	gt := newGrepTool()

	if _, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"x","path":"`+outside+`"}`), ec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reqs := rec.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("requests: %d", len(reqs))
	}
	if reqs[0].Category != "file.read" {
		t.Errorf("Category: %q", reqs[0].Category)
	}
	if reqs[0].Target != filepath.Clean(outside) {
		t.Errorf("Target: got %q want %q", reqs[0].Target, outside)
	}
	if reqs[0].ToolName != "grep" {
		t.Errorf("ToolName: %q", reqs[0].ToolName)
	}
}
