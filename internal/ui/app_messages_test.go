package ui

import "testing"

func TestExtractTargetDecodesEscapedCommand(t *testing.T) {
	t.Parallel()

	args := []byte(`{"command":"cd \"/tmp/project with spaces\" && go test ./internal/ui -run TestToolCallProgress"}`)
	want := `cd "/tmp/project with spaces" && go test ./internal/ui -run TestToolCallProgress`

	if got := extractTarget(args); got != want {
		t.Fatalf("extractTarget() = %q, want %q", got, want)
	}
}
