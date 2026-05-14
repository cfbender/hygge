package config

// PermissionMode is the set of allowed values for permission fields.
type PermissionMode string

// Permission mode constants.
const (
	// PermAllow grants the operation without prompting.
	PermAllow PermissionMode = "allow"
	// PermAsk prompts the user before allowing the operation.
	PermAsk PermissionMode = "ask"
	// PermDeny rejects the operation without prompting.
	PermDeny PermissionMode = "deny"
)

// defaultConfig returns the built-in defaults for every config key.
// Keys not supplied by any source fall back to these values.
func defaultConfig() map[string]any {
	return map[string]any{
		"model": map[string]any{
			"provider": "anthropic",
			"name":     "claude-sonnet-4-5",
			"options":  map[string]any{},
		},
		"permission": map[string]any{
			"file_read_outside_pwd": string(PermAsk),
			"file_write":            string(PermAsk),
			"shell":                 string(PermAsk),
			"network":               string(PermDeny),
			"mcp":                   string(PermAsk),
		},
		"theme": map[string]any{
			"name": "shell",
		},
	}
}
