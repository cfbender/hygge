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

	"charm.land/catwalk/pkg/catwalk"
)

// CatwalkFetcher is the production [Fetcher] that talks to the catwalk
// catalog service.  It uses ETag-based conditional requests to avoid
// transferring unchanged data.
type CatwalkFetcher struct {
	client  *http.Client
	baseURL string
}

// NewCatwalkFetcher builds a CatwalkFetcher.  client must be non-nil.
// baseURL is the catwalk server root (scheme + host, no trailing slash).
func NewCatwalkFetcher(client *http.Client, baseURL string) *CatwalkFetcher {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &CatwalkFetcher{client: client, baseURL: strings.TrimRight(baseURL, "/")}
}

// Fetch implements [Fetcher].  Returns a parsed [Snapshot] on success.
// Errors cover transport failures, non-2xx responses, body-read
// failures, and malformed JSON.
//
// ETag from the server response is stored in the returned snapshot so
// the on-disk cache can forward it on the next call.  Call
// [CatwalkFetcher.FetchWithETag] directly when you have an existing tag.
func (f *CatwalkFetcher) Fetch(ctx context.Context) (*Snapshot, error) {
	return f.FetchWithETag(ctx, "")
}

// FetchWithETag performs an ETag-conditional fetch.  When etag is
// non-empty it is sent as If-None-Match.  Returns (nil, ErrNotModified)
// when the server replies 304.
func (f *CatwalkFetcher) FetchWithETag(ctx context.Context, etag string) (*Snapshot, error) {
	if f.client == nil {
		return nil, errors.New("catalog: CatwalkFetcher has nil http client")
	}
	url := f.baseURL + "/v2/providers"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hygge/1 (+catalog)")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, ErrNotModified
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return nil, fmt.Errorf("fetch %s: http %d", url, resp.StatusCode)
	}

	// 16 MiB ceiling; the live catalog is currently small.
	const maxBody = 16 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	newETag := resp.Header.Get("ETag")
	return parseCatwalkJSON(body, newETag)
}

// ErrNotModified is returned by [CatwalkFetcher.FetchWithETag] when the
// server replies 304 Not Modified.
var ErrNotModified = errors.New("catalog: not modified (304)")

// parseCatwalkJSON converts a JSON payload from the catwalk /v2/providers
// endpoint into a Snapshot.  FetchedAt is left zero — callers stamp it.
//
// The payload is a JSON array of catwalk.Provider objects, identical in
// shape to the embedded JSON files in the catwalk module.
func parseCatwalkJSON(body []byte, etag string) (*Snapshot, error) {
	if len(body) == 0 {
		return &Snapshot{
			ETag:      etag,
			Providers: map[string]map[string]Entry{},
		}, nil
	}
	var providers []catwalk.Provider
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	return snapshotFromCatwalkProviders(providers, etag), nil
}

// snapshotFromCatwalkProviders converts a slice of catwalk.Provider
// values into a Snapshot.
func snapshotFromCatwalkProviders(providers []catwalk.Provider, etag string) *Snapshot {
	out := make(map[string]map[string]Entry, len(providers))
	for _, p := range providers {
		providerID := string(p.ID)
		if providerID == "" {
			continue
		}
		mods := make(map[string]Entry, len(p.Models))
		for _, m := range p.Models {
			if m.ID == "" {
				continue
			}
			e := entryFromCatwalkModel(providerID, m)
			mods[m.ID] = e
		}
		if len(mods) > 0 {
			out[providerID] = mods
		}
	}
	return &Snapshot{ETag: etag, Providers: out}
}

// entryFromCatwalkModel maps a single catwalk.Model to a catalog Entry.
//
// Field mapping:
//
//	catwalk.Model.CanReason             → Capabilities.Reasoning
//	catwalk.Model.SupportsImages        → Capabilities.Attachment
//	  (JSON tag: supports_attachments)
//	catwalk.Model.CostPer1MIn           → Cost.Input
//	catwalk.Model.CostPer1MOut          → Cost.Output
//	catwalk.Model.CostPer1MInCached     → Cost.CacheRead
//	catwalk.Model.CostPer1MOutCached    → Cost.CacheWrite
//	catwalk.Model.ContextWindow         → Limit.ContextWindow
//	catwalk.Model.DefaultMaxTokens      → Limit.MaxOutput
//	catwalk.Model.ReasoningLevels       → ReasoningLevels
//	catwalk.Model.DefaultReasoningEffort → DefaultReasoningEffort
//
// Note: catwalk does not expose modality flags (InputText, InputImages,
// OutputText, OutputImages) in the Model struct, so those Capabilities
// fields are left false.  ToolCalling is similarly absent; consumers
// that need it should consult the provider type (e.g. anthropic,
// openai) to determine tool support.
func entryFromCatwalkModel(providerID string, m catwalk.Model) Entry {
	return Entry{
		Provider: providerID,
		ID:       m.ID,
		Name:     m.Name,
		Capabilities: Capabilities{
			Reasoning:  m.CanReason,
			Attachment: m.SupportsImages,
		},
		Limit: Limit{
			ContextWindow: m.ContextWindow,
			MaxOutput:     m.DefaultMaxTokens,
		},
		Cost: Cost{
			Input:      m.CostPer1MIn,
			Output:     m.CostPer1MOut,
			CacheRead:  m.CostPer1MInCached,
			CacheWrite: m.CostPer1MOutCached,
		},
		ReasoningLevels:        m.ReasoningLevels,
		DefaultReasoningEffort: m.DefaultReasoningEffort,
	}
}

// ---------------------------------------------------------------------------
// Legacy models.dev fetcher — kept for test compatibility.
// ---------------------------------------------------------------------------

// HTTPFetcher is the legacy [Fetcher] that hits a models.dev-style HTTP
// endpoint.  It is still used by tests that seed a fake server with
// models.dev JSON.  Production uses [CatwalkFetcher].
type HTTPFetcher struct {
	client  *http.Client
	baseURL string
}

// NewHTTPFetcher builds a legacy HTTPFetcher.
func NewHTTPFetcher(client *http.Client, baseURL string) *HTTPFetcher {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &HTTPFetcher{client: client, baseURL: baseURL}
}

// Fetch implements [Fetcher] via the legacy models.dev /api.json endpoint.
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

// parseRawJSON converts a models.dev JSON payload into a Snapshot.
// Used only by HTTPFetcher and by the embedded snapshot transition.
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

// applyModalities translates rawModalities into Capabilities boolean flags.
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

// ---------------------------------------------------------------------------
// Legacy models.dev wire types — used by parseRawJSON above.
// ---------------------------------------------------------------------------

type rawTop map[string]rawProvider

type rawProvider struct {
	Models map[string]rawModel `json:"models"`
}

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
