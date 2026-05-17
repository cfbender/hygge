package agent

import (
	"strings"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/session"
)

// markerPrefix is prepended to a compaction summary when synthesising the
// system prompt for a session that has been compacted.  The phrasing is
// deliberate: it cues the model that what follows is a recap of prior
// turns, not a fresh instruction set.
const markerPrefix = "Earlier in this conversation: "

// composeSystemPrompt folds a compaction summary, lazy-loaded subdir
// context, and hook-provided one-turn context into a system prompt.
// Order is: base prompt, marker summary (if any), lazy addition (if
// any), then hook additions (if any). Hook additions go last because
// they are generated for the exact user message that triggered this turn.
//
// When marker is nil and both addition sets are empty the prompt is
// returned unchanged.
func composeSystemPrompt(
	systemPrompt string,
	marker *session.Marker,
	lazyBlocks []agentsmd.Block,
	systemPromptAdditions []string,
) string {
	hasMarker := marker != nil && strings.TrimSpace(marker.Summary) != ""
	lazyAdd := agentsmd.BuildLazyAddition(lazyBlocks)
	hookAdd := buildHookSystemPromptAddition(systemPromptAdditions)

	if !hasMarker && lazyAdd == "" && hookAdd == "" {
		return systemPrompt
	}

	var b strings.Builder
	if systemPrompt != "" {
		b.WriteString(systemPrompt)
	}
	if hasMarker {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(markerPrefix)
		b.WriteString(marker.Summary)
	}
	if lazyAdd != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(lazyAdd)
	}
	if hookAdd != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(hookAdd)
	}
	return b.String()
}

func buildHookSystemPromptAddition(additions []string) string {
	if len(additions) == 0 {
		return ""
	}
	var filtered []string
	for _, add := range additions {
		if strings.TrimSpace(add) == "" {
			continue
		}
		filtered = append(filtered, add)
	}
	if len(filtered) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Additional hook context (loaded for this turn)\n\n")
	for i, add := range filtered {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(add)
	}
	return b.String()
}
