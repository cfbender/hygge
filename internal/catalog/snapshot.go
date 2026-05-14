package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

//go:embed snapshot.json
var embeddedSnapshot []byte

// Snapshot is the parsed, normalised in-memory and on-disk representation
// of the catalog.  Providers maps provider id to model id to [Entry].
//
// FetchedAt is the time the snapshot was produced (network fetch time
// for live data; build time for the embedded snapshot, which lands as
// the zero time since we don't bake a timestamp into the file).
type Snapshot struct {
	FetchedAt time.Time                   `json:"fetched_at"`
	Providers map[string]map[string]Entry `json:"providers"`
}

// snapshotFileFormat is the JSON shape persisted to
// $XDG_STATE_HOME/hygge/catalog.json.  We use this separate type rather
// than serialising Snapshot directly so the on-disk format stays
// versionable without churning Snapshot's in-memory shape.
type snapshotFileFormat struct {
	Version   int                         `json:"version"`
	FetchedAt time.Time                   `json:"fetched_at"`
	Providers map[string]map[string]Entry `json:"providers"`
}

const snapshotFileVersion = 1

// loadEmbeddedSnapshot parses the snapshot.json compiled into the
// binary.  The embedded file uses the live models.dev wire format
// (top-level provider keys, nested models map) — the same format the
// HTTPFetcher consumes — so we parse it through the same path.
//
// Returns an error only if the embedded file itself is malformed,
// which would be a build-time bug.
func loadEmbeddedSnapshot() (*Snapshot, error) {
	if len(embeddedSnapshot) == 0 {
		return nil, errors.New("catalog: embedded snapshot is empty")
	}
	snap, err := parseRawJSON(embeddedSnapshot)
	if err != nil {
		return nil, fmt.Errorf("catalog: parse embedded snapshot: %w", err)
	}
	// FetchedAt stays zero so Loaded.Age reflects "ancient" and the
	// background refresh fires on first run with network.
	return snap, nil
}

// readSnapshotFile reads and parses the on-disk snapshot.  Returns
// os.ErrNotExist (wrapped) for missing files so callers can branch on
// errors.Is; other errors signal corruption that the caller logs and
// falls back from.
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
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("catalog: parse disk snapshot: %w", err)
	}
	if f.Version != snapshotFileVersion {
		return nil, fmt.Errorf("catalog: unsupported snapshot version %d (want %d)", f.Version, snapshotFileVersion)
	}
	if f.Providers == nil {
		f.Providers = map[string]map[string]Entry{}
	}
	return &Snapshot{FetchedAt: f.FetchedAt, Providers: f.Providers}, nil
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
		Version:   snapshotFileVersion,
		FetchedAt: snap.FetchedAt,
		Providers: snap.Providers,
	}
	data, err := json.MarshalIndent(f, "", "  ")
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
