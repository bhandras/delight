package wire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	dumpEnableEnv = "DELIGHT_WIRE_DUMP"
	dumpDirEnv    = "DELIGHT_WIRE_DUMP_DIR"
)

var base64Like = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)

// DumpToTestdata writes a sanitized JSON fixture for a wire payload.
//
// This is intended for capturing real payloads during development, so they can
// be checked into `cli/internal/protocol/wire/testdata/` and used as golden
// tests.
//
// Enable by setting either:
//   - `DELIGHT_WIRE_DUMP=1` (writes to a default repo-relative directory),
//     or
//   - `DELIGHT_WIRE_DUMP_DIR=/abs/path` (writes to that directory).
//
// The fixture is sanitized to avoid committing secrets or personal data.
func DumpToTestdata(kind string, payload any) {
	dir := os.Getenv(dumpDirEnv)
	if dir == "" {
		if os.Getenv(dumpEnableEnv) == "" {
			return
		}
		dir = filepath.Join("protocol", "wire", "testdata", "captured")
	}

	if kind == "" {
		kind = "unknown"
	}

	value, err := normalizeToAny(payload)
	if err != nil {
		return
	}

	sanitized := sanitize(value)
	raw, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return
	}
	raw = append(raw, '\n')

	_ = os.MkdirAll(dir, 0o755)
	name := fmt.Sprintf("%s_%d.json", safeFilename(kind), time.Now().UnixMilli())
	_ = os.WriteFile(filepath.Join(dir, name), raw, 0o644)
}

func normalizeToAny(payload any) (any, error) {
	switch v := payload.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		var out any
		if err := json.Unmarshal(v, &out); err != nil {
			return string(v), nil
		}
		return out, nil
	case []byte:
		var out any
		if err := json.Unmarshal(v, &out); err != nil {
			return string(v), nil
		}
		return out, nil
	default:
		// Round-trip through JSON to detach from mutable maps/slices.
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		var out any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

func sanitize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = sanitizeKV(k, vv)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, vv := range t {
			out = append(out, sanitize(vv))
		}
		return out
	default:
		return sanitizeScalar(t)
	}
}

func sanitizeKV(key string, value any) any {
	lower := strings.ToLower(key)
	switch lower {
	case "sid", "sessionid", "machineid", "localid", "requestid":
		return "<id>"
	case "directory", "path", "cwd", "workdir":
		return "<path>"
	case "token", "authorization":
		return "<redacted>"
	}
	return sanitize(value)
}

func sanitizeScalar(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}

	trim := strings.TrimSpace(s)
	if trim == "" {
		return s
	}

	// Ciphertext and other long blobs: replace with a stable placeholder.
	if len(trim) > 40 && base64Like.MatchString(trim) {
		return "cipher"
	}

	// Avoid committing long free-form text.
	if len(trim) > 120 {
		return "<redacted>"
	}

	return s
}

func safeFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
	s = strings.Trim(s, "_")
	if s == "" {
		return "unknown"
	}
	return s
}
