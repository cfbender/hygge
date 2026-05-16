package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
)

func TestQuestionToolPublishesAndReturnsAnswer(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)
	questions := bus.Subscribe[bus.QuestionAsked](b, bus.SubscribeOptions{BufferSize: 1})
	t.Cleanup(questions.Unsubscribe)

	tool := NewQuestionTool()
	done := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := tool.Execute(t.Context(), json.RawMessage(`{"question":"Pick one","options":["A","B"]}`), ExecContext{
			SessionID: "sess-1",
			Bus:       b,
			Now:       func() time.Time { return time.Unix(0, 0).UTC() },
		})
		if err != nil {
			errCh <- err
			return
		}
		done <- res
	}()

	var asked bus.QuestionAsked
	select {
	case asked = <-questions.C():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for QuestionAsked")
	}
	if asked.Question != "Pick one" || len(asked.Options) != 2 || asked.Options[1].Label != "B" {
		t.Fatalf("unexpected question event: %+v", asked)
	}
	bus.Publish(b, bus.QuestionAnswered{RequestID: asked.RequestID, AnswerID: "2", Answer: "B"})

	select {
	case err := <-errCh:
		t.Fatalf("Execute error: %v", err)
	case res := <-done:
		if res.IsError {
			t.Fatalf("Result.IsError = true: %+v", res)
		}
		if !strings.Contains(res.Content, "B") {
			t.Fatalf("Result.Content = %q, want answer", res.Content)
		}
		if res.Metadata["answer_id"] != "2" || res.Metadata["answer"] != "B" {
			t.Fatalf("metadata = %+v", res.Metadata)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for question result")
	}
}

func TestQuestionToolCancellationIsToolResult(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)
	questions := bus.Subscribe[bus.QuestionAsked](b, bus.SubscribeOptions{BufferSize: 1})
	t.Cleanup(questions.Unsubscribe)

	tool := NewQuestionTool()
	done := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := tool.Execute(t.Context(), json.RawMessage(`{"question":"Pick one","options":["A","B"]}`), ExecContext{SessionID: "sess-1", Bus: b})
		if err != nil {
			errCh <- err
			return
		}
		done <- res
	}()

	var asked bus.QuestionAsked
	select {
	case asked = <-questions.C():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for QuestionAsked")
	}
	bus.Publish(b, bus.QuestionAnswered{RequestID: asked.RequestID, Canceled: true})

	select {
	case err := <-errCh:
		t.Fatalf("Execute error: %v", err)
	case res := <-done:
		if !res.IsError || res.Metadata["canceled"] != true {
			t.Fatalf("expected canceled IsError result, got %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for question result")
	}
}

func TestQuestionToolValidatesOptions(t *testing.T) {
	_, err := NewQuestionTool().Execute(context.Background(), json.RawMessage(`{"question":"Pick","options":["only one"]}`), ExecContext{Bus: bus.New()})
	if err == nil {
		t.Fatal("expected validation error")
	}
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != CodeInvalidArgs {
		t.Fatalf("expected invalid args ToolError, got %T %v", err, err)
	}
}
