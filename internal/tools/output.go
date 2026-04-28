package tools

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// compressToolOutput applies the configured byte limit to the tool result
// output, preserving the first and last 20 lines plus any lines matching
// relevance terms. Extracted from engine.go to keep the god file split.
// See engine.go:737.
func (e *Engine) compressToolOutput(req Request, res Result) Result {
	limit := e.resolveOutputByteLimit(req.Params)
	if limit <= 0 || strings.TrimSpace(res.Output) == "" {
		return res
	}
	out, compressed, omittedLines := compressOutput(res.Output, limit, collectRelevanceTerms(req.Params))
	if !compressed {
		return res
	}
	if res.Data == nil {
		res.Data = map[string]any{}
	}
	res.Data["output_original_bytes"] = len([]byte(res.Output))
	res.Data["output_compressed_bytes"] = len([]byte(out))
	res.Data["output_omitted_lines"] = omittedLines
	res.Output = out
	res.Truncated = true
	return res
}

func (e *Engine) resolveOutputByteLimit(params map[string]any) int {
	if v := asInt(params, "max_output_bytes", 0); v > 0 {
		return v
	}
	if v := asInt(params, "max_output_chars", 0); v > 0 {
		return v
	}
	return parseByteLimit(e.cfg.Security.Sandbox.MaxOutput, 100*1024)
}

func parseByteLimit(raw string, fallback int) int {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return fallback
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "KB"):
		mult = 1024
		s = strings.TrimSpace(strings.TrimSuffix(s, "KB"))
	case strings.HasSuffix(s, "MB"):
		mult = 1024 * 1024
		s = strings.TrimSpace(strings.TrimSuffix(s, "MB"))
	case strings.HasSuffix(s, "B"):
		mult = 1
		s = strings.TrimSpace(strings.TrimSuffix(s, "B"))
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fallback
	}
	return n * mult
}

func collectRelevanceTerms(params map[string]any) []string {
	if params == nil {
		return nil
	}
	keys := []string{"pattern", "query", "symbol", "name", "path"}
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, k := range keys {
		v := strings.TrimSpace(strings.ToLower(asString(params, k, "")))
		if v == "" {
			continue
		}
		for _, token := range strings.FieldsFunc(v, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '/' || r == ':' || r == ',' || r == ';' || r == '.'
		}) {
			t := strings.TrimSpace(token)
			if len(t) < 3 {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func compressOutput(output string, limit int, terms []string) (string, bool, int) {
	if len([]byte(output)) <= limit || limit <= 0 {
		return output, false, 0
	}

	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return truncateUTF8ByBytes(output, limit), true, 0
	}

	headN, tailN := 20, 20
	keep := map[int]struct{}{}
	for i := 0; i < minInt(headN, len(lines)); i++ {
		keep[i] = struct{}{}
	}
	for i := maxInt(0, len(lines)-tailN); i < len(lines); i++ {
		keep[i] = struct{}{}
	}

	if len(terms) > 0 {
		for i, line := range lines {
			low := strings.ToLower(line)
			for _, t := range terms {
				if strings.Contains(low, t) {
					for j := maxInt(0, i-1); j <= minInt(len(lines)-1, i+1); j++ {
						keep[j] = struct{}{}
					}
					break
				}
			}
		}
	}

	ordered := make([]int, 0, len(keep))
	for idx := range keep {
		ordered = append(ordered, idx)
	}
	sort.Ints(ordered)

	var b strings.Builder
	omitted := 0
	prev := -1
	for _, idx := range ordered {
		if prev >= 0 && idx > prev+1 {
			gap := idx - prev - 1
			omitted += gap
			fmt.Fprintf(&b, "... [omitted %d lines]\n", gap)
		}
		b.WriteString(lines[idx])
		if idx < len(lines)-1 {
			b.WriteByte('\n')
		}
		prev = idx
	}

	compressed := b.String()
	if len([]byte(compressed)) > limit {
		compressed = truncateUTF8ByBytes(compressed, limit)
	}
	return compressed, true, omitted
}

func truncateUTF8ByBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	b := []byte(s)
	if len(b) <= maxBytes {
		return s
	}
	ellipsis := "\n... [truncated]"
	limit := maxBytes - len([]byte(ellipsis))
	if limit <= 0 {
		limit = maxBytes
		ellipsis = ""
	}
	var out strings.Builder
	n := 0
	for _, r := range s {
		rb := utf8.RuneLen(r)
		if n+rb > limit {
			break
		}
		out.WriteRune(r)
		n += rb
	}
	if ellipsis != "" {
		out.WriteString(ellipsis)
	}
	return out.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
