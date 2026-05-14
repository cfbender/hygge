// Package catalog is hygge's central source of truth for model metadata:
// pricing, capabilities, context-window limits, and modalities.
//
// # Sources
//
// Data is sourced from models.dev with three layers of fallback:
//
//  1. A disk-cached snapshot at $XDG_STATE_HOME/hygge/catalog.json,
//     refreshed on demand via [Catalog.Refresh] (which is wired to the
//     `hygge catalog refresh` CLI command).
//  2. An embedded snapshot.json compiled into the binary.  This is the
//     bedrock fallback: hygge always has at least this catalog available
//     even when offline and no disk cache exists.
//  3. An optional background refresh kicked off at [Load] time when the
//     disk cache is missing or older than [LoadOptions.MaxStaleness].
//     The refresh runs in a goroutine and NEVER blocks startup.
//  4. An optional periodic ticker, started when [LoadOptions.RefreshInterval]
//     is positive, that calls [Catalog.Refresh] at that cadence.  This
//     lets a long-lived hygge process pick up upstream catalog changes
//     without a restart.  The ticker exits promptly on [Catalog.Close].
//
// # Schema
//
// Parsed against the live models.dev /api.json schema as of 2026-05.  The
// fields we depend on are:
//
//	{
//	  "<provider>": {
//	    "models": {
//	      "<model-id>": {
//	        "id":            string,
//	        "name":          string,
//	        "release_date":  string,
//	        "limit":         { "context": int, "output": int },
//	        "modalities":    { "input": [string...], "output": [string...] },
//	        "tool_call":     bool,
//	        "reasoning":     bool,
//	        "attachment":    bool,
//	        "cost":          { "input": float, "output": float,
//	                          "cache_read": float, "cache_write": float }
//	      }
//	    }
//	  }
//	}
//
// Unknown top-level providers and unknown fields inside model entries are
// preserved as best-effort metadata or dropped silently — never an error.
//
// # Concurrency
//
// [Catalog] is safe for concurrent reads.  Refresh holds an internal
// write lock for the duration of the network fetch and disk write.
// The periodic-refresh ticker runs in its own goroutine; concurrent
// calls to Refresh (from the ticker, from backgroundRefresh, and from
// `hygge catalog refresh`) are serialised by the internal refreshing
// mutex so only one network fetch is in flight at a time.
//
// # Boundaries
//
// This package depends only on the standard library.  It must not import
// internal/agent, internal/store, internal/provider, or internal/cost.
// Both internal/cost and internal/provider/* consume a [*Catalog] handed
// to them by the cmd/hygge/cli bootstrap; this package never reaches
// up.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the canonical models.dev catalog host.  The full URL
// fetched is BaseURL + "/api.json".
const DefaultBaseURL = "https://models.dev"

// DefaultMaxStaleness is the freshness window used when [LoadOptions]
// does not set one explicitly.  After this much time has passed since the
// disk snapshot's FetchedAt, [Load] schedules a background refresh.
const DefaultMaxStaleness = 7 * 24 * time.Hour

// DefaultHTTPTimeout caps the HTTP request when no client is injected.
const DefaultHTTPTimeout = 15 * time.Second

// Source identifies which layer in the resolution cascade produced the
// current in-memory snapshot.  Surfaced via [Catalog.Loaded] for
// diagnostics; not used for correctness.
type Source string

const (
	// SourceEmbedded means the snapshot came from the bundled
	// snapshot.json embedded into the binary at build time.
	SourceEmbedded Source = "embedded"
	// SourceDisk means the snapshot was read from the on-disk cache
	// at $XDG_STATE_HOME/hygge/catalog.json.
	SourceDisk Source = "disk"
	// SourceNetwork means the snapshot was just fetched from
	// models.dev.
	SourceNetwork Source = "network"
)

// Entry is one model in the catalog: a flat denormalised view across the
// provider, id, capability flags, limits, and pricing.
type Entry struct {
	// Provider is the canonical models.dev provider id, e.g. "anthropic"
	// or "openai".  Lowercase, no spaces.
	Provider string

	// ID is the bare model id, e.g. "claude-sonnet-4-5".  For
	// providers that namespace their ids (OpenRouter uses
	// "<vendor>/<model>"), the id retains its full namespace form
	// as the catalog publishes it.
	ID string

	// Name is the human-readable display name from models.dev, e.g.
	// "Claude Sonnet 4.5 (latest)".  May be empty when the upstream
	// catalog omits it.
	Name string

	// Capabilities collects the boolean feature flags advertised by
	// the upstream catalog.
	Capabilities Capabilities

	// Limit reports the context-window and per-call output caps.
	Limit Limit

	// Cost is the per-million-token pricing.  Zero values mean "this
	// model does not charge for that token class" (e.g. no caching).
	Cost Cost

	// ReleaseDate is a free-form date string from upstream
	// ("2025-09-29", "Q3 2025", etc.).  May be empty.
	ReleaseDate string

	// Source identifies which layer produced this entry.  Set when the
	// Catalog hands an Entry to a caller; not persisted in JSON.
	Source Source `json:"-"`
}

// Capabilities is the set of boolean feature flags upstream models.dev
// advertises.  Zero value = "not advertised" — callers should treat that
// as "unknown / probably false".
type Capabilities struct {
	// Reasoning indicates the model produces an explicit thinking /
	// reasoning trace (OpenAI's o-series, Anthropic with the thinking
	// block, etc.).
	Reasoning bool

	// ToolCalling indicates the model supports function/tool-use
	// blocks in the assistant turn.
	ToolCalling bool

	// Attachment indicates the model accepts file attachments.
	// Distinct from InputImages: a model can accept image input
	// inline (in a chat block) without supporting attachment APIs.
	Attachment bool

	// InputText indicates the model accepts text input.  Set by
	// inspecting modalities.input for the "text" element.
	InputText bool

	// InputImages indicates the model accepts image input.
	InputImages bool

	// OutputText indicates the model emits text output.
	OutputText bool

	// OutputImages indicates the model emits image output (rare).
	OutputImages bool
}

// Limit is the context-window and per-call output token cap.  Zero means
// the upstream catalog did not publish a value.
type Limit struct {
	ContextWindow int64
	MaxOutput     int64
}

// Cost is the per-1-million-token pricing in USD.  Same shape as
// internal/cost's Pricing but lives in this package to keep the boundary
// clean.
type Cost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// LoadOptions configures [Load].  Zero value is valid and yields a
// production-defaults Catalog.
type LoadOptions struct {
	// StateDir is the directory the disk snapshot lives in.  The
	// actual file is <StateDir>/catalog.json.  Empty falls back to
	// $XDG_STATE_HOME/hygge (or ~/.local/state/hygge).
	StateDir string

	// Source is an injectable fetcher for tests.  Nil uses the real
	// models.dev fetcher.
	Source Fetcher

	// HTTPClient is used for live fetches when Source is nil.  Nil
	// defaults to an [http.Client] with [DefaultHTTPTimeout].
	HTTPClient *http.Client

	// BaseURL overrides the models.dev host when Source is nil.
	// Empty falls back to [DefaultBaseURL].  Tests point this at an
	// httptest server.
	BaseURL string

	// Now is an injectable clock for tests.  Nil uses [time.Now].
	Now func() time.Time

	// BackgroundRefresh enables the on-Load goroutine that refreshes
	// stale snapshots in the background.  Defaults to true; tests
	// typically set it to false for determinism.
	BackgroundRefresh *bool

	// MaxStaleness is the age beyond which an on-disk snapshot is
	// considered stale and triggers the background refresh.  Zero
	// defaults to [DefaultMaxStaleness].
	MaxStaleness time.Duration

	// RefreshInterval, when positive, starts a background ticker that
	// calls [Catalog.Refresh] at that cadence.  Zero (the default)
	// means no periodic refresh — the one-shot background refresh
	// still fires on startup when BackgroundRefresh is enabled.
	//
	// The ticker exits promptly when [Catalog.Close] is called.
	// Configured via [catalog] refresh_interval in config.toml.
	RefreshInterval time.Duration
}

// Fetcher is the source interface the Catalog uses to obtain a fresh
// snapshot.  Production wires this to the real models.dev fetcher; tests
// inject a stub.
type Fetcher interface {
	// Fetch returns a populated snapshot, or an error.  The snapshot's
	// FetchedAt field will be overwritten by the Catalog using the
	// current clock — implementations need not set it.
	Fetch(ctx context.Context) (*Snapshot, error)
}

// RefreshResult is returned by [Catalog.Refresh].
type RefreshResult struct {
	// Providers is the number of distinct provider ids in the new
	// snapshot.
	Providers int
	// Models is the total model count across all providers.
	Models int
	// FetchedAt is when the new snapshot was produced.
	FetchedAt time.Time
	// PreviousAge is the age of the snapshot the new fetch replaced,
	// or zero when no prior snapshot existed.
	PreviousAge time.Duration
}

// Loaded summarises the in-memory snapshot's provenance and age.
// Returned by [Catalog.Loaded] for diagnostic surfaces.
type Loaded struct {
	Source      Source
	FetchedAt   time.Time
	Age         time.Duration
	Providers   int
	Models      int
	StateDir    string
	SnapshotURL string
}

// Catalog is the public handle.  Construct via [Load].
type Catalog struct {
	statePath  string
	source     Fetcher
	now        func() time.Time
	maxStale   time.Duration
	bgRefresh  bool
	httpClient *http.Client
	baseURL    string

	mu       sync.RWMutex
	snapshot *Snapshot
	src      Source

	// refreshing serialises Refresh calls and the background-refresh
	// goroutine so we never issue two concurrent network fetches.
	refreshing sync.Mutex

	// ticker fields for periodic refresh.  All three are nil/zero
	// when RefreshInterval was not set.
	tickerCancel context.CancelFunc
	tickerDone   chan struct{}
}

// Load constructs a Catalog.  Always returns a usable handle: even when
// the disk cache is missing AND the background refresh fails, the
// embedded snapshot is loaded so [Catalog.Lookup] still answers.
//
// Errors are returned only for catastrophic situations — for example, the
// embedded snapshot itself failing to parse, which would mean the binary
// was built wrong.
func Load(opts LoadOptions) (*Catalog, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MaxStaleness <= 0 {
		opts.MaxStaleness = DefaultMaxStaleness
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	bgRefresh := true
	if opts.BackgroundRefresh != nil {
		bgRefresh = *opts.BackgroundRefresh
	}
	src := opts.Source
	if src == nil {
		src = NewHTTPFetcher(opts.HTTPClient, baseURL)
	}

	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = defaultStateDir()
	}
	statePath := ""
	if stateDir != "" {
		statePath = filepath.Join(stateDir, "catalog.json")
	}

	c := &Catalog{
		statePath:  statePath,
		source:     src,
		now:        opts.Now,
		maxStale:   opts.MaxStaleness,
		bgRefresh:  bgRefresh,
		httpClient: opts.HTTPClient,
		baseURL:    baseURL,
	}

	// 1. Try disk.
	if snap, err := readSnapshotFile(statePath); err == nil && snap != nil {
		c.snapshot = snap
		c.src = SourceDisk
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("catalog: failed to read disk snapshot; falling back to embedded",
			"path", statePath, "err", err)
	}

	// 2. Fall back to embedded.
	if c.snapshot == nil {
		snap, err := loadEmbeddedSnapshot()
		if err != nil {
			return nil, fmt.Errorf("catalog: load embedded snapshot: %w", err)
		}
		c.snapshot = snap
		c.src = SourceEmbedded
	}

	// 3. Schedule background refresh when stale.
	if bgRefresh && c.shouldBackgroundRefresh() {
		go c.backgroundRefresh()
	}

	// 4. Start periodic ticker when configured.
	if opts.RefreshInterval > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		c.tickerCancel = cancel
		c.tickerDone = make(chan struct{})
		go c.tickerLoop(ctx, opts.RefreshInterval)
	}

	return c, nil
}

// shouldBackgroundRefresh reports whether the current snapshot is older
// than maxStale.  Called under no lock — relies on snapshot being set
// before backgroundRefresh fires.
func (c *Catalog) shouldBackgroundRefresh() bool {
	c.mu.RLock()
	snap := c.snapshot
	c.mu.RUnlock()
	if snap == nil {
		return true
	}
	return c.now().Sub(snap.FetchedAt) > c.maxStale
}

// backgroundRefresh runs in a goroutine to refresh the snapshot when
// stale.  Never blocks startup, never panics.  Failures log a Warn and
// leave the existing snapshot in place.
func (c *Catalog) backgroundRefresh() {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultHTTPTimeout+5*time.Second)
	defer cancel()
	result, err := c.Refresh(ctx)
	if err != nil {
		slog.Warn("catalog: background refresh failed", "err", err)
		return
	}
	slog.Debug("catalog: background refresh succeeded",
		"providers", result.Providers,
		"models", result.Models)
}

// tickerLoop runs in a goroutine and calls Refresh at the given interval
// until ctx is cancelled.  It is started by Load when RefreshInterval > 0
// and exits promptly when Close is called (which cancels ctx).
func (c *Catalog) tickerLoop(ctx context.Context, d time.Duration) {
	defer close(c.tickerDone)
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := c.Refresh(ctx); err != nil {
				slog.Warn("catalog: periodic refresh failed", "err", err)
			}
		}
	}
}

// Close stops the periodic-refresh ticker (if any) and waits for its
// goroutine to exit.  Idempotent.  Returns nil.
//
// Callers that hold a long-lived Catalog (e.g. the CLI runtime) should
// defer Close so the ticker goroutine is not leaked after shutdown.
func (c *Catalog) Close() error {
	if c.tickerCancel != nil {
		c.tickerCancel()
		<-c.tickerDone
	}
	return nil
}

// Lookup returns the catalog entry for (provider, model).  Both
// arguments are matched against the canonical id used by models.dev.
// Returns ok=false when not found.
//
// The lookup is case-insensitive on provider and on model, since
// upstream provider ids are sometimes spelled differently across hygge's
// surfaces (e.g. user typing "Anthropic" in a config field) but
// models.dev canonicalises to lowercase.
func (c *Catalog) Lookup(provider, model string) (Entry, bool) {
	if provider == "" || model == "" {
		return Entry{}, false
	}
	c.mu.RLock()
	snap := c.snapshot
	src := c.src
	c.mu.RUnlock()
	if snap == nil {
		return Entry{}, false
	}
	pkey := strings.ToLower(provider)
	mkey := strings.ToLower(model)
	mods, ok := snap.Providers[pkey]
	if !ok {
		// Try the original spelling as a fallback in case the
		// upstream catalog keeps a non-lowercase provider id.
		mods, ok = snap.Providers[provider]
		if !ok {
			return Entry{}, false
		}
	}
	if e, ok := mods[mkey]; ok {
		e.Source = src
		return e, true
	}
	if e, ok := mods[model]; ok {
		e.Source = src
		return e, true
	}
	// One final pass: walk the map case-insensitively.  Rare path —
	// only triggers when the upstream catalog uses mixed-case ids.
	for k, e := range mods {
		if strings.EqualFold(k, model) {
			e.Source = src
			return e, true
		}
	}
	return Entry{}, false
}

// Models returns every entry for the given provider, sorted by id.
// Returns an empty slice when no provider matches.
func (c *Catalog) Models(provider string) []Entry {
	if provider == "" {
		return nil
	}
	c.mu.RLock()
	snap := c.snapshot
	src := c.src
	c.mu.RUnlock()
	if snap == nil {
		return nil
	}
	pkey := strings.ToLower(provider)
	mods, ok := snap.Providers[pkey]
	if !ok {
		mods, ok = snap.Providers[provider]
		if !ok {
			return nil
		}
	}
	out := make([]Entry, 0, len(mods))
	for _, e := range mods {
		e.Source = src
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Providers returns the sorted list of provider ids in the current
// snapshot.
func (c *Catalog) Providers() []string {
	c.mu.RLock()
	snap := c.snapshot
	c.mu.RUnlock()
	if snap == nil {
		return nil
	}
	out := make([]string, 0, len(snap.Providers))
	for k := range snap.Providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Refresh fetches a fresh snapshot from the source and persists it to
// disk.  Blocking.  Single-flight: concurrent Refresh calls collapse to
// one underlying fetch.
//
// On success the in-memory snapshot is replaced and the disk cache is
// rewritten atomically.  On failure the previous snapshot is preserved
// and the error is returned.
func (c *Catalog) Refresh(ctx context.Context) (RefreshResult, error) {
	c.refreshing.Lock()
	defer c.refreshing.Unlock()

	snap, err := c.source.Fetch(ctx)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("catalog: refresh: %w", err)
	}
	if snap == nil {
		return RefreshResult{}, errors.New("catalog: source returned nil snapshot")
	}
	snap.FetchedAt = c.now()

	// Snap the previous age before we swap.
	c.mu.RLock()
	prev := c.snapshot
	c.mu.RUnlock()
	var prevAge time.Duration
	if prev != nil && !prev.FetchedAt.IsZero() {
		prevAge = c.now().Sub(prev.FetchedAt)
	}

	c.mu.Lock()
	c.snapshot = snap
	c.src = SourceNetwork
	c.mu.Unlock()

	if c.statePath != "" {
		if err := writeSnapshotFile(c.statePath, snap); err != nil {
			// Disk-write failure is not fatal: the in-memory snapshot
			// is correct and serves subsequent Lookups.
			slog.Warn("catalog: failed to write disk snapshot",
				"path", c.statePath, "err", err)
		}
	}

	providers := len(snap.Providers)
	models := 0
	for _, m := range snap.Providers {
		models += len(m)
	}
	return RefreshResult{
		Providers:   providers,
		Models:      models,
		FetchedAt:   snap.FetchedAt,
		PreviousAge: prevAge,
	}, nil
}

// Loaded reports the in-memory snapshot's provenance, age, and size.
// Useful for `hygge catalog list` and diagnostic logging.
func (c *Catalog) Loaded() Loaded {
	c.mu.RLock()
	snap := c.snapshot
	src := c.src
	c.mu.RUnlock()
	if snap == nil {
		return Loaded{}
	}
	models := 0
	for _, m := range snap.Providers {
		models += len(m)
	}
	stateDir := ""
	if c.statePath != "" {
		stateDir = filepath.Dir(c.statePath)
	}
	return Loaded{
		Source:      src,
		FetchedAt:   snap.FetchedAt,
		Age:         c.now().Sub(snap.FetchedAt),
		Providers:   len(snap.Providers),
		Models:      models,
		StateDir:    stateDir,
		SnapshotURL: c.baseURL,
	}
}

// StatePath returns the absolute path of the on-disk snapshot file, or
// the empty string when no state directory was resolved.
func (c *Catalog) StatePath() string { return c.statePath }

// defaultStateDir returns $XDG_STATE_HOME/hygge with the usual XDG
// fallback to ~/.local/state/hygge.  Returns "" when even the home
// directory cannot be resolved (rare; CI containers with no HOME).
func defaultStateDir() string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "hygge")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "hygge")
}
