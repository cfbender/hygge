package openaicompat

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

// streamEventBufSize bounds the channel between the SSE reader goroutine
// and the consumer.  Small enough to bound memory, large enough to absorb
// network jitter when the consumer is briefly slow.
const streamEventBufSize = 16

// toolCallAccumulator collects argument fragments and identifying metadata
// for a single tool_call across SSE deltas.
//
// Tool calls are keyed by the integer Index in tool_calls[].index, NOT by
// the call ID.  The spec guarantees that the FIRST delta for a given index
// carries the id and function.name; subsequent deltas may carry only an
// argument fragment without re-identifying the call.  Keying by index is
// the only correct strategy.
type toolCallAccumulator struct {
	ID   string
	Name string
	Args strings.Builder
}

// parseStream reads an OpenAI-compatible Chat Completions SSE response body
// and emits provider.Event values onto out.  It closes out exactly once
// before returning.  ctx cancellation terminates the parser promptly and
// emits a final EventError with the ctx error.
//
// providerName is included in EventError messages so operators can tell
// which shim a failure originated from when several are loaded.
func parseStream(ctx context.Context, providerName string, body io.ReadCloser, out chan<- provider.Event) {
	defer close(out)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	// Chat completion chunks are small but tool-call argument deltas can
	// occasionally arrive as a single large fragment; bump the buffer
	// ceiling generously.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// Per-tool-call accumulators, keyed by tool_calls[].index.
	tools := map[int]*toolCallAccumulator{}
	// toolOrder records the order tools were first seen so we emit
	// EventToolUse deterministically when the stream finishes.
	var toolOrder []int

	var usage provider.Usage
	usageSet := false

	// emittedStart ensures we send at most one EventMessageStart.
	emittedStart := false

	// finished flags that we've already flushed pending tool_calls (e.g.
	// after seeing finish_reason).  Defensive against providers that send
	// further chunks after finish_reason for some reason.
	finished := false

	flushToolCalls := func() bool {
		if finished {
			return false
		}
		finished = true
		for _, idx := range toolOrder {
			acc := tools[idx]
			raw := acc.Args.String()
			if raw == "" {
				raw = "{}"
			}
			if !json.Valid([]byte(raw)) {
				emit(ctx, out, provider.Event{
					Type: provider.EventError,
					Err:  fmt.Errorf("%s: tool_call %q produced invalid JSON arguments: %q", providerName, acc.Name, raw),
				})
				return true
			}
			emit(ctx, out, provider.Event{
				Type:      provider.EventToolUse,
				ToolID:    acc.ID,
				ToolName:  acc.Name,
				ToolInput: json.RawMessage(raw),
			})
		}
		return false
	}

	processLine := func(line string) bool {
		if !strings.HasPrefix(line, "data:") {
			// Comments (": ...") and event: lines (OpenAI doesn't use
			// them) are ignored for forward compat.
			return false
		}
		payload := strings.TrimPrefix(line, "data:")
		payload = strings.TrimSpace(payload)
		if payload == "" {
			return false
		}
		if payload == "[DONE]" {
			// Flush any tool calls that hadn't been flushed by a
			// finish_reason.  Some providers omit finish_reason on the
			// final chunk and rely on [DONE] alone.
			if len(toolOrder) > 0 && !finished {
				if flushToolCalls() {
					return true // error already emitted
				}
			}
			if usageSet {
				emit(ctx, out, provider.Event{Type: provider.EventUsage, Usage: usage})
			}
			emit(ctx, out, provider.Event{Type: provider.EventDone})
			return true
		}

		var chunk chatResponseChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			emit(ctx, out, provider.Event{
				Type: provider.EventError,
				Err:  fmt.Errorf("%s: decode chunk: %w: %s", providerName, err, payload),
			})
			return true
		}

		// Usage chunks (with include_usage=true, the final chunk before
		// [DONE] carries usage and an empty choices array).
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
			usageSet = true
		}

		for _, ch := range chunk.Choices {
			// Initial role announcement: the first delta typically has
			// role:"assistant" and empty content.  Emit EventMessageStart
			// the first time we see it so the consumer can render a
			// blank assistant turn.
			if !emittedStart && ch.Delta.Role == "assistant" {
				emit(ctx, out, provider.Event{Type: provider.EventMessageStart})
				emittedStart = true
			}

			if ch.Delta.Content != "" {
				emit(ctx, out, provider.Event{Type: provider.EventTextDelta, Text: ch.Delta.Content})
			}

			for _, tc := range ch.Delta.ToolCalls {
				acc, ok := tools[tc.Index]
				if !ok {
					acc = &toolCallAccumulator{}
					tools[tc.Index] = acc
					toolOrder = append(toolOrder, tc.Index)
				}
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				if tc.Function != nil {
					if tc.Function.Name != "" {
						acc.Name = tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						acc.Args.WriteString(tc.Function.Arguments)
					}
				}
			}

			if ch.FinishReason != "" {
				// All tool_call argument fragments for THIS turn have
				// arrived.  Emit the accumulated tool_use events now.
				// Per the spec, finish_reason "tool_calls" guarantees
				// completeness; "stop" / "length" with no tool calls is
				// just a regular end.
				if len(toolOrder) > 0 {
					if flushToolCalls() {
						return true
					}
				}
			}
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
			// Any read termination (EOF, connection reset, transient
			// network error) before we saw [DONE] is treated as an
			// unexpected EOF.  The error is wrapped with both
			// io.ErrUnexpectedEOF and provider.ErrTransient so callers
			// can branch on either.
			err := scanner.Err()
			if err != nil && !errors.Is(err, io.EOF) {
				emit(ctx, out, provider.Event{
					Type: provider.EventError,
					Err:  fmt.Errorf("%s: %w: %w: %w", providerName, provider.ErrTransient, io.ErrUnexpectedEOF, err),
				})
				return
			}
			emit(ctx, out, provider.Event{
				Type: provider.EventError,
				Err:  fmt.Errorf("%s: %w: stream ended without [DONE]", providerName, io.ErrUnexpectedEOF),
			})
			return
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		if processLine(line) {
			return
		}
	}
}

// emit sends ev on out, honouring context cancellation.  Returns false if
// the context was cancelled before the send completed.  Even on
// cancellation we attempt a non-blocking send so the caller observes a
// terminal error event whenever the channel still has capacity.
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
