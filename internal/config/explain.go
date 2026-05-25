package config

import (
	"fmt"
	"sort"
	"strings"
)

var explainSectionOrder = []string{
	"model",
	"permission",
	"theme",
	"ui",
	"compaction",
	"session",
	"catalog",
	"notifications",
	"modes",
	"subagents",
	"mcp",
	"mcps",
	"plugins",
}

var deprecatedExplainKeys = map[string]bool{
	"model.provider": true,
	"model.name":     true,
}

// ExplainAll returns a multi-line TOML-like representation of the fully merged
// effective config. It renders the merged raw config map so dynamic sections
// such as [subagents.<name>], [plugins.<name>], and MCP tables are included.
// Scalar leaves include inline comments showing the winning source, or a
// concise override chain when multiple sources contributed.
//
// Deprecated top-level model.provider and model.name compatibility keys are
// intentionally omitted from the merged view; [[modes]] is the canonical model
// selection shape.
func ExplainAll(prov Provenance, cfg *Config) string {
	if cfg == nil {
		return ""
	}
	r := explainRenderer{prov: prov}
	r.renderRoot(cfg.raw)
	return r.String()
}

type explainRenderer struct {
	b    strings.Builder
	prov Provenance
}

func (r *explainRenderer) String() string { return r.b.String() }

func (r *explainRenderer) renderRoot(raw map[string]any) {
	if raw == nil {
		return
	}
	for _, key := range orderedKeys(raw, explainSectionOrder) {
		value := raw[key]
		path := key
		if deprecatedExplainKeys[path] || isEmptyExplainValue(path, value) {
			continue
		}
		switch val := value.(type) {
		case map[string]any:
			cleaned := filterExplainMap(path, val)
			if len(cleaned) == 0 {
				continue
			}
			r.renderMapSection(path, key, cleaned)
		case []any:
			if len(val) == 0 {
				continue
			}
			r.renderArraySection(path, key, val)
		default:
			r.ensureBlank()
			r.writeKeyLine(path, key, val)
		}
	}
}

func (r *explainRenderer) renderMapSection(path, header string, values map[string]any) {
	r.ensureBlank()
	fmt.Fprintf(&r.b, "[%s]\n", header)
	r.renderMapBody(path, values)
}

func (r *explainRenderer) renderMapBody(path string, values map[string]any) {
	keys := orderedKeys(values, nil)
	for _, key := range keys {
		value := values[key]
		childPath := path + "." + key
		if deprecatedExplainKeys[childPath] || isEmptyExplainValue(childPath, value) {
			continue
		}
		if items, ok := value.([]any); ok && !allScalarValues(items) {
			continue
		}
		switch value.(type) {
		case map[string]any:
			continue
		default:
			r.writeKeyLine(childPath, key, value)
		}
	}

	for _, key := range keys {
		value := values[key]
		childPath := path + "." + key
		if deprecatedExplainKeys[childPath] || isEmptyExplainValue(childPath, value) {
			continue
		}
		switch val := value.(type) {
		case map[string]any:
			cleaned := filterExplainMap(childPath, val)
			if len(cleaned) == 0 {
				continue
			}
			r.b.WriteString("\n")
			fmt.Fprintf(&r.b, "[%s]\n", childPath)
			r.renderMapBody(childPath, cleaned)
		case []any:
			if len(val) == 0 || allScalarValues(val) {
				continue
			}
			r.b.WriteString("\n")
			r.renderArraySection(childPath, childPath, val)
		}
	}
}

func (r *explainRenderer) renderArraySection(path, header string, values []any) {
	for i, item := range values {
		if i > 0 || r.b.Len() > 0 {
			r.b.WriteString("\n")
		}
		switch val := item.(type) {
		case map[string]any:
			stablePath := arrayElementPath(path, val, i)
			fmt.Fprintf(&r.b, "[[%s]]\n", header)
			r.renderMapBody(stablePath, val)
		default:
			r.writeKeyLine(path, header, values)
			return
		}
	}
}

func (r *explainRenderer) writeKeyLine(path, key string, value any) {
	line := renderKeyLine(key, value, r.prov[path])
	r.b.WriteString(line)
	r.b.WriteString("\n")
}

func (r *explainRenderer) ensureBlank() {
	if r.b.Len() > 0 {
		r.b.WriteString("\n")
	}
}

func filterExplainMap(path string, values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		childPath := path + "." + key
		if deprecatedExplainKeys[childPath] || isEmptyExplainValue(childPath, value) {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			filtered := filterExplainMap(childPath, nested)
			if len(filtered) == 0 {
				continue
			}
			out[key] = filtered
			continue
		}
		out[key] = value
	}
	return out
}

func isEmptyExplainValue(path string, value any) bool {
	if value == nil {
		return true
	}
	if path == "model" {
		return false
	}
	switch val := value.(type) {
	case map[string]any:
		return len(filterExplainMap(path, val)) == 0
	case []any:
		return len(val) == 0
	default:
		return false
	}
}

func orderedKeys(values map[string]any, preferred []string) []string {
	seen := make(map[string]bool, len(values))
	keys := make([]string, 0, len(values))
	for _, key := range preferred {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0, len(values)-len(keys))
	for key := range values {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func allScalarValues(values []any) bool {
	for _, value := range values {
		switch value.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

func arrayElementPath(path string, item map[string]any, index int) string {
	if name, ok := item["name"].(string); ok && name != "" {
		return path + "." + name
	}
	if id, ok := item["id"].(string); ok && id != "" {
		return path + "." + id
	}
	return fmt.Sprintf("%s.%d", path, index)
}

// renderKeyLine builds a single TOML assignment line with a trailing inline
// comment that shows source provenance.
func renderKeyLine(tomlKey string, effective any, sources []Source) string {
	valueStr := formatValue(effective)
	assignment := tomlKey + " = " + valueStr
	comment := buildSourceComment(sources)
	const minPad = 30
	pad := max(minPad-len(assignment), 2)
	return assignment + strings.Repeat(" ", pad) + "# " + comment
}

// buildSourceComment returns the inline comment text for a set of sources.
// It is designed to be concise: one-source cases are a bare label;
// multi-source cases show the winner and a condensed override chain.
// An empty/nil sources slice means the key was never set by any source.
func buildSourceComment(sources []Source) string {
	if len(sources) == 0 {
		return "(not set)"
	}
	if len(sources) == 1 {
		return sourceLabel(sources[0])
	}
	winning := sources[len(sources)-1]
	prior := make([]string, 0, len(sources)-1)
	for _, s := range sources[:len(sources)-1] {
		prior = append(prior, sourceLabel(s))
	}
	chain := strings.Join(prior, " → ")
	return fmt.Sprintf("%s  (overrides %s)", sourceLabel(winning), chain)
}

// sourceLabel returns a human-readable label for a source.
func sourceLabel(s Source) string {
	switch s.File {
	case "<defaults>", "<env>", "<flag>":
		return s.File
	default:
		if s.Line > 0 {
			return fmt.Sprintf("%s:%d", s.File, s.Line)
		}
		return s.File
	}
}

// Explain returns a human-readable explanation of where key got its value,
// together with the underlying source chain.
//
// key is a dotted path: "model.name", "permission.shell", etc.
// Returns a multi-line formatted string, the chain of Sources for the key,
// and any error (e.g. key not found in provenance).
//
// Example output:
//
//	permission.shell = "ask"
//	  set by:
//	    1. <defaults>                           : (set here)
//	    2. ~/.config/hygge/config.toml          : (set here)
//	    3. ~/.config/hygge/profiles/work.toml   : (set here)
//	  effective: "ask"  (from ~/.config/hygge/profiles/work.toml)
func Explain(prov Provenance, cfg *Config, key string) (string, []Source, error) {
	sources, ok := prov[key]
	if !ok || len(sources) == 0 {
		return "", nil, fmt.Errorf("config: key %q not found in provenance", key)
	}

	// Resolve the effective value from cfg.
	effective, err := resolveValue(cfg, key)
	if err != nil {
		return "", nil, err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s = %s\n", key, formatValue(effective))
	sb.WriteString("  set by:\n")
	for i, src := range sources {
		lineInfo := ""
		if src.Line > 0 {
			lineInfo = fmt.Sprintf(":%d", src.Line)
		}
		fmt.Fprintf(&sb, "    %d. %-52s : %s\n",
			i+1,
			src.File+lineInfo,
			formatValue(src),
		)
	}

	winning := sources[len(sources)-1]
	fmt.Fprintf(&sb, "  effective: %s   (from %s)\n",
		formatValue(effective),
		winning.File,
	)

	return sb.String(), sources, nil
}

// resolveValue walks the Config struct to find the value at the dotted key.
// This is intentionally simple and handles the stable typed schema used by the
// focused single-key explain command.
func resolveValue(cfg *Config, key string) (any, error) {
	parts := strings.SplitN(key, ".", 3)
	switch parts[0] {
	case "model":
		if len(parts) < 2 {
			return nil, fmt.Errorf("config: explain: incomplete key %q", key)
		}
		switch parts[1] {
		case "provider":
			return cfg.Model.Provider, nil
		case "name":
			return cfg.Model.Name, nil
		case "small_provider":
			return cfg.Model.SmallProvider, nil
		case "small_model":
			return cfg.Model.SmallModel, nil
		case "reasoning":
			return cfg.Model.Reasoning, nil
		case "reasoning_budget":
			return cfg.Model.ReasoningBudget, nil
		case "options":
			if len(parts) == 3 {
				v, ok := cfg.Model.Options[parts[2]]
				if !ok {
					return nil, fmt.Errorf("config: explain: key %q not in model.options", key)
				}
				return v, nil
			}
			return cfg.Model.Options, nil
		}
	case "permission":
		if len(parts) < 2 {
			return nil, fmt.Errorf("config: explain: incomplete key %q", key)
		}
		switch parts[1] {
		case "file_read_outside_pwd":
			return cfg.Permission.FileReadOutsidePwd, nil
		case "file_write":
			return cfg.Permission.FileWrite, nil
		case "shell":
			return cfg.Permission.Shell, nil
		case "network":
			return cfg.Permission.Network, nil
		case "mcp":
			return cfg.Permission.MCP, nil
		case "subagent":
			return cfg.Permission.Subagent, nil
		}
	case "theme":
		if len(parts) >= 2 && parts[1] == "name" {
			return cfg.Theme.Name, nil
		}
	case "ui":
		if len(parts) == 2 && parts[1] == "nerd_fonts" {
			return cfg.UI.NerdFonts, nil
		}
	case "compaction":
		if len(parts) == 2 && parts[1] == "threshold_pct" {
			return cfg.Compaction.ThresholdPct, nil
		}
	case "session":
		if len(parts) == 2 && parts[1] == "resume_default" {
			return cfg.Session.ResumeDefault, nil
		}
	case "catalog":
		if len(parts) == 2 && parts[1] == "refresh_interval" {
			return cfg.Catalog.RefreshInterval, nil
		}
	case "notifications":
		if len(parts) == 2 {
			switch parts[1] {
			case "enabled":
				return cfg.Notifications.Enabled, nil
			case "permission_ask":
				return cfg.Notifications.PermissionAsk, nil
			case "turn_complete":
				return cfg.Notifications.TurnComplete, nil
			}
		}
	}
	return nil, fmt.Errorf("config: explain: unknown key %q", key)
}

// formatValue returns a TOML-like representation of a value.
func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case PermissionMode:
		return fmt.Sprintf("%q", string(val))
	case Source:
		// Used in the explain output per-source line — we want to show
		// the source's "contribution" rather than a real value since we
		// track only file-level provenance today (not per-key values).
		return "(set here)"
	case bool:
		return fmt.Sprintf("%v", val)
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%v", val)
	case []string:
		items := make([]string, 0, len(val))
		for _, item := range val {
			items = append(items, formatValue(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case []any:
		items := make([]string, 0, len(val))
		for _, item := range val {
			items = append(items, formatValue(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]any:
		keys := orderedKeys(val, nil)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s = %s", key, formatValue(val[key])))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	default:
		return fmt.Sprintf("%v", val)
	}
}
