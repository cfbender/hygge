// Package cli — plugin and project-context wiring: autoload plugin
// discovery and the lazy AGENTS.md tracker.
package cli

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cfbender/hygge/internal/agentsmd"
)

// buildLazyTracker constructs the per-tool-call subdir context
// tracker the agent loop hands every touched path to.  Returns nil
// (lazy loading disabled) when no project root could be discovered
// from pwd, or when no markers exist at all.  Seeds the tracker's
// seen-dir set with every directory whose context was already loaded
// at bootstrap so those files are never re-injected.
func buildLazyTracker(homeDir, pwd string, loaded []agentsmd.Block) *agentsmd.LazyTracker {
	root := agentsmd.FindProjectRootFrom(pwd)
	if root == "" {
		slog.Warn("cli: lazy context disabled (no project root found)",
			"pwd", pwd)
		return nil
	}
	seenDirs := make([]string, 0, len(loaded))
	for _, b := range loaded {
		if b.Path == "" {
			continue
		}
		seenDirs = append(seenDirs, filepath.Dir(b.Path))
	}
	return agentsmd.NewLazyTracker(homeDir, root, seenDirs)
}

func pluginSourcesWithAutoload(configured []string, xdgConfig, pwd string) []string {
	sources := make([]string, 0, len(configured)+4)
	seen := make(map[string]struct{}, len(configured)+4)
	add := func(source string) {
		if _, ok := seen[source]; ok {
			return
		}
		seen[source] = struct{}{}
		sources = append(sources, source)
	}
	for _, source := range configured {
		add(source)
	}
	for _, source := range discoverAutoloadPluginSources(xdgConfig, pwd) {
		add(source)
	}
	return sources
}

func discoverAutoloadPluginSources(xdgConfig, pwd string) []string {
	var roots []string
	if pwd != "" {
		roots = append(roots, filepath.Join(pwd, ".hygge", "plugins"))
	}
	if xdgConfig != "" {
		roots = append(roots, filepath.Join(xdgConfig, "hygge", "plugins"))
	}

	var sources []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			slog.Warn("cli: failed to read autoload plugins directory", "path", root, "err", err)
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(root, entry.Name())
			if !autoloadPluginDir(dir) {
				continue
			}
			sources = append(sources, "local:"+dir)
		}
	}
	return sources
}

func autoloadPluginDir(dir string) bool {
	for _, name := range []string{"plugin.toml", "plugin.lua"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
