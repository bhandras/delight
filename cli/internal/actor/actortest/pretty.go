package actortest

import (
	"encoding/json"
	"fmt"
)

// Pretty returns a snapshot-friendly string representation of v.
//
// It prefers JSON when possible (stable ordering for structs), and falls back
// to Go formatting for types that cannot be marshaled (e.g. channels).
func Pretty(v any) string {
	if v == nil {
		return "<nil>"
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err == nil {
		return string(data)
	}
	return fmt.Sprintf("%#v", v)
}

