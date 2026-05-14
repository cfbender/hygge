package permission

import (
	"os"
	"path/filepath"

	"github.com/cfbender/hygge/internal/state"
)

// writeStateFile writes content to the state.json file at p, creating parents.
func writeStateFile(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o600)
}

// makeStateDirReadOnly chmods the hygge state directory so Save fails.
func makeStateDirReadOnly(opts state.LoadOptions) error {
	p, err := state.Path(opts)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o500) //nolint:gosec // test: make dir read-only
}

// restoreStateDir restores write permissions so t.TempDir cleanup succeeds.
func restoreStateDir(opts state.LoadOptions) error {
	p, err := state.Path(opts)
	if err != nil {
		return err
	}
	return os.Chmod(filepath.Dir(p), 0o700) //nolint:gosec // restoring test dir permissions
}
