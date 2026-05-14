package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cfbender/hygge/internal/provider"
)

// streamEventBufSize is the channel buffer between the SSE reader goroutine
// and the consumer.  Small enough to bound memory, large enough to absorb
// network jitter when the consumer is briefly slow.
const streamEventBufSize = 16

// parseStream reads an Anthropic SSE response body and emits provider.Event
// values onto out.  It closes out exactly once before returning.  ctx
// cancellation terminates the parser promptly and emits a final EventError
// with the ctx error.
func parseStream(ctx context.Context, body io.ReadCloser, out chan<- provider.Event) {
	defer close(out)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	// SSE messages can be larger than the default 64KiB token if a
	// content_block_start carries a big tool schema or a big initial
	// content block.  Bump the buffer ceiling generously.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// Per-content-block accumulator state, keyed by block index.
	type blockState struct {
		kind     string // "text" | "tool_use" | "thinking"
		toolID   string
		toolName string
		toolJSON strings.Builder // accumulated input_json_delta fragments
	}
	blocks := map[int]*blockState{}

	// Running usage merged across message_start + message_delta.
	var usage provider.Usage

	// SSE event accumulation: an event terminates on a blank line.
	var eventName, eventData string

	flush := func() bool {
		// Returns true when the parser should stop (terminal event seen).
		defer func() {
			eventName = ""
			eventData = ""
		}()

		if eventName == "" && eventData == "" {
			return false
		}

		switch eventName {
		case "message_start":
			var ev sseMessageStart
			if err := json.Unmarshal([]byte(eventData), &ev); err != nil {
				emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: decode message_start: %w", err)})
				return true
			}
			usage = mergeUsage(usage, ev.Message.Usage)
			emit(ctx, out, provider.Event{Type: provider.EventMessageStart, Usage: usage})
		case "content_block_start":
			var ev sseContentBlockStart
			if err := json.Unmarshal([]byte(eventData), &ev); err != nil {
				emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: decode content_block_start: %w", err)})
				return true
			}
			st := &blockState{kind: ev.ContentBlock.Type}
			if ev.ContentBlock.Type == "tool_use" {
				st.toolID = ev.ContentBlock.ID
				st.toolName = ev.ContentBlock.Name
				// content_block_start may include an initial input object
				// (often "{}"); we ignore it and rely solely on the
				// accumulated partial_json fragments emitted as deltas.
			}
			blocks[ev.Index] = st
		case "content_block_delta":
			var ev sseContentBlockDelta
			if err := json.Unmarshal([]byte(eventData), &ev); err != nil {
				emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: decode content_block_delta: %w", err)})
				return true
			}
			switch ev.Delta.Type {
			case "text_delta":
				emit(ctx, out, provider.Event{Type: provider.EventTextDelta, Text: ev.Delta.Text})
			case "thinking_delta":
				emit(ctx, out, provider.Event{Type: provider.EventThinkingDelta, Text: ev.Delta.Thinking})
			case "input_json_delta":
				st, ok := blocks[ev.Index]
				if !ok {
					// Defensive: tool_use delta without a prior start.
					st = &blockState{kind: "tool_use"}
					blocks[ev.Index] = st
				}
				st.toolJSON.WriteString(ev.Delta.PartialJSON)
			case "signature_delta":
				// Anthropic emits opaque signature deltas for thinking
				// blocks; we don't surface them.  Ignore.
			default:
				// Unknown delta types are forward-compat noise; ignore.
			}
		case "content_block_stop":
			var ev sseContentBlockStop
			if err := json.Unmarshal([]byte(eventData), &ev); err != nil {
				emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: decode content_block_stop: %w", err)})
				return true
			}
			st, ok := blocks[ev.Index]
			if ok && st.kind == "tool_use" {
				raw := st.toolJSON.String()
				if raw == "" {
					raw = "{}"
				}
				if !json.Valid([]byte(raw)) {
					emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: tool_use %q produced invalid JSON input: %q", st.toolName, raw)})
					return true
				}
				emit(ctx, out, provider.Event{
					Type:      provider.EventToolUse,
					ToolID:    st.toolID,
					ToolName:  st.toolName,
					ToolInput: json.RawMessage(raw),
				})
			}
			delete(blocks, ev.Index)
		case "message_delta":
			var ev sseMessageDelta
			if err := json.Unmarshal([]byte(eventData), &ev); err != nil {
				emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: decode message_delta: %w", err)})
				return true
			}
			usage = mergeUsage(usage, ev.Usage)
			emit(ctx, out, provider.Event{Type: provider.EventUsage, Usage: usage})
		case "message_stop":
			emit(ctx, out, provider.Event{Type: provider.EventDone})
			return true
		case "error":
			var ev sseError
			if err := json.Unmarshal([]byte(eventData), &ev); err != nil {
				emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: decode error event: %w", err)})
				return true
			}
			emit(ctx, out, provider.Event{Type: provider.EventError, Err: classifySSEError(ev.Error)})
			return true
		case "ping":
			// Heartbeat; ignore silently.
		default:
			// Unknown top-level event; ignore for forward compatibility.
		}
		return false
	}

	for {
		select {
		case <-ctx.Done():
			emit(ctx, out, provider.Event{Type: provider.EventError, Err: ctx.Err()})
			return
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
				emit(ctx, out, provider.Event{Type: provider.EventError, Err: fmt.Errorf("anthropic: %w: %w", provider.ErrTransient, err)})
				return
			}
			// EOF without a terminal event is a graceful close; emit Done.
			emit(ctx, out, provider.Event{Type: provider.EventDone})
			return
		}
		line := scanner.Text()
		if line == "" {
			if flush() {
				return
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(line, "data:")
			d = strings.TrimPrefix(d, " ")
			if eventData != "" {
				eventData += "\n"
			}
			eventData += d
		case strings.HasPrefix(line, ":"):
			// SSE comment line; ignore.
			_ = line
		}
	}
}

// emit sends ev on out, honouring context cancellation.  Returns false if
// the context was cancelled before the send completed.  Even on cancellation
// we attempt a non-blocking send so the caller observes a terminal error
// event whenever the channel still has capacity.
func emit(ctx context.Context, out chan<- provider.Event, ev provider.Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		select {
		case out <- ev:
		default:
		}
		return false
	}
}

// mergeUsage folds incremental usage from a single SSE event into the
// running total.  Fields present in cur replace zero-valued fields in
// running, and non-zero output_tokens from message_delta replaces the
// running zero from message_start (Anthropic's message_start carries
// input_tokens; message_delta carries final output_tokens).
func mergeUsage(running provider.Usage, cur sseUsage) provider.Usage {
	if cur.InputTokens > 0 {
		running.InputTokens = cur.InputTokens
	}
	if cur.OutputTokens > 0 {
		running.OutputTokens = cur.OutputTokens
	}
	if cur.CacheReadInputTokens > 0 {
		running.CacheReadTokens = cur.CacheReadInputTokens
	}
	if cur.CacheCreationInputTokens > 0 {
		running.CacheWriteTokens = cur.CacheCreationInputTokens
	}
	return running
}

// classifySSEError maps an Anthropic error_type string to one of the typed
// errors so the agent loop can branch on Is.
func classifySSEError(detail sseErrorDetail) error {
	switch detail.Type {
	case "overloaded_error", "api_error":
		return fmt.Errorf("%w: %s: %s", provider.ErrTransient, detail.Type, detail.Message)
	case "rate_limit_error":
		return fmt.Errorf("%w: %s", provider.ErrRateLimited, detail.Message)
	case "authentication_error", "permission_error":
		return fmt.Errorf("%w: %s", provider.ErrAuth, detail.Message)
	case "invalid_request_error":
		return fmt.Errorf("%w: %s", provider.ErrInvalidRequest, detail.Message)
	default:
		return fmt.Errorf("anthropic: %s: %s", detail.Type, detail.Message)
	}
}
