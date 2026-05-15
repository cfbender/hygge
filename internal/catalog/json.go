package catalog

import "encoding/json"

// decodeJSON is a thin wrapper around json.Unmarshal, kept as a named
// helper so callers read more clearly.
func decodeJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// encodeJSONIndent marshals v to pretty-printed JSON.
func encodeJSONIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
