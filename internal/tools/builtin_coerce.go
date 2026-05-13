// builtin_coerce.go — shape-tolerant param coercion for built-in
// tools. Sibling of builtin.go which keeps the WriteFileTool, the
// missingParamError actionable-error builder, valueLooksLikePath, and
// the truncateToolTextWithMarker output trimmer.
//
// Splitting the coercers out keeps builtin.go focused on tool surface
// + actionable errors while this file owns "given a map[string]any
// from a model, normalize the value to the Go type the tool wants".
// All coercers tolerate the shapes providers and weaker models
// actually serialize — the package's single source of truth so every
// tool accepts the same input shapes (e.g. arrays-or-CSV-strings,
// numbers-as-strings, "True"/"yes"/"1" booleans, JSON-decoded float64
// for integer values).

package tools

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	maxIntValue = int(^uint(0) >> 1)
	minIntValue = -maxIntValue - 1
)

func asString(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	// Nil + slice + map all produce gibberish via fmt.Sprint:
	//   nil      → "<nil>"
	//   []any{x} → "[x]"
	//   map      → "map[k:v]"
	// Tools using these as paths/queries would silently fail with
	// "file <nil> not found" style errors. Return fallback so the
	// caller's required-field check fires with a useful message
	// instead, or the default value applies.
	if v == nil {
		return fallback
	}
	switch vv := v.(type) {
	case string:
		return vv
	case []any, map[string]any:
		_ = vv
		return fallback
	default:
		return fmt.Sprint(v)
	}
}

func asInt(m map[string]any, key string, fallback int) int {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch vv := v.(type) {
	case int:
		return vv
	case int32:
		return int(vv)
	case int64:
		return int(vv)
	case uint:
		return int(vv)
	case uint32:
		return int(vv)
	case uint64:
		return int(vv)
	case float64:
		if math.IsNaN(vv) || math.IsInf(vv, 0) || vv != math.Trunc(vv) {
			return fallback
		}
		if vv < float64(minIntValue) || vv > float64(maxIntValue) {
			return fallback
		}
		return int(vv)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(vv))
		if err == nil {
			return n
		}
	}
	return fallback
}

func asFloat(m map[string]any, key string, fallback float64) float64 {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch vv := v.(type) {
	case float64:
		if math.IsNaN(vv) || math.IsInf(vv, 0) {
			return fallback
		}
		return vv
	case float32:
		f := float64(vv)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fallback
		}
		return f
	case int:
		return float64(vv)
	case int32:
		return float64(vv)
	case int64:
		return float64(vv)
	case uint:
		return float64(vv)
	case uint32:
		return float64(vv)
	case uint64:
		return float64(vv)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(vv), 64)
		if err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	}
	return fallback
}

// asStringSlice extracts a list-of-strings param tolerating the
// shapes providers and weaker models actually serialize:
//   - []string                 → trimmed copy
//   - []any{"a", "b"}          → stringified + trimmed
//   - "a, b , c"               → CSV split
//   - "a"                      → []string{"a"}  (single bare string)
//   - nil / missing / wrong    → nil
//
// Empty entries are dropped so `include: ", , *.go"` produces
// ["*.go"] not ["", "", "*.go"]. The pre-consolidation helpers
// (splitGlobList, stringSliceArg) had small contract drifts; this
// is the single source of truth so all tools accept the same shapes.
func asStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	return coerceStringSlice(m[key])
}

// coerceStringSlice is the shape-agnostic core; callers that already
// extracted the raw value (e.g. from a nested struct) use this directly.
func coerceStringSlice(raw any) []string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if item == nil {
				continue
			}
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		out := make([]string, 0, 4)
		for _, part := range strings.Split(v, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func asBool(m map[string]any, key string, fallback bool) bool {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch vv := v.(type) {
	case bool:
		return vv
	case string:
		// Trim+lower so " True ", "TRUE", "yes", etc. don't fall
		// through to the fallback. Earlier the EqualFold/== checks
		// matched only bare "true"/"1" — a model passing `"True"` for
		// case_sensitive silently got false, which is exactly the
		// kind of provider-side serialization variance the asInt /
		// asString helpers already tolerate.
		s := strings.ToLower(strings.TrimSpace(vv))
		switch s {
		case "true", "yes", "y", "1", "on":
			return true
		case "false", "no", "n", "0", "off", "":
			return false
		}
		return fallback
	case int:
		return vv != 0
	case int32:
		return vv != 0
	case int64:
		return vv != 0
	case float64:
		// Tolerate JSON-decoded `1`/`0` which arrive as float64 here.
		// Reject NaN to avoid the surprising `NaN != 0 == true` case.
		if vv != vv { // NaN check
			return fallback
		}
		return vv != 0
	}
	return fallback
}
