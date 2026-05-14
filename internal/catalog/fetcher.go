package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// rawTop is the top-level wire shape: provider id -> provider object.
type rawTop map[string]rawProvider

// rawProvider matches the per-provider object on models.dev.  Only the
// "models" submap is meaningful for hygge; everything else (id, name,
// env list, npm package id, ...) is metadata we ignore.
type rawProvider struct {
	Models map[string]rawModel `json:"models"`
}

// rawModel is the permissive parse target for one model entry.  Unknown
// fields are ignored.  Field names match the live models.dev schema as
// inspected in 2026-05.
type rawModel struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	ReleaseDate string        `json:"release_date"`
	Limit       rawLimit      `json:"limit"`
	Modalities  rawModalities `json:"modalities"`
	ToolCall    bool          `json:"tool_call"`
	Reasoning   bool          `json:"reasoning"`
	Attachment  bool          `json:"attachment"`
	Cost        rawCost       `json:"cost"`
}

type rawLimit struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

type rawModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type rawCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// HTTPFetcher is the production [Fetcher] that hits models.dev.
type HTTPFetcher struct {
	client  *http.Client
	baseURL string
}

// NewHTTPFetcher builds an HTTPFetcher.  client must be non-nil;
// callers typically pass [http.DefaultClient] or a client with a custom
// timeout.
func NewHTTPFetcher(client *http.Client, baseURL string) *HTTPFetcher {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &HTTPFetcher{client: client, baseURL: baseURL}
}

// Fetch implements [Fetcher].  Returns a parsed [Snapshot] on success;
// errors cover transport failures, non-2xx responses, body-read
// failures, and malformed JSON.
//
// The fetcher is intentionally permissive about missing per-model
// fields — a model with no "cost" block parses as zero pricing rather
// than failing the whole catalog.
func (h *HTTPFetcher) Fetch(ctx context.Context) (*Snapshot, error) {
	if h.client == nil {
		return nil, errors.New("catalog: HTTPFetcher has nil http client")
	}
	url := strings.TrimRight(h.baseURL, "/") + "/api.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hygge/0.2 (+catalog)")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return nil, fmt.Errorf("fetch %s: http %d", url, resp.StatusCode)
	}

	// 16 MiB ceiling.  The live catalog is ~2 MiB; this leaves room
	// for growth without letting a pathological body exhaust memory.
	const maxBody = 16 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) == 0 {
		return &Snapshot{
			FetchedAt: time.Time{},
			Providers: map[string]map[string]Entry{},
		}, nil
	}
	return parseRawJSON(body)
}

// parseRawJSON converts a JSON payload from models.dev into a Snapshot.
// FetchedAt is left zero — callers stamp it.
func parseRawJSON(body []byte) (*Snapshot, error) {
	var raw rawTop
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	out := make(map[string]map[string]Entry, len(raw))
	for provider, p := range raw {
		if len(p.Models) == 0 {
			continue
		}
		mods := make(map[string]Entry, len(p.Models))
		for modelID, m := range p.Models {
			id := m.ID
			if id == "" {
				id = modelID
			}
			e := Entry{
				Provider:    provider,
				ID:          id,
				Name:        m.Name,
				ReleaseDate: m.ReleaseDate,
				Capabilities: Capabilities{
					Reasoning:   m.Reasoning,
					ToolCalling: m.ToolCall,
					Attachment:  m.Attachment,
				},
				Limit: Limit{
					ContextWindow: m.Limit.Context,
					MaxOutput:     m.Limit.Output,
				},
				Cost: Cost{
					Input:      m.Cost.Input,
					Output:     m.Cost.Output,
					CacheRead:  m.Cost.CacheRead,
					CacheWrite: m.Cost.CacheWrite,
				},
			}
			applyModalities(&e.Capabilities, m.Modalities)
			mods[modelID] = e
		}
		out[provider] = mods
	}
	return &Snapshot{Providers: out}, nil
}

// applyModalities translates a rawModalities into the boolean flags on
// Capabilities.  Unknown modality strings are silently ignored.
func applyModalities(c *Capabilities, m rawModalities) {
	for _, s := range m.Input {
		switch strings.ToLower(s) {
		case "text":
			c.InputText = true
		case "image":
			c.InputImages = true
		}
	}
	for _, s := range m.Output {
		switch strings.ToLower(s) {
		case "text":
			c.OutputText = true
		case "image":
			c.OutputImages = true
		}
	}
}
