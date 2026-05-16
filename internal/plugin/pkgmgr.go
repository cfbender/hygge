package plugin

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cfbender/hygge/internal/gitexec"
)

// SourceKind is the type of plugin source URI.
type SourceKind string

// Source kind constants.
const (
	// SourceGitHub is a plugin sourced from a GitHub repository.
	SourceGitHub SourceKind = "github"
	// SourceLocal is a plugin sourced from a local filesystem path.
	SourceLocal SourceKind = "local"
)

// Source represents a parsed plugin source URI.
type Source struct {
	Kind   SourceKind
	Raw    string // the original URI string
	User   string // github only: owner
	Repo   string // github only: repository
	Path   string // local only: filesystem path (absolute)
	Ref    string // github only: tag, branch, or commit sha. "" = default branch
	Branch bool   // true when Ref was specified with # (known branch, not tag)
}

// ParseSource parses a plugin source URI.
//
// Grammar:
//
//	github:USER/REPO             → default branch
//	github:USER/REPO@REF        → tag, commit, or branch via @ separator
//	github:USER/REPO#BRANCH     → explicitly a branch (use # to distinguish from tags)
//	local:/abs/path             → no clone, use path directly
//	npm:...                     → error: not supported in v0.3
func ParseSource(uri string) (Source, error) {
	scheme, rest, ok := strings.Cut(uri, ":")
	if !ok {
		return Source{}, fmt.Errorf("plugin: invalid source URI %q: missing scheme (expected github: or local:)", uri)
	}

	switch scheme {
	case "npm":
		return Source{}, fmt.Errorf(
			"plugin: npm: sources are not supported in v0.3. " +
				"See README for future plans; Lua plugins are the supported format",
		)
	case "local":
		p := rest
		// Expand ~ prefix.
		if strings.HasPrefix(p, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return Source{}, fmt.Errorf("plugin: local: expand ~: %w", err)
			}
			p = filepath.Join(home, p[2:])
		}
		if !filepath.IsAbs(p) {
			return Source{}, fmt.Errorf("plugin: local: path %q must be absolute (or start with ~/)", rest)
		}
		return Source{Kind: SourceLocal, Raw: uri, Path: p}, nil

	case "github":
		// Parse USER/REPO, optionally followed by @REF or #BRANCH.
		repoRef := rest
		var ref string
		var isBranch bool

		// Check for # branch separator first (before @).
		if idx := strings.Index(repoRef, "#"); idx >= 0 {
			ref = repoRef[idx+1:]
			repoRef = repoRef[:idx]
			isBranch = true
		} else if idx := strings.Index(repoRef, "@"); idx >= 0 {
			ref = repoRef[idx+1:]
			repoRef = repoRef[:idx]
		}

		userRepo := strings.SplitN(repoRef, "/", 2)
		if len(userRepo) != 2 || userRepo[0] == "" || userRepo[1] == "" {
			return Source{}, fmt.Errorf("plugin: github: invalid repo format %q; expected USER/REPO", repoRef)
		}
		return Source{
			Kind:   SourceGitHub,
			Raw:    uri,
			User:   userRepo[0],
			Repo:   userRepo[1],
			Ref:    ref,
			Branch: isBranch,
		}, nil

	default:
		return Source{}, fmt.Errorf("plugin: unknown source scheme %q (supported: github, local)", scheme) //nolint:revive // user-facing message
	}
}

// CacheDirName returns a safe filesystem directory name for this source.
// Format: <scheme>-<user-or-hash>-<repo>-<ref>
func (s Source) CacheDirName() string {
	switch s.Kind {
	case SourceLocal:
		// Hash the path so it's filesystem-safe and deterministic.
		h := sha256.Sum256([]byte(s.Path))
		return fmt.Sprintf("local-%x", h[:8])
	case SourceGitHub:
		ref := s.Ref
		if ref == "" {
			ref = "default"
		}
		// Sanitise the ref for use as a directory name.
		ref = sanitizePathSegment(ref)
		name := fmt.Sprintf("github-%s-%s-%s",
			sanitizePathSegment(s.User),
			sanitizePathSegment(s.Repo),
			ref)
		// Truncate if too long (256-char filesystem limit safety).
		if len(name) > 200 {
			h := sha256.Sum256([]byte(name))
			name = fmt.Sprintf("github-%x", h[:16])
		}
		return name
	}
	return "unknown"
}

// CloneURL returns the HTTPS clone URL for a GitHub source.
func (s Source) CloneURL() string {
	if s.Kind != SourceGitHub {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s.git", s.User, s.Repo)
}

// sanitizePathSegment replaces characters that are unsafe in directory names.
func sanitizePathSegment(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('-')
		}
	}
	return strings.Trim(sb.String(), "-")
}

// PackageManager handles plugin source resolution and cache management.
type PackageManager struct {
	cacheDir string // root of the plugin cache
	git      gitexec.Runner
}

// NewPackageManager constructs a PackageManager using the given cacheDir.
// Typically $XDG_STATE_HOME/hygge/plugins.
func NewPackageManager(cacheDir string) *PackageManager {
	return NewPackageManagerWithGitRunner(cacheDir, nil)
}

// NewPackageManagerWithGitRunner constructs a PackageManager with an injected
// git runner. Passing nil uses the production non-interactive runner.
func NewPackageManagerWithGitRunner(cacheDir string, runner gitexec.Runner) *PackageManager {
	if runner == nil {
		runner = gitexec.DefaultRunner{}
	}
	return &PackageManager{cacheDir: cacheDir, git: runner}
}

// Resolve ensures the plugin source is available on disk and returns the
// directory containing the plugin files (plugin.toml + entry .lua).
//
// For local: sources, the path is returned directly (no copy).
// For github: sources, the repo is cloned into the cache on first call and
// the cache path is returned.
func (pm *PackageManager) Resolve(ctx context.Context, src Source) (dir string, err error) {
	switch src.Kind {
	case SourceLocal:
		info, err := os.Stat(src.Path)
		if err != nil {
			return "", fmt.Errorf("plugin: local source %q: %w", src.Path, err)
		}
		if !info.IsDir() {
			// Single-file plugin: dir is the containing directory.
			return filepath.Dir(src.Path), nil
		}
		return src.Path, nil

	case SourceGitHub:
		return pm.resolveGitHub(ctx, src)

	default:
		return "", fmt.Errorf("plugin: unknown source kind %q", src.Kind)
	}
}

// resolveGitHub ensures a GitHub repo is cloned and at the right ref.
func (pm *PackageManager) resolveGitHub(ctx context.Context, src Source) (string, error) {
	cacheDir := filepath.Join(pm.cacheDir, src.CacheDirName())
	if err := os.MkdirAll(pm.cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("plugin: ensure cache dir: %w", err)
	}

	// Check if already cloned.
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); err == nil {
		// Already cloned — verify the ref if specified.
		if src.Ref != "" {
			if err := pm.gitCheckout(ctx, cacheDir, src.Ref); err != nil {
				return "", fmt.Errorf("plugin: git checkout %q: %w", src.Ref, err)
			}
		}
		return cacheDir, nil
	}

	// Clone.
	slog.Info("plugin: cloning", "url", src.CloneURL(), "dir", cacheDir)
	if err := pm.gitClone(ctx, src.CloneURL(), cacheDir); err != nil {
		// Clean up partial clone.
		_ = os.RemoveAll(cacheDir)
		return "", fmt.Errorf("plugin: git clone %q: %w", src.CloneURL(), err)
	}

	// Checkout specific ref if requested.
	if src.Ref != "" {
		if err := pm.gitCheckout(ctx, cacheDir, src.Ref); err != nil {
			return "", fmt.Errorf("plugin: git checkout %q: %w", src.Ref, err)
		}
	}

	return cacheDir, nil
}

// Update pulls the latest changes for a plugin in the cache.  For local
// sources it's a no-op.
func (pm *PackageManager) Update(ctx context.Context, src Source) error {
	if src.Kind == SourceLocal {
		slog.Info("plugin: local source — no update needed", "path", src.Path)
		return nil
	}
	cacheDir := filepath.Join(pm.cacheDir, src.CacheDirName())
	if _, err := os.Stat(filepath.Join(cacheDir, ".git")); err != nil {
		// Not yet cloned — just resolve (clone).
		_, err := pm.resolveGitHub(ctx, src)
		return err
	}
	slog.Info("plugin: pulling", "dir", cacheDir)
	if err := pm.gitPull(ctx, cacheDir); err != nil {
		return fmt.Errorf("plugin: git pull: %w", err)
	}
	return nil
}

// Remove deletes the plugin's cache directory.  For local sources it's a no-op
// (we never copy local dirs into the cache).
func (pm *PackageManager) Remove(src Source) error {
	if src.Kind == SourceLocal {
		return nil
	}
	cacheDir := filepath.Join(pm.cacheDir, src.CacheDirName())
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("plugin: remove cache: %w", err)
	}
	return nil
}

// CacheDir returns the cache directory for the given source.
func (pm *PackageManager) CacheDir(src Source) string {
	if src.Kind == SourceLocal {
		return src.Path
	}
	return filepath.Join(pm.cacheDir, src.CacheDirName())
}

func (pm *PackageManager) gitClone(ctx context.Context, url, dir string) error {
	out, err := pm.git.Run(ctx, "", "clone", "--depth=1", url, dir)
	if err != nil {
		return fmt.Errorf("git clone failed: %w\n%s", err, string(out))
	}
	return nil
}

func (pm *PackageManager) gitCheckout(ctx context.Context, dir, ref string) error {
	out, err := pm.git.Run(ctx, "", "-C", dir, "checkout", ref)
	if err != nil {
		return fmt.Errorf("git checkout %q failed: %w\n%s", ref, err, string(out))
	}
	return nil
}

func (pm *PackageManager) gitPull(ctx context.Context, dir string) error {
	out, err := pm.git.Run(ctx, "", "-C", dir, "pull", "--ff-only")
	if err != nil {
		return fmt.Errorf("git pull failed: %w\n%s", err, string(out))
	}
	return nil
}
