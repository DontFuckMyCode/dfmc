package tools

import (
	"strings"
)

// normalizeToolParams rewrites the params map for tools that have
// non-obvious field names (common in JS/Python-trained models) so
// Execute always sees the canonical shape. Extracted from engine.go
// to keep the god file split. See engine.go:495.
func normalizeToolParams(name string, params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "line_start", "start", "from", "lineStart", "start_line")
		promoteFirstAlias(params, "line_end", "end", "to", "lineEnd", "end_line")
		start := asInt(params, "line_start", 1)
		start = max(1, start)
		end := asInt(params, "line_end", start+199)
		end = max(start, end)
		if end-start+1 > 400 {
			end = start + 399
		}
		params["line_start"] = start
		params["line_end"] = end
	case "list_dir":
		promoteFirstAlias(params, "path", "dir", "directory", "target", "root")
		promoteFirstAlias(params, "max_entries", "limit", "max", "maxEntries")
		promoteFirstAlias(params, "recursive", "recurse")
		maxEntries := asInt(params, "max_entries", 200)
		if maxEntries <= 0 {
			maxEntries = 200
		}
		if maxEntries > 500 {
			maxEntries = 500
		}
		params["max_entries"] = maxEntries
	case "grep_codebase":
		promoteFirstAlias(params, "pattern", "query", "regex", "text", "needle", "search")
		promoteFirstAlias(params, "path", "dir", "directory", "root")
		promoteFirstAlias(params, "max_results", "limit", "max", "maxResults")
		promoteFirstAlias(params, "case_sensitive", "caseSensitive")
		promoteFirstAlias(params, "context", "context_lines", "contextLines")
		promoteFirstAlias(params, "before", "before_lines", "context_before")
		promoteFirstAlias(params, "after", "after_lines", "context_after")
		maxResults := asInt(params, "max_results", 80)
		if maxResults <= 0 {
			maxResults = 80
		}
		if maxResults > 500 {
			maxResults = 500
		}
		params["max_results"] = maxResults
	case "glob":
		promoteFirstAlias(params, "pattern", "glob", "query", "match")
		promoteFirstAlias(params, "path", "dir", "directory", "root")
		promoteFirstAlias(params, "max_results", "limit", "max", "maxResults")
	case "ast_query":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "kind", "type", "symbol_kind")
		promoteFirstAlias(params, "name_contains", "name", "query", "filter", "contains")
	case "find_symbol":
		promoteFirstAlias(params, "name", "symbol", "query", "identifier")
		promoteFirstAlias(params, "kind", "type", "symbol_kind")
		promoteFirstAlias(params, "path", "dir", "directory", "file")
		promoteFirstAlias(params, "max_results", "limit", "max", "maxResults")
		promoteFirstAlias(params, "include_body", "body", "with_body")
	case "run_command":
		promoteFirstAlias(params, "command", "cmd", "program", "executable", "bin")
		promoteFirstAlias(params, "args", "argv", "arguments", "command_args")
		promoteFirstAlias(params, "dir", "cwd", "workdir", "working_dir")
		promoteFirstAlias(params, "timeout_ms", "timeoutMs", "timeout")
		timeoutMs := asInt(params, "timeout_ms", 0)
		timeoutMs = max(0, timeoutMs)
		timeoutMs = min(timeoutMs, 120_000)
		if timeoutMs > 0 {
			params["timeout_ms"] = timeoutMs
		}
	case "edit_file":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "replace_all", "replaceAll", "all", "global")
		if _, ok := params["old_string"]; !ok {
			if v, alt := params["old"]; alt {
				params["old_string"] = v
				delete(params, "old")
			}
		}
		if _, ok := params["new_string"]; !ok {
			if v, alt := params["new"]; alt {
				params["new_string"] = v
				delete(params, "new")
			}
		}
	case "write_file":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "overwrite", "force", "replace", "allow_overwrite")
		if _, ok := params["content"]; !ok {
			for _, alt := range []string{"text", "body", "data"} {
				if v, found := params[alt]; found {
					params["content"] = v
					delete(params, alt)
					break
				}
			}
		}
	}
	return params
}

// promoteFirstAlias promotes the first alias that is present in params
// to the canonical key name. Canonical wins when both are present.
// Extracted from engine.go to keep the god file split. See engine.go:607.
func promoteFirstAlias(params map[string]any, canonical string, aliases ...string) {
	if params == nil {
		return
	}
	if _, ok := params[canonical]; ok {
		return
	}
	for _, alt := range aliases {
		if v, ok := params[alt]; ok {
			params[canonical] = v
			delete(params, alt)
			return
		}
	}
}
