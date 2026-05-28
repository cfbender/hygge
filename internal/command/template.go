package command

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// templateCommand is a TOML-declared or markdown-declared prompt template.
// Execute renders the template against the user's input and returns it as
// an [Outcome.Message] for the TUI to send as a normal user turn.
// Template commands never mutate app state.
type templateCommand struct {
	name        string
	description string
	source      string
	args        []ArgSpec
	prompt      string
	// mode is the optional agent/mode to apply when executing this command.
	// Sourced from the `mode` (or its `agent` alias) frontmatter field.
	mode string
	// model is the optional model string to apply when executing this command.
	// Sourced from the `model` frontmatter field.
	model string
}

func (t *templateCommand) Name() string        { return t.name }
func (t *templateCommand) Description() string { return t.description }
func (t *templateCommand) Source() string      { return t.source }
func (t *templateCommand) Mode() string        { return t.mode }
func (t *templateCommand) Model() string       { return t.model }
func (t *templateCommand) Args() []ArgSpec {
	out := make([]ArgSpec, len(t.args))
	copy(out, t.args)
	return out
}

// Execute renders the prompt template against input and returns it
// as the outcome's Message.  A required arg missing at runtime
// produces a Notice outcome (not an error) so the TUI surfaces the
// validation message without crashing the input loop.  When the
// command was declared with a `model` field, Execute also emits an
// [UpdateModel] update so the TUI switches models before sending
// the rendered message.
func (t *templateCommand) Execute(_ context.Context, _ App, input string) (Outcome, error) {
	values, tail, err := parseArgs(input, t.args)
	if err != nil {
		// Surface as a Notice so the user sees the message;
		// returning err would have the TUI render "command failed: …"
		// which is more noise than signal for arg errors.
		return Outcome{Notice: fmt.Sprintf("/%s: %s", t.name, err.Error())}, nil
	}
	rendered := renderTemplate(t.prompt, values, tail)
	out := Outcome{Message: rendered}
	if t.mode != "" || t.model != "" {
		out.Updates = map[string]string{}
		if t.mode != "" {
			out.Updates[UpdateMode] = t.mode
		}
		if t.model != "" {
			out.Updates[UpdateModel] = t.model
		}
	}
	return out, nil
}

// placeholderRe matches `{{name}}` references in a template.  Names
// follow argNameRe.  Whitespace immediately inside the braces is
// tolerated so users can write `{{ name }}` if they prefer.
var placeholderRe = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]*)\s*\}\}`)

// placeholdersIn returns every distinct placeholder name referenced
// by tmpl in declaration order.  Used by the loader to warn about
// unknown placeholders at load time.
func placeholdersIn(tmpl string) []string {
	matches := placeholderRe.FindAllStringSubmatch(tmpl, -1)
	out := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, m := range matches {
		name := m[1]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// renderTemplate substitutes `{{name}}` references in tmpl against
// the values map.  The reserved name `tail` is substituted with the
// supplied tail string.  Unknown references are left literal — the
// loader has already warned about them so this is a debugging aid,
// not a silent failure mode.
func renderTemplate(tmpl string, values map[string]string, tail string) string {
	return placeholderRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		sub := placeholderRe.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		name := sub[1]
		if name == reservedTailArg {
			return tail
		}
		if v, ok := values[name]; ok {
			return v
		}
		return match
	})
}

// parseArgs splits input into one value per declared arg, plus a
// trailing free-form tail.  Parsing rules:
//
//   - Whitespace-separated tokens.
//   - Double-quoted strings are a single token; backslash escapes
//     the next character inside quotes.
//   - The Nth declared arg consumes the Nth token; remaining text
//     (after the Nth token's terminating whitespace) becomes the
//     tail string verbatim, preserving inner whitespace.
//   - When the template has no declared args, the entire input
//     (after trimming the leading space the TUI inserts between the
//     command name and the body) becomes the tail.
//   - Required args missing from input produce a friendly error
//     ("missing required arg <name>").
//
// We deliberately don't implement shell-style escapes or single
// quotes — the spec calls out keeping arg parsing simple.
func parseArgs(input string, specs []ArgSpec) (map[string]string, string, error) {
	input = strings.TrimLeft(input, " \t")

	// No declared args: everything is tail.
	if len(specs) == 0 {
		return nil, input, nil
	}

	values := make(map[string]string, len(specs))
	remaining := input
	for i, spec := range specs {
		// For the LAST spec, the rest of input is the value.  This
		// is the documented "one positional arg captures everything"
		// pattern from the spec (e.g. /review captures the whole
		// code block).
		if i == len(specs)-1 {
			v := strings.TrimSpace(remaining)
			if v == "" {
				if spec.Required {
					return nil, "", fmt.Errorf("missing required arg %q", spec.Name)
				}
			} else {
				values[spec.Name] = v
			}
			remaining = ""
			break
		}
		tok, rest, err := nextToken(remaining)
		if err != nil {
			return nil, "", err
		}
		if tok == "" {
			if spec.Required {
				return nil, "", fmt.Errorf("missing required arg %q", spec.Name)
			}
			remaining = rest
			continue
		}
		values[spec.Name] = tok
		remaining = rest
	}
	tail := strings.TrimLeft(remaining, " \t")
	return values, tail, nil
}

// nextToken consumes one whitespace-separated token from s, honoring
// double-quoted strings.  Returns the token, the unconsumed
// remainder, and any parse error (unclosed quote).
func nextToken(s string) (string, string, error) {
	// Skip leading whitespace.
	i := 0
	for i < len(s) && isSpace(rune(s[i])) {
		i++
	}
	if i >= len(s) {
		return "", "", nil
	}
	if s[i] == '"' {
		// Quoted string.  Backslash escapes the next char.
		i++ // skip opening quote
		var b strings.Builder
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				b.WriteByte(s[i+1])
				i += 2
				continue
			}
			if c == '"' {
				return b.String(), s[i+1:], nil
			}
			b.WriteByte(c)
			i++
		}
		return "", "", fmt.Errorf("unclosed quoted string")
	}
	// Bare token: until next whitespace.
	start := i
	for i < len(s) && !isSpace(rune(s[i])) {
		i++
	}
	return s[start:i], s[i:], nil
}

// isSpace is a narrow whitespace predicate; the bubbletea input
// canonicalises tabs/spaces, but we tolerate any Unicode space rune
// so paste-from-elsewhere just works.
func isSpace(r rune) bool { return unicode.IsSpace(r) }
