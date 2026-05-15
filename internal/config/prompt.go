package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePrompt resolves a prompt string. If it starts with "file:" the
// remainder is treated as a file path and the file contents are returned.
// Relative paths are resolved against baseDir (typically XDG_CONFIG_HOME/hygge).
// If the file cannot be read, the raw string is returned with a warning.
func ResolvePrompt(raw string, baseDir string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if !strings.HasPrefix(raw, "file:") {
		return raw
	}

	path := strings.TrimSpace(strings.TrimPrefix(raw, "file:"))
	if path == "" {
		return ""
	}

	// Expand ~ to home directory.
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}

	// Resolve relative paths against baseDir.
	if !filepath.IsAbs(path) && baseDir != "" {
		path = filepath.Join(baseDir, path)
	}

	data, err := os.ReadFile(path) //nolint:gosec // path from user config
	if err != nil {
		slog.Warn("config: could not read prompt file; using raw value",
			"path", path, "err", err)
		return raw
	}

	return strings.TrimSpace(string(data))
}
