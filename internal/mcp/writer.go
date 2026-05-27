package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// AppendServerOptions describes the server entry to write and where to
// write it.  Secrets (bearer tokens, API keys destined for HTTP headers)
// must not be passed here; they belong in AuthEntry via SetAuth.
type AppendServerOptions struct {
	// Path is the absolute path of the mcp.toml file to write to.
	// The parent directory is created if absent.  If the file already
	// exists, the stanza is appended; if it does not, the file is
	// created.
	Path string

	// Server is the configuration entry to append.  Fields that are
	// zero/empty/false are omitted from the written stanza.
	//
	// Name and Transport are required.  For "stdio" Transport, Command
	// is required.  For "sse"/"http" Transport, URL is required.
	//
	// Headers are written as $VAR-style references (e.g.
	// Authorization = "$MY_TOKEN") — the literal values must be stored
	// separately via SetAuth so that the mcp.toml is safe to commit.
	Server AppendServerSpec
}

// AppendServerSpec is the data-transfer object for one [[servers]] block.
// It mirrors tomlServer without the unexported normalisation plumbing.
type AppendServerSpec struct {
	Name               string
	Transport          string // "stdio" | "sse" | "http"
	Command            string
	Args               []string
	Env                map[string]string
	Dir                string
	URL                string
	Headers            map[string]string // values should be $VAR refs
	OAuth              bool              // true → write oauth = true
	Enabled            *bool             // nil → omit (default true)
	PermissionCategory string            // empty → omit (default "mcp")
}

// AppendServer validates spec and appends a [[servers]] TOML stanza to
// the file at opts.Path.  The parent directory is created with mode
// 0o755.  The file (or appended content) is written with mode 0o644.
//
// If a server with the same Name already exists in the file this
// function returns an error — it will not create duplicates.  Callers
// that want to update an existing entry should remove it first or use a
// different name.
func AppendServer(opts AppendServerOptions) error {
	spec := opts.Server

	// --- validation ---
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("mcp writer: name is required")
	}
	transport := strings.TrimSpace(spec.Transport)
	if transport == "" {
		transport = "stdio"
	}
	if !validTransports[transport] {
		return fmt.Errorf("mcp writer: unknown transport %q (supported: stdio, sse, http)", transport)
	}
	switch transport {
	case "stdio":
		if spec.OAuth {
			return fmt.Errorf("mcp writer: oauth cannot be used with transport %q", transport)
		}
		if strings.TrimSpace(spec.Command) == "" {
			return fmt.Errorf("mcp writer: command is required for transport %q", transport)
		}
	case "sse", "http":
		if strings.TrimSpace(spec.URL) == "" {
			return fmt.Errorf("mcp writer: url is required for transport %q", transport)
		}
	}

	// --- duplicate check ---
	if _, err := os.Stat(opts.Path); err == nil {
		data, readErr := os.ReadFile(opts.Path) //nolint:gosec
		if readErr != nil {
			return fmt.Errorf("mcp writer: read %s: %w", opts.Path, readErr)
		}
		var raw tomlSchema
		if unmarshalErr := unmarshalTOML(data, &raw); unmarshalErr != nil {
			return fmt.Errorf("mcp writer: parse %s: %w", opts.Path, unmarshalErr)
		}
		for _, s := range raw.Servers {
			if strings.TrimSpace(s.Name) == strings.TrimSpace(spec.Name) {
				return fmt.Errorf("mcp writer: server %q already exists in %s; remove it first", spec.Name, opts.Path)
			}
		}
	}

	// --- build stanza ---
	var sb strings.Builder
	sb.WriteString("\n[[servers]]\n")
	writeField(&sb, "name", spec.Name)
	if transport != "stdio" {
		writeField(&sb, "transport", transport)
	}
	if spec.Command != "" {
		writeField(&sb, "command", spec.Command)
	}
	if len(spec.Args) > 0 {
		sb.WriteString("args = [")
		for i, a := range spec.Args {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(quoteStringTOML(a))
		}
		sb.WriteString("]\n")
	}
	if len(spec.Env) > 0 {
		sb.WriteString("env = { ")
		keys := sortedStringKeys(spec.Env)
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(quoteStringTOML(k))
			sb.WriteString(" = ")
			sb.WriteString(quoteStringTOML(spec.Env[k]))
		}
		sb.WriteString(" }\n")
	}
	if spec.Dir != "" {
		writeField(&sb, "dir", spec.Dir)
	}
	if spec.URL != "" {
		writeField(&sb, "url", spec.URL)
	}
	if len(spec.Headers) > 0 {
		sb.WriteString("headers = { ")
		keys := sortedStringKeys(spec.Headers)
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(quoteStringTOML(k))
			sb.WriteString(" = ")
			sb.WriteString(quoteStringTOML(spec.Headers[k]))
		}
		sb.WriteString(" }\n")
	}
	if spec.OAuth && transport != "stdio" {
		sb.WriteString("oauth = true\n")
	}
	if spec.Enabled != nil && !*spec.Enabled {
		sb.WriteString("enabled = false\n")
	}
	if spec.PermissionCategory != "" && spec.PermissionCategory != "mcp" {
		writeField(&sb, "permission_category", spec.PermissionCategory)
	}

	// --- write ---
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o700); err != nil {
		return fmt.Errorf("mcp writer: create dir: %w", err)
	}
	f, err := os.OpenFile(opts.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("mcp writer: open %s: %w", opts.Path, err)
	}
	_, writeErr := f.WriteString(sb.String())
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("mcp writer: write %s: %w", opts.Path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("mcp writer: close %s: %w", opts.Path, closeErr)
	}
	return nil
}

// writeField writes a TOML key = "value" line.
func writeField(sb *strings.Builder, key, value string) {
	sb.WriteString(key)
	sb.WriteString(" = ")
	sb.WriteString(quoteStringTOML(value))
	sb.WriteByte('\n')
}

// quoteStringTOML returns value as a TOML basic string.
func quoteStringTOML(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// sortedStringKeys returns the keys of m in sorted order.
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort (maps are small).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// unmarshalTOML decodes data into dst using go-toml.  Separated so
// writer.go can call the same parser without depending on a concrete
// import block — it reuses config.go's toml import transitively since
// both live in package mcp.
func unmarshalTOML(data []byte, dst *tomlSchema) error {
	return toml.Unmarshal(data, dst)
}
