package cost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// rawModel is the permissive parse target for one model entry in the
// models.dev catalog.  Unknown fields are ignored.  Missing or wrong-typed
// numeric fields default to zero — UnmarshalJSON below handles type
// flexibility for the cost block.
type rawModel struct {
	Cost rawCost `json:"cost"`
}

// rawCost holds the four pricing fields.  All are USD per 1M tokens in the
// models.dev convention.  Missing fields parse as zero.
type rawCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// rawProvider matches the per-provider object on models.dev.  Only the
// "models" submap is meaningful for pricing; everything else is metadata we
// don't need.
type rawProvider struct {
	Models map[string]rawModel `json:"models"`
}

// fetchAndParse downloads the models.dev catalog and returns a normalized
// snapshot.  The snapshot is keyed by provider id and then model id, using
// whatever spellings the upstream catalog uses.
//
// The parser is intentionally permissive:
//   - Unknown top-level providers are kept as-is.
//   - Unknown fields inside a model object are ignored.
//   - Missing "cost" or "models" keys yield empty entries, not errors.
//   - A 2xx response with an empty or null body returns an empty snapshot
//     (the catalog will then fall through to disk-cache / fallback).
//
// A non-2xx response, a transport error, or unparseable JSON yields an
// error so [Catalog.Refresh] callers can distinguish "fetched nothing" from
// "fetch failed".
func fetchAndParse(ctx context.Context, client *http.Client, baseURL string, now time.Time) (*Snapshot, error) {
	url := strings.TrimRight(baseURL, "/") + "/api.json"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cost: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hygge/0.1 (+models-catalog)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cost: fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a small prefix of the body for the error message but do
		// not propagate it as data — we don't trust non-2xx payloads.
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return nil, fmt.Errorf("cost: fetch %s: http %d", url, resp.StatusCode)
	}

	// Cap the body size to a generous-but-finite limit (8 MiB) so a
	// pathological response can't exhaust memory.  The real catalog is
	// ~2 MiB at the time of writing.
	const maxBody = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("cost: read body: %w", err)
	}
	if len(body) == 0 {
		return &Snapshot{FetchedAt: now, Providers: map[string]map[string]Pricing{}}, nil
	}

	var raw map[string]rawProvider
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("cost: parse catalog: %w", err)
	}

	out := make(map[string]map[string]Pricing, len(raw))
	for provider, p := range raw {
		if len(p.Models) == 0 {
			continue
		}
		mods := make(map[string]Pricing, len(p.Models))
		for model, m := range p.Models {
			mods[model] = Pricing{
				Provider:          provider,
				Model:             model,
				InputPerMTok:      m.Cost.Input,
				OutputPerMTok:     m.Cost.Output,
				CacheReadPerMTok:  m.Cost.CacheRead,
				CacheWritePerMTok: m.Cost.CacheWrite,
				UpdatedAt:         now,
			}
		}
		out[provider] = mods
	}

	return &Snapshot{FetchedAt: now, Providers: out}, nil
}
