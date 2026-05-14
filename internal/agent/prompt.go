package agent

import (
	"strings"

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
) provider.Request {
	values := make([]session.Message, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		values = append(values, *m)
	}

	system := composeSystemPrompt(systemPrompt, marker)

	return provider.Request{
		ModelName: modelName,
		Messages:  values,
		System:    system,
		Tools:     tools,
		Options:   options,
	}
}

// composeSystemPrompt folds a compaction summary into a system prompt.
// When marker is nil the prompt is returned unchanged.  When marker is
// non-nil but systemPrompt is empty, the summary stands alone under the
// markerPrefix.
func composeSystemPrompt(systemPrompt string, marker *session.Marker) string {
	if marker == nil || strings.TrimSpace(marker.Summary) == "" {
		return systemPrompt
	}
	if systemPrompt == "" {
		return markerPrefix + marker.Summary
	}
	var b strings.Builder
	b.WriteString(systemPrompt)
	b.WriteString("\n\n")
	b.WriteString(markerPrefix)
	b.WriteString(marker.Summary)
	return b.String()
}
