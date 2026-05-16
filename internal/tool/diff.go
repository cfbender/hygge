package tool

import (
	"fmt"
	"strings"
)

func toolResultWithDiff(summary, path, beforeLabel, afterLabel, before, after string) string {
	diff := simpleUnifiedDiff(path, beforeLabel, afterLabel, before, after)
	if diff == "" {
		return summary
	}
	return summary + "\n" + diff
}

func simpleUnifiedDiff(path, beforeLabel, afterLabel, before, after string) string {
	if before == after {
		return ""
	}
	if beforeLabel == "" {
		beforeLabel = path + " (before)"
	}
	if afterLabel == "" {
		afterLabel = path + " (after)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n@@\n", beforeLabel, afterLabel)
	for _, line := range splitToolDiffText(before) {
		b.WriteByte('-')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range splitToolDiffText(after) {
		b.WriteByte('+')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func splitToolDiffText(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
