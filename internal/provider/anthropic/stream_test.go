package anthropic

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/provider"
)

// nopCloser wraps an io.Reader as io.ReadCloser.
type nopCloser struct{ io.Reader }

func (nopCloser) Close() error { return nil }

func runParse(t *testing.T, sse string) []provider.Event {
	t.Helper()
	ch := make(chan provider.Event, 64)
	parseStream(t.Context(), nopCloser{strings.NewReader(sse)}, ch)
	var out []provider.Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestParseStream_ToolUseAccumulatesJSON(t *testing.T) {
	sse := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_a","name":"read","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"x\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"1}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}

`
	events := runParse(t, sse)
	var tu *provider.Event
	for i := range events {
		if events[i].Type == provider.EventToolUse {
			tu = &events[i]
		}
	}
	if tu == nil {
		t.Fatalf("no tool_use event")
	}
	if string(tu.ToolInput) != `{"x":1}` {
		t.Errorf("ToolInput: %s", tu.ToolInput)
	}
}

func TestParseStream_ToolUseInvalidJSONEmitsError(t *testing.T) {
	sse := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t","name":"x","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{not"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

`
	events := runParse(t, sse)
	last := events[len(events)-1]
	if last.Type != provider.EventError {
		t.Fatalf("expected EventError; got %v", last.Type)
	}
}

func TestParseStream_PingIgnored(t *testing.T) {
	sse := `event: ping
data: {"type":"ping"}

event: message_stop
data: {"type":"message_stop"}

`
	events := runParse(t, sse)
	for _, e := range events {
		if e.Type != provider.EventDone {
			t.Errorf("unexpected: %v", e.Type)
		}
	}
}

func TestParseStream_GracefulEOF(t *testing.T) {
	// No message_stop; parser must still emit Done on EOF.
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","usage":{"input_tokens":1}}}

`
	events := runParse(t, sse)
	last := events[len(events)-1]
	if last.Type != provider.EventDone {
		t.Errorf("expected Done on EOF; got %v", last.Type)
	}
}

func TestParseStream_CtxCanceledBeforeRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan provider.Event, 16)
	parseStream(ctx, nopCloser{strings.NewReader("")}, ch)
	var evs []provider.Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	if len(evs) == 0 {
		t.Fatal("expected at least one event")
	}
	last := evs[len(evs)-1]
	if last.Type != provider.EventError || !errors.Is(last.Err, context.Canceled) {
		t.Errorf("expected EventError with ctx.Canceled; got %v: %v", last.Type, last.Err)
	}
}

func TestParseStream_ErrorEventClassification(t *testing.T) {
	cases := []struct {
		errType string
		target  error
	}{
		{"overloaded_error", provider.ErrTransient},
		{"api_error", provider.ErrTransient},
		{"rate_limit_error", provider.ErrRateLimited},
		{"authentication_error", provider.ErrAuth},
		{"invalid_request_error", provider.ErrInvalidRequest},
	}
	for _, c := range cases {
		t.Run(c.errType, func(t *testing.T) {
			sse := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"" + c.errType + "\",\"message\":\"x\"}}\n\n"
			events := runParse(t, sse)
			last := events[len(events)-1]
			if last.Type != provider.EventError {
				t.Fatalf("expected EventError; got %v", last.Type)
			}
			if !errors.Is(last.Err, c.target) {
				t.Errorf("err class: %v vs %v", last.Err, c.target)
			}
		})
	}
}

func TestParseStream_UsageMerging(t *testing.T) {
	// message_start contributes input_tokens; message_delta contributes
	// output_tokens.  Final EventUsage should carry both.
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"x","usage":{"input_tokens":100,"output_tokens":1,"cache_read_input_tokens":5,"cache_creation_input_tokens":7}}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":200}}

event: message_stop
data: {"type":"message_stop"}

`
	events := runParse(t, sse)
	var u provider.Usage
	for _, e := range events {
		if e.Type == provider.EventUsage {
			u = e.Usage
		}
	}
	if u.InputTokens != 100 || u.OutputTokens != 200 || u.CacheReadTokens != 5 || u.CacheWriteTokens != 7 {
		t.Errorf("usage: %+v", u)
	}
}

func TestParseStream_MalformedJSONEmitsError(t *testing.T) {
	sse := "event: message_start\ndata: not-json\n\n"
	events := runParse(t, sse)
	last := events[len(events)-1]
	if last.Type != provider.EventError {
		t.Errorf("expected EventError; got %v", last.Type)
	}
}
