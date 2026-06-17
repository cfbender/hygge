package ui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/cfbender/hygge/internal/ui/components"
)

const maxMentionFileCandidates = 2000

var promptMentionPattern = regexp.MustCompile(`(^|[\s(])@([^\s]+)`)

// MentionSubagent is the UI-facing description of a selectable sub-agent type.
// The CLI maps internal/subagent.Type values into this shape so internal/ui can
// render @ mention completions without depending on the subagent package.
type MentionSubagent struct {
	Name        string
	Description string
}

type mentionCandidate struct {
	components.MentionItem
	Insert string
}

func (a *App) activeMentionQuery() (query string, start int, ok bool) {
	text := a.input.Value()
	if text == "" || strings.HasPrefix(strings.TrimSpace(text), "/") {
		return "", 0, false
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return "", 0, false
	}
	for i := len(runes) - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			break
		}
		if runes[i] == '@' {
			return string(runes[i+1:]), i, true
		}
	}
	return "", 0, false
}

func (a *App) mentionMatches() []mentionCandidate {
	query, _, ok := a.activeMentionQuery()
	if !ok {
		return nil
	}
	q := strings.ToLower(query)
	var out []mentionCandidate

	for _, sa := range a.opts.Subagents {
		name := strings.TrimSpace(sa.Name)
		if name == "" {
			continue
		}
		label := "agent:" + name
		haystack := strings.ToLower(label + " " + sa.Description)
		if q == "" || strings.Contains(haystack, q) {
			out = append(out, mentionCandidate{
				MentionItem: components.MentionItem{Kind: "subagent", Label: label, Description: sa.Description},
				Insert:      "@" + label + " ",
			})
		}
	}

	out = append(out, rankedFileMentions(a.mentionFiles(), q, maxMentionMatches-len(out))...)

	return out
}

const maxMentionMatches = 50

// rankedFileMentions returns up to limit file candidates for q, ordered by
// relevance so the most likely matches survive the display cap. Without ranking,
// a substring filter over an alphabetically sorted list silently hides every
// match past the first limit entries, which makes whole areas of the project
// look "missing" from @ completions.
func rankedFileMentions(paths []string, q string, limit int) []mentionCandidate {
	if limit <= 0 {
		return nil
	}
	type scored struct {
		path  string
		score int
	}
	var matches []scored
	for _, path := range paths {
		score := mentionMatchScore(path, q)
		if score < 0 {
			continue
		}
		matches = append(matches, scored{path: path, score: score})
	}
	// Higher score first; ties keep the existing alphabetical order (stable).
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]mentionCandidate, 0, len(matches))
	for _, m := range matches {
		out = append(out, mentionCandidate{
			MentionItem: components.MentionItem{Kind: "file", Label: m.path},
			Insert:      "@" + m.path + " ",
		})
	}
	return out
}

// mentionMatchScore ranks how well a project-relative path matches the lowercase
// query q. It returns -1 when the path does not match. Higher is better:
// basename matches rank above path-only matches, and prefix matches above
// interior ones, so the file a user is most likely reaching for surfaces first.
func mentionMatchScore(path, q string) int {
	if q == "" {
		return 0
	}
	lowerPath := strings.ToLower(path)
	base := lowerPath
	if i := strings.LastIndex(lowerPath, "/"); i >= 0 {
		base = lowerPath[i+1:]
	}
	switch {
	case base == q:
		return 100
	case strings.HasPrefix(base, q):
		return 80
	case strings.Contains(base, q):
		return 60
	case strings.HasPrefix(lowerPath, q):
		return 40
	case strings.Contains(lowerPath, q):
		return 20
	default:
		return -1
	}
}

func (a *App) mentionFiles() []string {
	root := a.opts.ProjectDir
	if root == "" || strings.HasPrefix(root, "~") {
		return nil
	}
	if a.mentionFileRoot == root && a.mentionFileCache != nil {
		return a.mentionFileCache
	}

	var paths []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if shouldSkipMentionDir(name) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		if len(paths) >= maxMentionFileCandidates {
			return fs.SkipAll
		}
		return nil
	})
	sort.Strings(paths)
	a.mentionFileRoot = root
	a.mentionFileCache = paths
	return paths
}

func shouldSkipMentionDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "__pycache__", "dist", "target", "bin":
		return true
	default:
		return false
	}
}

func (a *App) clampedMentionHighlight(matches []mentionCandidate) int {
	if len(matches) == 0 {
		return -1
	}
	hi := a.mentionHighlight
	if hi < 0 {
		return 0
	}
	if hi >= len(matches) {
		return len(matches) - 1
	}
	return hi
}

func (a *App) moveMentionHighlight(delta int) bool {
	if a.mentionDismissed {
		return false
	}
	matches := a.mentionMatches()
	if len(matches) == 0 {
		a.mentionHighlight = -1
		return false
	}
	hi := max(max(a.mentionHighlight, 0)+delta, 0)
	if hi >= len(matches) {
		hi = len(matches) - 1
	}
	a.mentionHighlight = hi
	return true
}

func (a *App) acceptMentionCompletion() bool {
	if a.mentionDismissed {
		return false
	}
	_, start, ok := a.activeMentionQuery()
	if !ok {
		return false
	}
	matches := a.mentionMatches()
	hi := a.clampedMentionHighlight(matches)
	if hi < 0 {
		return false
	}
	textRunes := []rune(a.input.Value())
	newValue := string(textRunes[:start]) + matches[hi].Insert
	a.input.Textarea.SetValue(newValue)
	a.input.Textarea.CursorEnd()
	a.history.Reset()
	a.mentionHighlight = -1
	a.mentionDismissed = false
	return true
}

func mentionItems(candidates []mentionCandidate) []components.MentionItem {
	items := make([]components.MentionItem, 0, len(candidates))
	for _, c := range candidates {
		items = append(items, c.MentionItem)
	}
	return items
}

func (a *App) promptAttachmentsForMentions(text string) ([]promptAttachment, error) {
	root := a.opts.ProjectDir
	if root == "" || strings.HasPrefix(root, "~") {
		return nil, nil
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve project dir: %w", err)
	}
	seen := map[string]struct{}{}
	var attachments []promptAttachment
	for _, token := range mentionFileTokens(text) {
		if strings.HasPrefix(token, "agent:") {
			continue
		}
		path := strings.TrimRight(token, ".,;:!?)")
		if path == "" {
			continue
		}
		candidate := filepath.FromSlash(path)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(rootAbs, candidate)
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		att, err := loadMentionPromptAttachment(abs)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, att)
		seen[abs] = struct{}{}
	}
	return attachments, nil
}

func mentionFileTokens(text string) []string {
	matches := promptMentionPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) >= 3 {
			out = append(out, m[2])
		}
	}
	return out
}
