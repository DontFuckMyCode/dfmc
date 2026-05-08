package tools

// command_args.go — argv tokenization and timeout resolution helpers
// shared by run_command. Companion siblings:
//
//   - command.go              RunCommandTool.Execute + result shaping
//   - command_validate.go     binary blocklist + arg-sequence policy +
//                             shell-metacharacter / interpreter detection
//   - command_recovery.go     hint generators for the two common LLM
//                             packing footguns ("cd dir && cmd" and
//                             "go build ./..." in the binary slot)
//
// commandArgs accepts the three shapes the model reaches for: nil,
// a single string ("--flag value"), a []string, or a []any from JSON.
// splitCommandArgs runs the only quote-aware path; everything else
// is preserved verbatim. resolveCommandTimeout honours either the
// duration string `timeout: "30s"` or the numeric `timeout_ms: 30000`,
// clamped against the configured ceiling.

import (
	"fmt"
	"strings"
	"time"
)

func commandArgs(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		return splitCommandArgs(v)
	case []string:
		return append([]string(nil), v...), nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out, nil
	default:
		return splitCommandArgs(fmt.Sprint(v))
	}
}

func splitCommandArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var (
		args    []string
		current strings.Builder
		quote   rune
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}
	for _, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted args value")
	}
	flush()
	return args, nil
}

func resolveCommandTimeout(params map[string]any, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = 30 * time.Second
	}
	if params == nil {
		return fallback
	}
	if raw := strings.TrimSpace(asString(params, "timeout", "")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return clampCommandTimeout(d, fallback)
		}
	}
	if ms := asInt(params, "timeout_ms", 0); ms > 0 {
		return clampCommandTimeout(time.Duration(ms)*time.Millisecond, fallback)
	}
	return fallback
}

func clampCommandTimeout(requested, limit time.Duration) time.Duration {
	if requested <= 0 {
		return limit
	}
	if limit > 0 && requested > limit {
		return limit
	}
	return requested
}
