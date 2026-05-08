package tools

// meta_helpers.go — shape-decoding utilities and self-teaching error
// builders shared across the meta tools (meta_search, meta_help,
// meta_call, meta_batch). Lives apart from meta.go so the registration/
// budget core doesn't grow every time we add another robust-decoder
// alias for a model that emits "input"/"arguments"/"tool" instead of
// the schema-canonical "args"/"name".

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func isMetaTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "tool_search", "tool_help", "tool_call", "tool_batch_call":
		return true
	}
	return false
}

func extractArgsObject(params map[string]any, key string) (map[string]any, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		// Be defensive: some models (especially third-party OpenAI-compatible
		// endpoints) emit the arguments under "input" or "arguments" despite
		// our schema naming the field "args". Accept those as aliases when
		// the primary key is missing, rather than failing the call outright.
		if key == "args" {
			for _, alt := range []string{"input", "arguments", "params"} {
				if v, has := params[alt]; has && v != nil {
					raw = v
					ok = true
					break
				}
			}
		}
		if !ok || raw == nil {
			return map[string]any{}, nil
		}
	}
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return map[string]any{}, nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
			return nil, fmt.Errorf("%s must be a JSON object: %w", key, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be an object, got %T", key, raw)
	}
}

// pickToolName reads the tool-name field from a call object, accepting
// `name` (the schema-correct key) and `tool` as a fallback. Some models
// — particularly fine-tuned OpenAI-compat endpoints — emit `tool` when
// reproducing a tool-call shape from training data. Accepting the alias
// turns what would be a hard failure into a working call.
func pickToolName(obj map[string]any) string {
	if name := strings.TrimSpace(asString(obj, "name", "")); name != "" {
		return name
	}
	return strings.TrimSpace(asString(obj, "tool", ""))
}

// previewBatchTarget returns a one-line "what is this call about?" hint
// derived from the call's args. Picks the first identifying key in a
// deterministic priority order (path > pattern > query > command > dir
// > url > name) so the TUI shows "✓ read_file foo.go" instead of just
// "✓ read_file". Empty string when nothing identifying is present —
// the caller skips the field rather than rendering "✓ read_file (no
// args)".
func previewBatchTarget(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"path", "pattern", "query", "command", "dir", "url", "name"} {
		if raw, ok := args[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value == "" {
				continue
			}
			// run_command stays useful when we surface command + first arg.
			if key == "command" {
				if rest := previewCommandArgs(args["args"]); rest != "" {
					value = value + " " + rest
				}
			}
			if len(value) > 64 {
				value = value[:61] + "..."
			}
			return value
		}
	}
	return ""
}

// previewCommandArgs renders a short, single-line preview of the args
// that follow `command` for run_command-shaped calls. Accepts the
// shapes commandArgs() accepts (string, []string, []any).
func previewCommandArgs(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.Join(v, " ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func currentToolReason(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	reason, _ := ctx.Value(toolReasonContextKey{}).(string)
	return strings.TrimSpace(reason)
}

func inheritToolReason(ctx context.Context, args map[string]any) string {
	if args == nil {
		return ""
	}
	if reason := reasonFromParams(args); reason != "" {
		return reason
	}
	reason := currentToolReason(ctx)
	if reason == "" {
		return ""
	}
	args[ReasonField] = reason
	return reason
}

func reasonFromParams(args map[string]any) string {
	if args == nil {
		return ""
	}
	raw, ok := args[ReasonField]
	if !ok {
		return ""
	}
	reason, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(reason)
}

// missingNameError builds the actionable "name is required" reply for
// the meta tools. Pre-fix the error was just "name is required" — the
// model couldn't tell whether it had passed the wrong key, sent args
// at the wrong nesting level, or just forgotten the field. Listing the
// keys it ACTUALLY sent + the canonical example lets the next call
// self-correct in a single round instead of looping with the same bug.
func missingNameError(toolName string, params map[string]any, example string) error {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	got := "(empty)"
	if len(keys) > 0 {
		got = "[" + strings.Join(keys, ", ") + "]"
	}
	return fmt.Errorf(
		"%s requires a `name` field naming the backend tool to invoke. "+
			"Got params keys %s but no `name` (or alias `tool`). "+
			"Correct shape: %s",
		toolName, got, example)
}
