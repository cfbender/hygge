package ui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
)

// OnboardingResult is the output of the onboarding wizard passed to
// AppOptions.SaveOnboardingResult.
type OnboardingResult struct {
	// ProviderName is the provider selected for the first mode.
	ProviderName string
	// ProviderAPIKey is the raw API key for ProviderName.
	ProviderAPIKey string
	// ProviderAPIKeys contains every provider API key configured during onboarding.
	ProviderAPIKeys map[string]string
	// Mode is the first mode the user created.
	Mode config.ModeConfig
	// Subagents lists the subagent definitions the user created.
	Subagents []components.OnboardingSubagentDraft
}

const generateSystemPromptInstruction = `You are a helpful assistant that writes concise AI system prompts.
When given a short description of a mode or subagent's behavior, output ONLY
the system prompt text — no explanation, no markdown fencing, no preamble.
Keep it under 250 words. Be specific and actionable.`

func generateSystemPromptUserMessage(idea string) string {
	return fmt.Sprintf("Write a system prompt for an AI agent with this behavior: %s", idea)
}

// GenerateSystemPromptWithModel uses Fantasy's shared streaming agent path to
// generate a system prompt for a mode/subagent described by idea. This avoids
// bypassing provider-specific Fantasy wiring for providers such as OpenRouter.
func GenerateSystemPromptWithModel(ctx context.Context, model fantasy.LanguageModel, idea string) (string, error) {
	if model == nil {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: model is nil")
	}
	if strings.TrimSpace(idea) == "" {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: idea is empty")
	}

	var sb strings.Builder
	res, err := fantasy.NewAgent(model, fantasy.WithTools()).Stream(ctx, fantasy.AgentStreamCall{
		Messages: []fantasy.Message{
			fantasy.NewSystemMessage(generateSystemPromptInstruction),
			fantasy.NewUserMessage(generateSystemPromptUserMessage(idea)),
		},
		OnTextDelta: func(_, delta string) error {
			sb.WriteString(delta)
			return nil
		},
	})
	if err != nil {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: stream: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: nil response from model")
	}
	result := strings.TrimSpace(sb.String())
	if result == "" {
		result = strings.TrimSpace(res.Response.Content.Text())
	}
	if result == "" {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: empty response from model")
	}
	return result, nil
}

// GenerateSystemPrompt uses the legacy provider abstraction to generate a
// system prompt. Prefer GenerateSystemPromptWithModel for production paths.
func GenerateSystemPrompt(ctx context.Context, prv provider.Provider, modelName, idea string) (string, error) {
	if prv == nil {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: provider is nil")
	}
	if strings.TrimSpace(idea) == "" {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: idea is empty")
	}

	events, err := prv.Stream(ctx, provider.Request{
		ModelName: modelName,
		System:    generateSystemPromptInstruction,
		Messages: []session.Message{
			{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: generateSystemPromptUserMessage(idea)}}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: stream: %w", err)
	}

	var sb strings.Builder
	for ev := range events {
		switch ev.Type {
		case provider.EventTextDelta:
			sb.WriteString(ev.Text)
		case provider.EventError:
			if ev.Err != nil {
				return "", fmt.Errorf("ui: GenerateSystemPrompt: provider error: %w", ev.Err)
			}
		}
	}
	result := strings.TrimSpace(sb.String())
	if result == "" {
		return "", fmt.Errorf("ui: GenerateSystemPrompt: empty response from model")
	}
	return result, nil
}
