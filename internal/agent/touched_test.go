package agent

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestTouchedPaths(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input string
		want  []string
	}{
		{"read with path", "read", `{"path":"/abs/foo.go"}`, []string{"/abs/foo.go"}},
		{"write with path", "write", `{"path":"foo.go","content":"x"}`, []string{"foo.go"}},
		{"edit with path", "edit", `{"path":"foo.go","oldString":"a","newString":"b"}`, []string{"foo.go"}},
		{"grep with path", "grep", `{"pattern":"x","path":"pkg"}`, []string{"pkg"}},
		{"grep without path defaults to .", "grep", `{"pattern":"x"}`, []string{"."}},
		{"glob with path", "glob", `{"pattern":"*.go","path":"cmd"}`, []string{"cmd"}},
		{"glob without path defaults to .", "glob", `{"pattern":"*.go"}`, []string{"."}},
		{"bash returns nil", "bash", `{"command":"ls"}`, nil},
		{"skill returns nil", "skill", `{"name":"tdd"}`, nil},
		{"mcp prefix returns nil", "mcp.search", `{"query":"x"}`, nil},
		{"unknown tool returns nil", "frobnicate", `{"path":"x"}`, nil},
		{"empty path string treated as missing", "read", `{"path":""}`, nil},
		{"empty input on read returns nil", "read", ``, nil},
		{"invalid json returns nil", "read", `not json`, nil},
		{"empty input on grep defaults to .", "grep", ``, []string{"."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := touchedPaths(tc.tool, json.RawMessage(tc.input))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("touchedPaths(%q, %q) = %v, want %v",
					tc.tool, tc.input, got, tc.want)
			}
		})
	}
}
