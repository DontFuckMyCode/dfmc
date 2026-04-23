// Chat composer input parsers: tokenization and slash-command splitting.
// Extracted from tui.go — pure string-level helpers with no Model
// dependency. Everything the composer needs to turn user text into
// (command, args, rawArgs) tuples lives here.

package tui

import (
	"fmt"
	"strings"
)

// formatRunArgList walks the args string token-by-token and re-quotes any
// whitespace-bearing piece using formatSlashArgToken. Bare alphanumeric
// flags like `-m` pass through untouched. Pre-fix the args string was
// concatenated raw, so any quoted argument the underlying tool spec
// contained (e.g. `git commit -m "fix"`) would be re-tokenized by the
// slash dispatcher and lose its quoting — the H3 review caught this.
func formatRunArgList(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	tokens, err := splitRespectingQuotes(args)
	if err != nil {
		return formatSlashArgToken(args)
	}
	formatted := make([]string, len(tokens))
	for i, tok := range tokens {
		formatted[i] = formatSlashArgToken(tok)
	}
	return strings.Join(formatted, " ")
}

// splitRespectingQuotes splits on whitespace but keeps quoted segments
// (single or double) atomic. Backslash escapes the next char inside the
// quote. Tokens are returned without surrounding quotes; the formatter
// re-applies them only when the token contains whitespace.
func splitRespectingQuotes(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
				continue
			}
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == ' ' || c == '\t' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(c)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted value")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

func parseChatCommandInput(raw string) (string, []string, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return "", nil, "", nil
	}
	body := strings.TrimSpace(strings.TrimPrefix(raw, "/"))
	if body == "" {
		return "", nil, "", nil
	}
	head, tail, err := splitFirstTokenAndTail(body)
	if err != nil {
		return "", nil, "", err
	}
	cmd := strings.ToLower(strings.TrimSpace(head))
	rawArgs := strings.TrimSpace(tail)
	if rawArgs == "" {
		return cmd, nil, "", nil
	}
	args, err := splitToolParamTokens(rawArgs)
	if err != nil {
		return "", nil, "", err
	}
	return cmd, args, rawArgs, nil
}

func splitFirstTokenAndTail(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	quote := rune(0)
	splitAt := -1
	for i, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			splitAt = i
			goto done
		}
	}
done:
	if quote != 0 {
		return "", "", fmt.Errorf("unterminated quoted value")
	}
	headRaw := raw
	tail := ""
	if splitAt >= 0 {
		headRaw = raw[:splitAt]
		tail = strings.TrimSpace(raw[splitAt:])
	}
	parts, err := splitToolParamTokens(headRaw)
	if err != nil {
		return "", "", err
	}
	head := ""
	if len(parts) > 0 {
		head = strings.TrimSpace(parts[0])
	}
	return head, tail, nil
}
