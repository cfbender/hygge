package agent

// touched.go: path extraction for the lazy per-tool-call AGENTS.md /
// CLAUDE.md loader.  Each provider tool_use block names a tool and
// supplies a JSON input blob; the lazy tracker needs the path-like
// fields out of that blob so it can walk up toward the project root.

import (
	"encoding/json"
)

// touchedPaths returns the path-like arguments a tool_use call
// referenced.  Only the built-in hygge tools are recognised; unknown
// tools (skill, mcp.*, future tools) return nil.
//
// For ambiguous tools (grep / glob default the search root to ".") the
// helper returns "." so the tracker walks the agent's working
// directory.  Bash is currently unsupported — it has no explicit cwd
// argument; callers receive nil until the bash tool grows one.
//
// TODO(v0.3): wire in bash-touched directories once the bash tool
// exposes a `cwd` argument.  Today bash always runs in ec.Pwd, which
// the lazy tracker is going to walk anyway when other tools touch
// files; the cost of skipping bash here is missing context for
// commands that cd into a subdir before doing work.
func touchedPaths(toolName string, input json.RawMessage) []string {
	switch toolName {
	case "read", "write", "edit":
		return extractPath(input)
	case "grep", "glob":
		if p := extractPath(input); len(p) > 0 {
			return p
		}
		// Default search root for grep/glob is the working
		// directory.  Returning "." lets the tracker resolve
		// against pwd.
		return []string{"."}
	case "bash":
		// TODO(v0.3): see package doc.
		return nil
	default:
		// skill, mcp.*, anything else — no path-like args we can
		// safely extract.
		return nil
	}
}

// extractPath pulls a top-level "path" string out of a tool input
// blob.  Returns nil when the field is missing, empty, or not a
// string.
func extractPath(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil
	}
	if args.Path == "" {
		return nil
	}
	return []string{args.Path}
}
