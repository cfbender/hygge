package catalog

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/catwalk/pkg/embedded"
)

// ProviderMeta carries provider-level metadata extracted from the Catwalk
// configuration.  It is stored per provider id alongside the model map so
// the LLM layer can construct openai-compat providers without a user-
// supplied base_url.
type ProviderMeta struct {
	// Type is the Catwalk provider type string, e.g. "openai-compat",
	// "anthropic", "openai".
	Type string `json:"type,omitempty"`

	// APIEndpoint is the provider's base URL for API requests,
	// e.g. "https://opencode.ai/zen/go/v1".  Used as base_url when
	// constructing an openai-compat fantasy provider.
	APIEndpoint string `json:"api_endpoint,omitempty"`

	// APIKeyRef is the raw api_key field from the Catwalk config.  When
	// it starts with "$" it is treated as an environment variable name,
	// e.g. "$OPENCODE_API_KEY" means os.Getenv("OPENCODE_API_KEY").
	APIKeyRef string `json:"api_key_ref,omitempty"`

	// DefaultHeaders are provider-level HTTP headers injected on every
	// request, as specified in the Catwalk provider config.
	DefaultHeaders map[string]string `json:"default_headers,omitempty"`
}

// Snapshot is the parsed, normalised in-memory and on-disk representation
// of the catalog.  Providers maps provider id to model id to [Entry].
//
// FetchedAt is the time the snapshot was produced (network fetch time
// for live data; zero for the embedded snapshot).
//
// ETag is the HTTP ETag received from the catwalk server, forwarded as
// If-None-Match on the next conditional refresh.  Empty for the embedded
// snapshot and for disk snapshots written before ETag support was added.
//
// ProvidersMeta carries provider-level metadata (type, api_endpoint, etc.)
// keyed by provider id.  Absent when loaded from an old snapshot that
// predates provider metadata support; callers must tolerate a nil map.
type Snapshot struct {
	FetchedAt     time.Time                   `json:"fetched_at"`
	ETag          string                      `json:"etag,omitempty"`
	Providers     map[string]map[string]Entry `json:"providers"`
	ProvidersMeta map[string]ProviderMeta     `json:"providers_meta,omitempty"`
}

// normalizeSnapshotKeys rebuilds the snapshot's provider and model maps
// with lowercase keys.  Lookup paths index the maps directly with
// lowercased inputs, so every snapshot ingestion point (network fetch,
// disk cache, embedded data) must yield lowercase keys.
func normalizeSnapshotKeys(snap *Snapshot) {
	if snap == nil {
		return
	}
	providers := make(map[string]map[string]Entry, len(snap.Providers))
	for pid, mods := range snap.Providers {
		lower := make(map[string]Entry, len(mods))
		for mid, e := range mods {
			lower[strings.ToLower(mid)] = e
		}
		providers[strings.ToLower(pid)] = lower
	}
	snap.Providers = providers
	if snap.ProvidersMeta != nil {
		meta := make(map[string]ProviderMeta, len(snap.ProvidersMeta))
		for pid, pm := range snap.ProvidersMeta {
			meta[strings.ToLower(pid)] = pm
		}
		snap.ProvidersMeta = meta
	}
}

// cloneProviderMeta returns a shallow copy of pm with DefaultHeaders
// cloned into a fresh map, so callers cannot mutate the snapshot's
// in-memory state through the returned value.  Handles nil map cleanly.
func cloneProviderMeta(pm ProviderMeta) ProviderMeta {
	clone := pm
	if pm.DefaultHeaders != nil {
		clone.DefaultHeaders = make(map[string]string, len(pm.DefaultHeaders))
		maps.Copy(clone.DefaultHeaders, pm.DefaultHeaders)
	}
	return clone
}

// snapshotFileFormat is the JSON shape persisted to
// $XDG_STATE_HOME/hygge/catalog.json.  We use this separate type rather
// than serialising Snapshot directly so the on-disk format stays
// versionable without churning Snapshot's in-memory shape.
type snapshotFileFormat struct {
	Version       int                         `json:"version"`
	FetchedAt     time.Time                   `json:"fetched_at"`
	ETag          string                      `json:"etag,omitempty"`
	Providers     map[string]map[string]Entry `json:"providers"`
	ProvidersMeta map[string]ProviderMeta     `json:"providers_meta,omitempty"`
}

// snapshotFileVersion is the current on-disk format version.
//
// Version history:
//
//	1 — original pre-Catwalk format
//	2 — Catwalk-backed; incompatible field set (ETag added,
//	    reasoning_levels / default_reasoning_effort added to Entry)
const snapshotFileVersion = 2

// ErrIncompatibleSnapshot marks an expected on-disk cache miss caused by an
// older cache schema. Callers should fall back without surfacing it as
// corruption.
var ErrIncompatibleSnapshot = errors.New("catalog: incompatible disk snapshot")

// loadEmbeddedSnapshot loads the catalog from the catwalk module's
// built-in provider configs.  These are the same JSON files the catwalk
// binary ships with, accessed via charm.land/catwalk/pkg/embedded.GetAll().
//
// Returns an error only if the embedded data itself is malformed, which
// would be a build-time bug.
func loadEmbeddedSnapshot() (*Snapshot, error) {
	providers := embedded.GetAll()
	if len(providers) == 0 {
		return nil, errors.New("catalog: embedded provider list is empty")
	}
	snap := snapshotFromCatwalkProviders(providers, "")
	if snap == nil || len(snap.Providers) == 0 {
		return nil, errors.New("catalog: embedded snapshot produced no providers")
	}
	// FetchedAt stays zero so Loaded.Age reflects "ancient" and the
	// background refresh fires on first run with network.
	return snap, nil
}

// readSnapshotFile reads and parses the on-disk snapshot.  Returns
// os.ErrNotExist (wrapped) for missing files so callers can branch on
// errors.Is; other errors signal corruption that the caller logs and
// falls back from.
//
// Version-1 snapshots use the previous cache schema. They are treated as a
// cache miss so upgrades fall back to the embedded snapshot and refresh without
// a user-visible corruption warning.
func readSnapshotFile(path string) (*Snapshot, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path) //nolint:gosec // intentional: XDG state path
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("catalog: disk snapshot is empty")
	}
	var f snapshotFileFormat
	if err := decodeJSON(data, &f); err != nil {
		return nil, fmt.Errorf("catalog: parse disk snapshot: %w", err)
	}
	if f.Version != snapshotFileVersion {
		return nil, fmt.Errorf("%w: version %d (want %d)", ErrIncompatibleSnapshot, f.Version, snapshotFileVersion)
	}
	if f.Providers == nil {
		f.Providers = map[string]map[string]Entry{}
	}
	snap := &Snapshot{FetchedAt: f.FetchedAt, ETag: f.ETag, Providers: f.Providers, ProvidersMeta: f.ProvidersMeta}
	normalizeSnapshotKeys(snap)
	return snap, nil
}

// writeSnapshotFile atomically writes the snapshot to disk using a
// temp-file-plus-rename dance.  The parent directory is created with
// 0o700; the file is written 0o600.
func writeSnapshotFile(path string, snap *Snapshot) error {
	if path == "" {
		return errors.New("catalog: cannot resolve snapshot path")
	}
	if snap == nil {
		return errors.New("catalog: cannot write nil snapshot")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	f := snapshotFileFormat{
		Version:       snapshotFileVersion,
		FetchedAt:     snap.FetchedAt,
		ETag:          snap.ETag,
		Providers:     snap.Providers,
		ProvidersMeta: snap.ProvidersMeta,
	}
	data, err := encodeJSONIndent(f)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	fh, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // 0o600 intentional
	if err != nil {
		return fmt.Errorf("open tmp %s: %w", tmp, err)
	}
	_, writeErr := fh.Write(data)
	syncErr := fh.Sync()
	closeErr := fh.Close()
	if writeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write tmp: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sync tmp: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", closeErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}
