package config

import (
	"fmt"
	"strings"
)

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
//	    1. <defaults>                           : "ask"
//	    2. ~/.config/hygge/config.toml          : "deny"
//	    3. ~/.config/hygge/profiles/work.toml   : "ask"
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
// This is intentionally simple — only handles the known v0.1 schema.
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
		}
	case "theme":
		if len(parts) >= 2 && parts[1] == "name" {
			return cfg.Theme.Name, nil
		}
	}
	return nil, fmt.Errorf("config: explain: unknown key %q", key)
}

// formatValue returns a human-readable TOML-like representation of a value.
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
	case int, int64, float64:
		return fmt.Sprintf("%v", val)
	case map[string]any:
		return "{...}"
	default:
		return fmt.Sprintf("%v", val)
	}
}
