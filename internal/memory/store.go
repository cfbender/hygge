// Package memory provides file-backed project and global memory storage.
package memory

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/cfbender/hygge/internal/gitexec"
	"github.com/cfbender/hygge/internal/session"
)

const sourceExplicitUserRequest = "explicit_user_request"

// ErrSecret is returned when memory content looks like it contains a secret.
var ErrSecret = errors.New("memory: content appears to contain a secret")

// FileStoreOptions configures file-backed project/global memory storage.
type FileStoreOptions struct {
	ProjectDir    string
	HomeDir       string
	XDGConfigHome string
	Now           func() time.Time
	Git           gitexec.Runner
}

// FileStore persists project and global memories as Markdown files.
type FileStore struct {
	projectDir    string
	homeDir       string
	xdgConfigHome string
	now           func() time.Time
	git           gitexec.Runner
}

// NewFileStore constructs a file-backed memory store.
func NewFileStore(opts FileStoreOptions) *FileStore {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	git := opts.Git
	if git == nil {
		git = gitexec.DefaultRunner{}
	}
	return &FileStore{projectDir: opts.ProjectDir, homeDir: opts.HomeDir, xdgConfigHome: opts.XDGConfigHome, now: now, git: git}
}

// ListMemories returns global memories, then project memories, for prompt injection.
func (s *FileStore) ListMemories(ctx context.Context) ([]*session.Memory, error) {
	global, err := s.List(ctx, session.MemoryScopeGlobal)
	if err != nil {
		return nil, err
	}
	project, err := s.List(ctx, session.MemoryScopeProject)
	if err != nil {
		return nil, err
	}
	return append(global, project...), nil
}

// Remember writes content as a project or global Markdown memory.
func (s *FileStore) Remember(ctx context.Context, scope session.MemoryScope, content string) (*session.Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("memory: content required")
	}
	if LooksLikeSecret(content) {
		return nil, ErrSecret
	}
	dir, err := s.dir(scope)
	if err != nil {
		return nil, err
	}
	title := titleFromContent(content)
	slug := slugify(title)
	if slug == "" {
		slug = "memory"
	}
	path, err := nextMemoryPath(dir, slug)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	m := &session.Memory{ID: session.NewMemoryID(), Scope: scope, Title: title, Body: content, Content: content, Source: sourceExplicitUserRequest, Path: path, CreatedAt: now, UpdatedAt: now}
	if err := writeMemoryFile(path, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Forget removes an active project or global memory file by id.
func (s *FileStore) Forget(ctx context.Context, scope session.MemoryScope, memoryID string) (*session.Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(memoryID) == "" {
		return nil, fmt.Errorf("memory: memory_id required")
	}
	memories, err := s.List(ctx, scope)
	if err != nil {
		return nil, err
	}
	for _, m := range memories {
		if m.ID != memoryID {
			continue
		}
		if err := os.Remove(m.Path); err != nil {
			return nil, fmt.Errorf("memory: forget %s memory %q: %w", scope, memoryID, err)
		}
		now := s.now().UTC()
		m.UpdatedAt = now
		m.DeletedAt = now
		return m, nil
	}
	return nil, fmt.Errorf("memory: forget %s memory %q: %w", scope, memoryID, session.ErrMemoryNotFound)
}

// ProjectMemoryGitignoreWarning returns a warning before the first project
// memory write when .hygge/ is not ignored by git. It never edits .gitignore.
func (s *FileStore) ProjectMemoryGitignoreWarning(ctx context.Context) (string, error) {
	dir, err := s.dir(session.MemoryScopeProject)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".md" {
				return "", nil
			}
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("memory: inspect project memory dir: %w", err)
	}
	ignored, err := s.projectHyggeIgnored(ctx)
	if err != nil || ignored {
		return "", err
	}
	return ".hygge/ is not ignored; add .hygge/ to .gitignore to keep project memories local.", nil
}

// List returns active file-backed memories for a single scope.
func (s *FileStore) List(ctx context.Context, scope session.MemoryScope) ([]*session.Memory, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := s.dir(scope)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: list %s memories: %w", scope, err)
	}
	var out []*session.Memory
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		m, err := ParseFile(path)
		if err != nil {
			continue
		}
		if m.Scope != scope || !m.DeletedAt.IsZero() {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// MemoryDir returns the directory that backs a project or global memory scope.
func (s *FileStore) MemoryDir(scope session.MemoryScope) (string, error) {
	return s.dir(scope)
}

func (s *FileStore) projectHyggeIgnored(ctx context.Context) (bool, error) {
	if strings.TrimSpace(s.projectDir) == "" {
		return false, fmt.Errorf("memory: project directory required")
	}
	if _, err := s.git.Run(ctx, s.projectDir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return true, nil
	}
	_, err := s.git.Run(ctx, s.projectDir, "check-ignore", "-q", ".hygge/")
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("memory: check .hygge gitignore: %w", err)
}

func (s *FileStore) dir(scope session.MemoryScope) (string, error) {
	switch scope {
	case session.MemoryScopeProject:
		if strings.TrimSpace(s.projectDir) == "" {
			return "", fmt.Errorf("memory: project directory required")
		}
		return filepath.Join(s.projectDir, ".hygge", "memory"), nil
	case session.MemoryScopeGlobal:
		base := s.xdgConfigHome
		if base == "" {
			home := s.homeDir
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return "", fmt.Errorf("memory: resolve home dir: %w", err)
				}
			}
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "hygge", "memory"), nil
	default:
		return "", fmt.Errorf("memory: file-backed scope must be project or global, got %q", scope)
	}
}

func writeMemoryFile(path string, m *session.Memory) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("memory: create memory dir: %w", err)
	}
	data := []byte(formatMemoryFile(m))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("memory: write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: commit file: %w", err)
	}
	return nil
}

func formatMemoryFile(m *session.Memory) string {
	body := strings.TrimSpace(m.Body)
	if body == "" {
		body = strings.TrimSpace(m.Content)
	}
	return fmt.Sprintf("---\nid: %s\nscope: %s\ncreated_at: %s\nupdated_at: %s\nsource: %s\n---\n\n# %s\n\n%s\n", m.ID, m.Scope, m.CreatedAt.UTC().Format(time.RFC3339), m.UpdatedAt.UTC().Format(time.RFC3339), m.Source, m.Title, body)
}

// ParseFile reads a Markdown memory file with frontmatter.
func ParseFile(path string) (*session.Memory, error) {
	data, err := os.ReadFile(path) //nolint:gosec // caller controls memory directory
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("memory: missing frontmatter")
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return nil, fmt.Errorf("memory: missing frontmatter close")
	}
	header := text[4 : 4+end]
	body := strings.TrimSpace(text[4+end+len("\n---"):])
	meta := parseFrontmatter(header)
	m := &session.Memory{ID: meta["id"], Scope: session.MemoryScope(meta["scope"]), Source: meta["source"], Path: path}
	if created, err := time.Parse(time.RFC3339, meta["created_at"]); err == nil {
		m.CreatedAt = created.UTC()
	}
	if updated, err := time.Parse(time.RFC3339, meta["updated_at"]); err == nil {
		m.UpdatedAt = updated.UTC()
	}
	m.Title, m.Body = splitMarkdownTitle(body)
	m.Content = m.Body
	if m.Body == "" {
		m.Body = strings.TrimSpace(body)
		m.Content = m.Body
	}
	return m, nil
}

func parseFrontmatter(header string) map[string]string {
	out := map[string]string{}
	for line := range strings.SplitSeq(header, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return out
}

func splitMarkdownTitle(body string) (string, string) {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "# ") {
		line, rest, _ := strings.Cut(body, "\n")
		return strings.TrimSpace(strings.TrimPrefix(line, "# ")), strings.TrimSpace(rest)
	}
	return titleFromContent(body), body
}

func titleFromContent(content string) string {
	first := strings.TrimSpace(strings.Split(strings.TrimSpace(content), "\n")[0])
	first = strings.TrimPrefix(first, "#")
	first = strings.TrimSpace(first)
	if len(first) > 72 {
		first = first[:72]
	}
	if first == "" {
		return "Memory"
	}
	return first
}

func nextMemoryPath(dir, slug string) (string, error) {
	for i := range 1000 {
		name := slug
		if i > 0 {
			name = fmt.Sprintf("%s-%d", slug, i+1)
		}
		path := filepath.Join(dir, name+".md")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("memory: check collision: %w", err)
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(slug))
	return filepath.Join(dir, fmt.Sprintf("%s-%08x.md", slug, h.Sum32())), nil
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\b(password|api[_-]?key|secret|token)\s*=`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
}

// LooksLikeSecret reports whether content matches common secret patterns.
func LooksLikeSecret(content string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	return false
}
