package tool

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cfbender/hygge/internal/bus"
)

// QuestionTool asks the user to choose between bounded options and returns the
// selected answer to the model.
type QuestionTool struct{}

// NewQuestionTool constructs the interactive question tool.
func NewQuestionTool() *QuestionTool { return &QuestionTool{} }

func (t *QuestionTool) Name() string { return "question" }

func (t *QuestionTool) Description() string {
	return "Ask the user a multiple-choice question when you need a bounded decision before continuing. " +
		"Provide concise options; the tool blocks until the user chooses or cancels."
}

func (t *QuestionTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"question", "options"},
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "The concise question to show the user.",
			},
			"options": map[string]any{
				"type":        "array",
				"minItems":    2,
				"maxItems":    9,
				"description": "Two to nine short answer options.",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
	}
}

func (t *QuestionTool) Parallelizable() bool { return false }

type questionArgs struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

func (t *QuestionTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	if ec.Bus == nil {
		return Result{}, newExecutionFailed("question bus not configured", nil)
	}

	var a questionArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	a.Question = strings.TrimSpace(a.Question)
	if a.Question == "" {
		return Result{}, newInvalidArgs("question is required", nil)
	}
	options := normalizeQuestionOptions(a.Options)
	if len(options) < 2 {
		return Result{}, newInvalidArgs("at least two non-empty options are required", nil)
	}
	if len(options) > 9 {
		return Result{}, newInvalidArgs("at most nine options are supported", nil)
	}

	reqID, err := newQuestionRequestID()
	if err != nil {
		return Result{}, newExecutionFailed("generate question request id", err)
	}
	sub := bus.Subscribe[bus.QuestionAnswered](ec.Bus, bus.SubscribeOptions{BufferSize: 8})
	defer sub.Unsubscribe()

	asked := bus.QuestionAsked{
		RequestID: reqID,
		SessionID: ec.SessionID,
		ToolName:  t.Name(),
		Question:  a.Question,
		Options:   make([]bus.QuestionOption, len(options)),
		At:        ec.nowFn()(),
	}
	for i, option := range options {
		asked.Options[i] = bus.QuestionOption{ID: fmt.Sprintf("%d", i+1), Label: option}
	}
	bus.Publish(ec.Bus, asked)

	for {
		select {
		case <-ctx.Done():
			return Result{}, newExecutionFailed("question canceled", ctx.Err())
		case ans, ok := <-sub.C():
			if !ok {
				return Result{}, newExecutionFailed("question bus closed", nil)
			}
			if ans.RequestID != reqID {
				continue
			}
			if ans.Canceled {
				return Result{IsError: true, Content: "user canceled the question", Metadata: map[string]any{"canceled": true}}, nil
			}
			answer := strings.TrimSpace(ans.Answer)
			answerID := strings.TrimSpace(ans.AnswerID)
			if answer == "" {
				answer = answerByID(options, answerID)
			}
			return Result{
				Content: fmt.Sprintf("User selected: %s", answer),
				Metadata: map[string]any{
					"answer_id": answerID,
					"answer":    answer,
				},
			}, nil
		}
	}
}

func normalizeQuestionOptions(in []string) []string {
	out := make([]string, 0, len(in))
	for _, option := range in {
		option = strings.TrimSpace(option)
		if option != "" {
			out = append(out, option)
		}
	}
	return out
}

func answerByID(options []string, id string) string {
	for i, option := range options {
		if id == fmt.Sprintf("%d", i+1) {
			return option
		}
	}
	return ""
}

func newQuestionRequestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "question-" + hex.EncodeToString(b[:]), nil
}
