// Package openrouter provides OpenRouter-specific HTTP utilities used by
// the hygge runtime.
//
// # What OpenRouter is
//
// OpenRouter is an HTTPS gateway that fronts many underlying model hosts
// (Anthropic, OpenAI, Meta, Google, Mistral, xAI, DeepSeek, ...) behind a
// single OpenAI-compatible Chat Completions API.  Users authenticate once
// against OpenRouter; OpenRouter routes each request to whichever upstream
// hosts the requested model.  Model names are namespaced as
// "<vendor>/<model>", e.g. "anthropic/claude-sonnet-4-5", "openai/gpt-5",
// "meta-llama/llama-3.3-70b-instruct".
//
// # Responsibilities
//
// This package's responsibilities are intentionally narrow:
//
//  1. Provide [RootIDCache] and [SessionHeaderTransport] for injecting
//     x-session-id on outgoing OpenRouter HTTP requests.
//  2. Provide [ContextWithSessionID] / [SessionIDFromContext] for threading
//     the current session ID through a request context.
//  3. Expose [SetCatalog] and [Models] so the CLI bootstrap and model
//     pickers can discover the live OpenRouter model catalog.
//
// Wire-protocol work — request shaping, SSE parsing, tool-call
// accumulation, error classification — lives in charm.land/fantasy/providers
// and is shared with every other OpenAI-compatible provider.
package openrouter
