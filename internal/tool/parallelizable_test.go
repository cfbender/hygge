package tool

import (
	"testing"
)

// TestParallelizable_BuiltinMapping asserts that every built-in tool returns
// the expected Parallelizable() value as documented in the package doc.
//
//	read, grep, glob, skill, task → true  (read-only / isolated)
//	bash, write, edit, question   → false (state-mutating or user-blocking)
func TestParallelizable_BuiltinMapping(t *testing.T) {
	rt := newReadTracker()

	cases := []struct {
		name string
		tool Tool
		want bool
	}{
		{"read", newReadTool(rt), true},
		{"grep", newGrepTool(), true},
		{"glob", newGlobTool(), true},
		{"webfetch", newWebfetchTool(nil), true},
		{"bash", newBashTool(), false},
		{"write", newWriteTool(rt), false},
		{"edit", newEditTool(rt), false},
		{"question", NewQuestionTool(), false},
		{"skill", NewSkillTool(nil), true},
		// SubagentTool requires a runner; use NewSubagentTool with nil runner — only
		// Parallelizable() is called, no Execute.
		{"subagent", NewSubagentTool(nil), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tool.Parallelizable()
			if got != tc.want {
				t.Errorf("%s.Parallelizable() = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
