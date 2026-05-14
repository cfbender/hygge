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

// ParseFile reads path and parses it as a flat-layout skill.  The
// filename stem must equal the frontmatter `name`.  For directory-
// style skills (`<name>/SKILL.md`) use ParseSkillDir instead.
//
// Returns a fully-populated Skill (Source and Dir are left zero — the
// loader fills them in) or an error.  ErrNoFrontmatter is returned
// when the file does not begin with the `---\n` delimiter; the loader
// treats that as a non-skill file and skips it.  Any other parse
// failure returns a *ParseError.
//
// Validation enforced here:
//   - The frontmatter must close with a `---` line.
//   - `name` and `description` must be present and non-empty.
//   - `when_to_use` is optional (`.agents`-standard skills fold this
//     into the description).
//   - `name` must match nameRegex.
//   - The filename stem must equal the frontmatter `name`.
func ParseFile(path string) (Skill, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from a controlled walk over a known directory
	if err != nil {
		return Skill{}, &ParseError{Path: path, Reason: fmt.Sprintf("read: %v", err)}
	}
	sk, err := parse(data, path, parseModeFile)
	if err != nil {
		return Skill{}, err
	}
	sk.Path = path
	sk.LoadedAt = time.Now()
	return sk, nil
}

// ParseSkillDir reads `<dir>/SKILL.md` and parses it as a directory-
// style skill.  The directory's base name must equal the frontmatter
// `name`.  Returns a fully-populated Skill with Path set to the
// SKILL.md location and Dir set to the directory itself.  Source is
// left zero — the loader fills it in.
//
// Validation is the same as ParseFile except the filename-stem check
// is replaced with a directory-name check.
func ParseSkillDir(dir string) (Skill, error) {
	skillPath := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(skillPath) //nolint:gosec // path comes from a controlled walk over a known directory
	if err != nil {
		return Skill{}, &ParseError{Path: skillPath, Reason: fmt.Sprintf("read: %v", err)}
	}
	sk, err := parse(data, skillPath, parseModeSkillDir)
	if err != nil {
		return Skill{}, err
	}
	sk.Path = skillPath
	sk.Dir = dir
	sk.LoadedAt = time.Now()
	return sk, nil
}

// parseMode selects which filename ↔ name consistency check parse
// applies.  parseModeNone disables the check (used by some unit tests
// that exercise parse directly with an empty path).
type parseMode int

const (
	parseModeNone     parseMode = iota // no path-based name check
	parseModeFile                      // stem(path) must equal name
	parseModeSkillDir                  // base(dir(path)) must equal name
)

// parse is the in-memory parser used by ParseFile, ParseSkillDir, and
// the tests.  It does not touch the filesystem aside from deriving the
// stem or directory name from path for the name-consistency check.
func parse(data []byte, path string, mode parseMode) (Skill, error) {
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

	// Name-consistency check is mode-dependent.  Empty path always
	// skips the check (used by parse-directly unit tests).
	if path != "" {
		switch mode {
		case parseModeFile:
			stem := filenameStem(path)
			if stem != name {
				return Skill{}, &ParseError{
					Path:   path,
					Reason: fmt.Sprintf("filename stem %q does not match frontmatter name %q", stem, name),
				}
			}
		case parseModeSkillDir:
			dirName := filepath.Base(filepath.Dir(path))
			if dirName != name {
				return Skill{}, &ParseError{
					Path:   path,
					Reason: fmt.Sprintf("directory name %q does not match frontmatter name %q", dirName, name),
				}
			}
		case parseModeNone:
			// no-op
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
// map of key → value.  Supports two value shapes:
//
//   - inline:  `key: value` (single line; quotes stripped)
//   - block:   `key: >` or `key: |` on one line, followed by
//     indented continuation lines.  `>` folds newlines to
//     spaces; `|` preserves newlines literally.  Both strip
//     trailing whitespace.  Continuation ends at the first
//     line that is not indented (>=1 space) and not blank.
//
// Lines that don't look like `key: value` or aren't part of a block
// scalar are reported as ParseError.  Blank lines outside block
// scalars are tolerated.  `# comment` lines are ignored.
func parseHeader(header []byte, path string) (map[string]string, error) {
	out := make(map[string]string)

	// Buffer all lines first so we can implement block-scalar
	// lookahead without re-scanning.
	scanner := bufio.NewScanner(bytes.NewReader(header))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, strings.TrimRight(scanner.Text(), "\r"))
	}
	if err := scanner.Err(); err != nil {
		return nil, &ParseError{Path: path, Reason: fmt.Sprintf("scan header: %v", err)}
	}

	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		// `# comment` lines are ignored.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.IndexByte(raw, ':')
		if colon < 1 {
			return nil, &ParseError{
				Path:   path,
				Reason: fmt.Sprintf("line %d: expected `key: value`, got %q", i+1, raw),
			}
		}
		key := strings.TrimSpace(raw[:colon])
		valuePart := strings.TrimSpace(raw[colon+1:])
		if key == "" {
			return nil, &ParseError{
				Path:   path,
				Reason: fmt.Sprintf("line %d: empty key", i+1),
			}
		}

		// Block scalar markers: `>` (folded) or `|` (literal),
		// optionally followed by a chomp indicator (`-` strip, `+`
		// keep — both treated as strip-trailing here).
		if valuePart == ">" || valuePart == "|" ||
			valuePart == ">-" || valuePart == "|-" ||
			valuePart == ">+" || valuePart == "|+" {
			marker := valuePart[0]
			value, consumed := readBlockScalar(lines[i+1:], marker)
			out[key] = value
			i += consumed
			continue
		}

		// Implicit block: `key:` with empty value, followed by an
		// indented continuation.  Treated as a literal block so
		// list-style continuations (`  - item`) and free-form
		// continuations both round-trip cleanly into Extras.  This
		// matches what `.agents` skills do for fields like
		// `allowed-tools` and avoids erroring out on perfectly
		// valid YAML that just happens to use implicit block style.
		if valuePart == "" && i+1 < len(lines) {
			next := lines[i+1]
			if next != "" && (next[0] == ' ' || next[0] == '\t') {
				value, consumed := readBlockScalar(lines[i+1:], '|')
				out[key] = value
				i += consumed
				continue
			}
		}

		out[key] = stripQuotes(valuePart)
	}
	return out, nil
}

// readBlockScalar reads continuation lines for a YAML-style block
// scalar.  Continuation lines are any non-blank lines that start with
// whitespace; the leading indent (smallest non-zero) is stripped from
// each so the value is dedented.  Blank lines inside a block scalar
// are preserved (rendered as newlines for `|`, swallowed for `>`).
//
// marker is the introducing character: `>` for folded (newlines →
// spaces) or `|` for literal (newlines preserved).
//
// Returns the assembled value and the number of input lines consumed.
func readBlockScalar(rest []string, marker byte) (string, int) {
	var contLines []string
	consumed := 0
	for _, line := range rest {
		if line == "" {
			// Blank line.  We don't know yet whether it's
			// internal to the block or terminating; include it
			// provisionally and trim trailing blanks at the end.
			contLines = append(contLines, "")
			consumed++
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			// First non-indented, non-blank line ends the block.
			break
		}
		contLines = append(contLines, line)
		consumed++
	}
	// Strip trailing blank lines.
	for len(contLines) > 0 && strings.TrimSpace(contLines[len(contLines)-1]) == "" {
		contLines = contLines[:len(contLines)-1]
	}
	if len(contLines) == 0 {
		return "", consumed
	}
	// Find the smallest leading indent across non-blank lines.
	minIndent := -1
	for _, l := range contLines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := 0
		for n < len(l) && (l[n] == ' ' || l[n] == '\t') {
			n++
		}
		if minIndent == -1 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent < 0 {
		minIndent = 0
	}
	for i, l := range contLines {
		if len(l) >= minIndent {
			contLines[i] = l[minIndent:]
		}
	}
	if marker == '>' {
		// Folded: join non-blank runs with single spaces; blank
		// lines remain as paragraph breaks (single newline).
		var b strings.Builder
		prevBlank := false
		for i, l := range contLines {
			if strings.TrimSpace(l) == "" {
				if !prevBlank {
					b.WriteByte('\n')
				}
				prevBlank = true
				continue
			}
			if i > 0 && !prevBlank {
				b.WriteByte(' ')
			}
			b.WriteString(l)
			prevBlank = false
		}
		return strings.TrimSpace(b.String()), consumed
	}
	// Literal: preserve newlines verbatim.
	return strings.TrimRight(strings.Join(contLines, "\n"), "\n"), consumed
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
