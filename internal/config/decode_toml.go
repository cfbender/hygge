package config

import (
	"bytes"
	"os"

	toml "github.com/pelletier/go-toml/v2"
)

// loadTOMLFile reads a TOML file at path and returns its contents as a
// map[string]any.  Parse errors from go-toml/v2 include file position
// information (row, column) via the DecodeError type.
//
// TODO: Future task — add 1Password op:// resolution for string values here.
func loadTOMLFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // intentional: path is a config file location
	if err != nil {
		return nil, err
	}
	return parseTOMLBytes(data)
}

// parseTOMLBytes decodes a TOML document from raw bytes into map[string]any.
// Errors carry position info from the pelletier decoder (row, column).
func parseTOMLBytes(data []byte) (map[string]any, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}
