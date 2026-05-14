package tool

import (
	"bytes"
	"encoding/json"
)

// newStrictDecoder returns a json.Decoder that errors on unknown fields.
// Kept in its own file so helpers.go stays import-light.
func newStrictDecoder(raw []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec
}
