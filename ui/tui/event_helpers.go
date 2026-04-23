// event_helpers.go — pure helpers used only by engine_events.go's
// event router. Two groups:
//
//   - payload* getters — type-safe map[string]any readers tolerant of
//     int/int32/int64/float/string number shapes (events cross goroutine
//     + serialisation boundaries, so the underlying type isn't always
//     what Go's compile-time type assertion expects). payloadStringSlice
//     unifies []string and []any. Returns the caller-supplied fallback
//     whenever the key is missing / wrong-typed / empty after trim.
//   - small format helpers — shortID (truncate drive run IDs for chip
//     display), truncateForLine (flatten + cap a string for a single
//     scannable line), and three subagent* formatters used by chip
//     captions ("provider/model", "a -> b -> c", "from -> to").
//
// Nothing here touches Model state or the event bus; extracting them
// keeps engine_events.go focused on the event-type switch.

package tui

import (
	"fmt"
	"strconv"
	"strings"
)

func payloadString(data map[string]any, key, fallback string) string {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return fallback
		}
		return value
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return fallback
		}
		return text
	}
}

func payloadInt(data map[string]any, key string, fallback int) int {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return n
		}
	}
	return fallback
}

// payloadStringSlice extracts a string list from an event payload,
// tolerant of how JSON / Go runtime may shape the value (events cross
// goroutine + serialisation boundaries). Returns nil when the key is
// missing or the value can't be coerced into strings — callers treat
// nil as "no list to render" and skip.
func payloadStringSlice(data map[string]any, key string) []string {
	if data == nil {
		return nil
	}
	raw, ok := data[key]
	if !ok || raw == nil {
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
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func payloadBool(data map[string]any, key string, fallback bool) bool {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

// shortID trims a drive run ID to a UI-friendly prefix. Drive IDs are
// `drv-<unix>-<rand>` (~20 chars); the chip line gets unreadable past
// the first 12.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// truncateForLine flattens newlines and caps length so a single
// transcript/notice line stays scannable. Mirrors cli's truncateLine
// but lives here to keep ui/tui self-contained.
func truncateForLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func subagentProviderLabel(providerName, modelName string) string {
	providerName = strings.TrimSpace(providerName)
	modelName = strings.TrimSpace(modelName)
	switch {
	case providerName != "" && modelName != "":
		return providerName + "/" + modelName
	case providerName != "":
		return providerName
	case modelName != "":
		return modelName
	default:
		return ""
	}
}

func subagentProfileChain(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	trimmed := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			trimmed = append(trimmed, candidate)
		}
	}
	if len(trimmed) == 0 {
		return ""
	}
	if len(trimmed) > 4 {
		return strings.Join(trimmed[:4], " -> ") + " -> ..."
	}
	return strings.Join(trimmed, " -> ")
}

func subagentProfileTransition(fromProfile, toProfile string) string {
	fromProfile = strings.TrimSpace(fromProfile)
	toProfile = strings.TrimSpace(toProfile)
	switch {
	case fromProfile != "" && toProfile != "":
		return fromProfile + " -> " + toProfile
	case toProfile != "":
		return "fallback -> " + toProfile
	default:
		return fromProfile
	}
}
