package skill

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// nameRegex enforces the skill-name format: a lowercase identifier with
// optional digits and hyphens, 1–64 characters total.  No spaces, no
// slashes, no uppercase.
var nameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// reservedKeys are the frontmatter keys consumed by the parser
// directly.  Anything else lands in Skill.Extras.
var reservedKeys = map[string]struct{}{
	"name":        {},
	"description": {},
	"when_to_use": {},
}

// ParseFile reads path and parses it as a skill.  Returns a fully-
// populated Skill (Source is left zero — the loader fills it in) or an
// error.  ErrNoFrontmatter is returned when the file does not begin
// with the `---\n` delimiter; the loader treats that as a non-skill
// file and skips it.  Any other parse failure returns a *ParseError.
//
// Validation enforced here:
//   - The frontmatter must close with a `---` line.
//   - `name`, `description`, and `when_to_use` must all be present and
//     non-empty.
//   - `name` must match nameRegex.
//   - The filename stem must equal the frontmatter `name`.
func ParseFile(path string) (Skill, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from a controlled walk over a known directory
	if err != nil {
		return Skill{}, &ParseError{Path: path, Reason: fmt.Sprintf("read: %v", err)}
	}
	sk, err := parse(data, path)
	if err != nil {
		return Skill{}, err
	}
	sk.Path = path
	sk.LoadedAt = time.Now()
	return sk, nil
}

// parse is the in-memory parser used by ParseFile and the tests.  It
// does not touch the filesystem aside from deriving the stem from path
// for the filename / frontmatter-name consistency check.
func parse(data []byte, path string) (Skill, error) {
	// Detect frontmatter: the file must begin with `---` followed by a
	// newline.  We tolerate a leading BOM but nothing else.
	body := data
	body = bytes.TrimPrefix(body, []byte("\ufeff"))

	if !hasFrontmatterStart(body) {
		return Skill{}, ErrNoFrontmatter
	}

	// Strip the opening `---\n` (or `---\r\n`).
	idx := bytes.IndexByte(body, '\n')
	if idx < 0 {
		return Skill{}, ErrNoFrontmatter
	}
	body = body[idx+1:]

	// Find the closing `---` line.
	closeIdx := findClosingFence(body)
	if closeIdx < 0 {
		return Skill{}, &ParseError{Path: path, Reason: "missing closing `---` for frontmatter"}
	}

	headerBytes := body[:closeIdx]
	// Advance past the closing fence line.
	rest := body[closeIdx:]
	// Skip the `---` plus the following newline (if any).
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		rest = nil
	} else {
		rest = rest[nl+1:]
	}

	meta, err := parseHeader(headerBytes, path)
	if err != nil {
		return Skill{}, err
	}

	name := meta["name"]
	description := meta["description"]
	whenToUse := meta["when_to_use"]

	if name == "" {
		return Skill{}, &ParseError{Path: path, Reason: "frontmatter is missing required `name`"}
	}
	if !nameRegex.MatchString(name) {
		return Skill{}, &ParseError{Path: path, Reason: fmt.Sprintf("invalid `name` %q (must match %s)", name, nameRegex.String())}
	}
	if description == "" {
		return Skill{}, &ParseError{Path: path, Reason: "frontmatter is missing required `description`"}
	}
	if whenToUse == "" {
		return Skill{}, &ParseError{Path: path, Reason: "frontmatter is missing required `when_to_use`"}
	}

	// Filename stem must equal name.  Empty path skips the check (used
	// by some tests that exercise parse directly).
	if path != "" {
		stem := filenameStem(path)
		if stem != name {
			return Skill{}, &ParseError{
				Path:   path,
				Reason: fmt.Sprintf("filename stem %q does not match frontmatter name %q", stem, name),
			}
		}
	}

	// Anything not in reservedKeys becomes an Extra.
	var extras map[string]string
	for k, v := range meta {
		if _, reserved := reservedKeys[k]; reserved {
			continue
		}
		if extras == nil {
			extras = make(map[string]string)
		}
		extras[k] = v
	}

	sk := Skill{
		Name:        name,
		Description: description,
		WhenToUse:   whenToUse,
		Body:        strings.TrimSpace(string(rest)),
		Extras:      extras,
	}
	return sk, nil
}

// hasFrontmatterStart reports whether data begins with a `---\n` (or
// `---\r\n`) delimiter.  A file that starts with `---` but no newline
// (or `--- ` followed by more text on the same line) is treated as not
// having frontmatter.
func hasFrontmatterStart(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	if !bytes.HasPrefix(data, []byte("---")) {
		return false
	}
	// The fourth byte must be a line terminator.  Spaces between
	// `---` and the newline are rejected — keeps the rule simple.
	switch data[3] {
	case '\n':
		return true
	case '\r':
		return len(data) >= 5 && data[4] == '\n'
	default:
		return false
	}
}

// findClosingFence returns the byte index of the closing `---` line in
// the frontmatter body, or -1 if none was found.  The fence must be a
// line containing only the three dashes (trailing CR allowed).
func findClosingFence(body []byte) int {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	offset := 0
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimRight(line, "\r")
		if trimmed == "---" {
			return offset
		}
		// +1 for the consumed newline; bufio.Scanner strips it.
		offset += len(line) + 1
	}
	return -1
}

// parseHeader walks the frontmatter body line by line and returns a
// map of key → value.  Lines that don't look like `key: value` are
// reported as ParseError.  Blank lines are tolerated.
func parseHeader(header []byte, path string) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(header))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// `# comment` lines are ignored — convenience for humans.
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 1 {
			return nil, &ParseError{
				Path:   path,
				Reason: fmt.Sprintf("line %d: expected `key: value`, got %q", lineNum, line),
			}
		}
		key := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		// Strip matching surrounding quotes on the value if present.
		value = stripQuotes(value)
		if key == "" {
			return nil, &ParseError{
				Path:   path,
				Reason: fmt.Sprintf("line %d: empty key", lineNum),
			}
		}
		out[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, &ParseError{Path: path, Reason: fmt.Sprintf("scan header: %v", err)}
	}
	return out, nil
}

// stripQuotes removes a single matching pair of surrounding single or
// double quotes.  Mismatched or unquoted values are returned unchanged.
func stripQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// filenameStem returns the filename without its extension.  Behaves
// like filepath.Base + strings.TrimSuffix(ext) but stays in one place.
func filenameStem(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}
