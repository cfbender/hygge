package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/fantasy"
)

type promptFantasyModel struct {
	call      fantasy.Call
	text      string
	streamErr error
}

func (m *promptFantasyModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, nil
}

func (m *promptFantasyModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.call = call
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return func(yield func(fantasy.StreamPart) bool) {
		if m.text != "" {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "prompt"}) {
				return
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "prompt", Delta: m.text}) {
				return
			}
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "prompt"}) {
				return
			}
		}
		yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop})
	}, nil
}

func (m *promptFantasyModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, nil
}

func (m *promptFantasyModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}

func (m *promptFantasyModel) Provider() string { return "test" }
func (m *promptFantasyModel) Model() string    { return "test-model" }

func TestGenerateSystemPromptWithModelStreamsViaFantasy(t *testing.T) {
	model := &promptFantasyModel{text: "You are concise."}

	got, err := GenerateSystemPromptWithModel(t.Context(), model, "be concise")
	if err != nil {
		t.Fatalf("GenerateSystemPromptWithModel: %v", err)
	}
	if got != "You are concise." {
		t.Fatalf("prompt = %q, want %q", got, "You are concise.")
	}
	if len(model.call.Prompt) != 2 {
		t.Fatalf("messages = %d, want 2", len(model.call.Prompt))
	}
	if model.call.Prompt[0].Role != fantasy.MessageRoleSystem {
		t.Fatalf("first message role = %q, want system", model.call.Prompt[0].Role)
	}
	if model.call.Prompt[1].Role != fantasy.MessageRoleUser {
		t.Fatalf("second message role = %q, want user", model.call.Prompt[1].Role)
	}
	if got := promptText(model.call.Prompt[0]); !strings.Contains(got, "output ONLY") {
		t.Fatalf("system prompt = %q, want generation instruction", got)
	}
	if got := promptText(model.call.Prompt[1]); !strings.Contains(got, "be concise") {
		t.Fatalf("user prompt = %q, want idea", got)
	}
}

func TestGenerateSystemPromptWithModelReturnsStreamError(t *testing.T) {
	boom := errors.New("boom")
	_, err := GenerateSystemPromptWithModel(t.Context(), &promptFantasyModel{streamErr: boom}, "be concise")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped boom", err)
	}
}

func TestGenerateSystemPromptWithModelRejectsEmptyResponse(t *testing.T) {
	_, err := GenerateSystemPromptWithModel(t.Context(), &promptFantasyModel{}, "be concise")
	if err == nil {
		t.Fatal("GenerateSystemPromptWithModel succeeded with empty response, want error")
	}
}

func promptText(msg fantasy.Message) string {
	var out strings.Builder
	for _, part := range msg.Content {
		text, ok := fantasy.AsMessagePart[fantasy.TextPart](part)
		if ok {
			out.WriteString(text.Text)
		}
	}
	return out.String()
}
