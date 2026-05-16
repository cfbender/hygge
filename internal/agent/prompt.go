package agent

import (
	"strings"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// markerPrefix is prepended to a compaction summary when synthesising the
// system prompt for a session that has been compacted.  The phrasing is
// deliberate: it cues the model that what follows is a recap of prior
// turns, not a fresh instruction set.
const markerPrefix = "Earlier in this conversation: "

// buildRequest assembles a [provider.Request] from session state.
//
// If marker is non-nil its Summary is prepended to systemPrompt (under
// markerPrefix and a blank separator line).  We deliberately do NOT inject
// the summary as a synthetic user/assistant message: that would pollute
// the visible message stream and complicate UI rendering of the
// conversation.  System-prompt augmentation keeps the on-screen
// conversation pristine while still feeding the summary to the model.
//
// lazyBlocks, when non-empty, are formatted via
// agentsmd.BuildLazyAddition and appended to the system prompt for
// THIS TURN ONLY.  They are not persisted into session history; the
// caller (the agent loop) is responsible for draining the pending
// queue before invoking buildRequest.
//
// systemPromptAdditions are also one-turn-only additions, typically
// collected from pre_message hooks. They are intentionally kept in the
// system prompt so plugin/context text does not alter the visible user
// message stream.
//
// msgs is forwarded verbatim as Request.Messages — adapters dereference
// the slice and translate parts into their wire format.  We pass values
// (not pointers) because provider.Request.Messages is []session.Message;
// callers retain ownership of the original *Message values.
//
// modelName is the provider-side model identifier.  An empty string lets
// the adapter pick its default model.
//
// options is forwarded to Request.Options.  A nil map is fine — the
// provider treats it as "no extra knobs".
func buildRequest(
	msgs []*session.Message,
	marker *session.Marker,
	systemPrompt string,
	tools []provider.Tool,
	modelName string,
	options map[string]any,
	lazyBlocks []agentsmd.Block,
	systemPromptAdditions []string,
	reasoning provider.Reasoning,
) provider.Request {
	values := make([]session.Message, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		values = append(values, *m)
	}

	system := composeSystemPrompt(systemPrompt, marker, lazyBlocks, systemPromptAdditions)

	return provider.Request{
		ModelName: modelName,
		Messages:  values,
		System:    system,
		Tools:     tools,
		Options:   options,
		Reasoning: reasoning,
	}
}

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
