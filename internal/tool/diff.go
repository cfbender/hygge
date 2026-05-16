package tool

import (
	"fmt"
	"strings"
)

func toolResultWithDiff(summary, beforeLabel, afterLabel string, oldStart, newStart int, before, after string) string {
	diff := simpleUnifiedDiff(beforeLabel, afterLabel, oldStart, newStart, before, after)
	if diff == "" {
		return summary
	}
	return summary + "\n" + diff
}

func simpleUnifiedDiff(beforeLabel, afterLabel string, oldStart, newStart int, before, after string) string {
	if before == after {
		return ""
	}
	beforeLines := splitToolDiffText(before)
	afterLines := splitToolDiffText(after)
	if oldStart < 0 {
		oldStart = 0
	}
	if oldStart == 0 && len(beforeLines) > 0 {
		oldStart = 1
	}
	if newStart <= 0 && len(afterLines) > 0 {
		newStart = 1
	}
	if beforeLabel == "" {
		beforeLabel = "before"
	}
	if afterLabel == "" {
		afterLabel = "after"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n@@ -%d,%d +%d,%d @@\n", beforeLabel, afterLabel, oldStart, len(beforeLines), newStart, len(afterLines))
	for _, line := range beforeLines {
		b.WriteByte('-')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range afterLines {
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

func lineNumberForOffset(s string, offset int) int {
	if offset <= 0 {
		return 1
	}
	if offset > len(s) {
		offset = len(s)
	}
	return strings.Count(s[:offset], "\n") + 1
}
