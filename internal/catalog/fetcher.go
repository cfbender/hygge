package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"

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
	meta := make(map[string]ProviderMeta, len(providers))
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
		// Always capture provider-level metadata even when there are no models
		// (unlikely, but defensive).
		pm := ProviderMeta{
			Type:        string(p.Type),
			APIEndpoint: p.APIEndpoint,
			APIKeyRef:   p.APIKey,
		}
		if len(p.DefaultHeaders) > 0 {
			pm.DefaultHeaders = make(map[string]string, len(p.DefaultHeaders))
			maps.Copy(pm.DefaultHeaders, p.DefaultHeaders)
		}
		meta[providerID] = pm
	}
	return &Snapshot{ETag: etag, Providers: out, ProvidersMeta: meta}
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
