package provider

// anthropic_xml_tools.go — compatibility shim for "Anthropic-compatible"
// endpoints that emit tool calls as XML inside a text block rather than
// as proper tool_use content blocks.
//
// Real Anthropic models always use structured tool_use blocks. MiniMax's
// M2 model (served at api.minimax.io/anthropic/v1) instead returns a
// single text block whose body is XML of the shape:
//
//	<minimax:tool_call>
//	  <invoke name="read_file">
//	    <parameter name="path">foo.go</parameter>
//	    <parameter name="line_start">10</parameter>
//	  </invoke>
//	  <invoke name="grep_codebase">...</invoke>
//	</minimax:tool_call>
//
// Without this shim, the agent loop sees zero tool calls and prints the
// raw XML to the user — exactly the "bu ne?" confusion this fix
// addresses.
//
// The extractor is intentionally narrow:
//   - only invoked when native tool_use was absent (see splitAnthropicContent)
//   - triggered only when the text actually contains `<invoke name="` —
//     a pattern unlikely to appear in natural prose
//   - the outer `<minimax:tool_call>` wrapper is optional; bare <invoke>
//     blocks are also handled so other XML-style endpoints benefit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// invokeBlockRe captures one <invoke name="X">...</invoke> unit. Uses
	// (?s) so the body can span newlines. Non-greedy body match because
	// multiple invokes often appear back-to-back inside one wrapper.
	invokeBlockRe = regexp.MustCompile(`(?s)<invoke\s+name="([^"]+)"\s*>(.*?)</invoke\s*>`)

	// parameterRe captures one <parameter name="X">VALUE</parameter>.
	// Whitespace around the opening tag is tolerated because MiniMax
	// pretty-prints with leading indent.
	parameterRe = regexp.MustCompile(`(?s)<parameter\s+name="([^"]+)"\s*>(.*?)</parameter\s*>`)

	// wrapperRe matches the outer MiniMax wrapper so we can strip it
	// cleanly when present. Optional — extractXMLToolCalls also strips
	// individual invoke blocks on its own.
	wrapperRe = regexp.MustCompile(`(?s)<minimax:tool_call\s*>(.*?)</minimax:tool_call\s*>`)
)

// extractXMLToolCalls scans text for MiniMax-style (or legacy Anthropic
// XML-style) <invoke name="..."> blocks and returns:
//
//   - cleaned: the original text with the XML tool-call blocks removed
//     and surrounding whitespace tidied up. Regular prose outside the
//     blocks is preserved verbatim.
//   - calls: one ToolCall per <invoke> block, in source order. IDs are
//     synthesized because the XML format carries none; the engine uses
//     the ID only to match tool_result messages on the next turn so a
//     stable-per-response prefix is enough.
//
// Returns (text, nil) unchanged if the `<invoke name="` guard doesn't
// match — that's the hot path for real Anthropic responses.
func extractXMLToolCalls(text string) (string, []ToolCall) {
	if !strings.Contains(text, `<invoke name="`) {
		return text, nil
	}

	cleaned := text
	if wrapperRe.MatchString(cleaned) {
		cleaned = wrapperRe.ReplaceAllString(cleaned, "")
	}

	matches := invokeBlockRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	calls := make([]ToolCall, 0, len(matches))
	for i, m := range matches {
		name := text[m[2]:m[3]]
		body := text[m[4]:m[5]]
		calls = append(calls, ToolCall{
			ID:    fmt.Sprintf("synth-xml-%d", i+1),
			Name:  strings.TrimSpace(name),
			Input: parseXMLParameters(body),
		})
	}

	// Strip any remaining bare <invoke> blocks that weren't inside a
	// wrapper so the cleaned text doesn't carry duplicate noise.
	cleaned = invokeBlockRe.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)

	return cleaned, calls
}

// parseXMLParameters turns the body of one <invoke> block into a
// name→value map. Numeric- and boolean-looking strings are coerced so
// tool parameter validators (which expect typed JSON) see the right
// kind — MiniMax emits everything as text content, so a raw string map
// would fail int-typed params like read_file.line_start.
func parseXMLParameters(body string) map[string]any {
	out := map[string]any{}
	for _, m := range parameterRe.FindAllStringSubmatch(body, -1) {
		key := strings.TrimSpace(m[1])
		if key == "" {
			continue
		}
		raw := strings.TrimSpace(m[2])
		out[key] = coerceXMLValue(raw)
	}
	return out
}

// coerceXMLValue guesses the intended JSON type for a string pulled out
// of an XML <parameter> body. Order matters: check bool before int so
// "true"/"false" don't fall through, and check int before float so
// "10" stays an int. Anything else stays a string.
func coerceXMLValue(raw string) any {
	lower := strings.ToLower(raw)
	switch lower {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}
