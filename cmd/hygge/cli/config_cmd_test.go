package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestConfigExplainNoKey(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "explain"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "model.provider") {
		t.Errorf("output should not include synthesized model.provider provenance:\n%s", got)
	}
	if !strings.Contains(got, "permission.shell") {
		t.Errorf("output missing permission.shell line:\n%s", got)
	}
}

func TestConfigExplainKey(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "explain", "permission.shell"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "permission.shell") {
		t.Errorf("output missing key:\n%s", got)
	}
	if !strings.Contains(got, "set by:") {
		t.Errorf("output missing provenance chain:\n%s", got)
	}
	if !strings.Contains(got, "<defaults>") {
		t.Errorf("output should reference the defaults source:\n%s", got)
	}
}
