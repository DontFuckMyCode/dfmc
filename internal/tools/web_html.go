package tools

// web_html.go — HTML-to-text stripping and the small SSRF error
// classifier. Sibling of web.go which keeps the SSRF-guarded HTTP
// transport (safeTransport, httpClient, isBlockedHost,
// normalizeResolverHost) and the WebFetchTool entry point.
//
// Tokenizer-based via golang.org/x/net/html so nested / malformed
// input doesn't break extraction. The previous regex chain produced
// corrupted text on real-world pages where <script> bodies contained
// `>` characters in template literals, or where tag attributes
// embedded the same. The tokenizer handles all of that correctly.
//
// Strategy:
//   - Walk the token stream once.
//   - When entering <script>/<style>/<nav>/<header>/<footer>/<noscript>,
//     drop everything until the matching close — same intent as the old
//     code, but tracked via a depth counter so nested elements work.
//   - Block-level open/close tags emit a newline so paragraph structure
//     survives the strip.
//   - <br> emits a newline.
//   - Inline content is appended verbatim; the tokenizer has already
//     decoded HTML entities (&#8217;, &amp;, etc.).
//
// After tokenization, collapse runs of whitespace and limit blank-line
// runs to 2 — same final shape the old code produced.

import (
	"html"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

var (
	reWS = regexp.MustCompile(`[ \t]+`)
	reNL = regexp.MustCompile(`\n{3,}`)
)

// dropTags lists elements whose entire body is discarded — boilerplate
// chrome and non-content surfaces that just add noise to LLM input.
var dropTags = map[string]bool{
	"script":   true,
	"style":    true,
	"nav":      true,
	"header":   true,
	"footer":   true,
	"noscript": true,
	"svg":      true,
	"iframe":   true,
}

// blockTags emit a newline on open OR close so block-level structure
// survives the strip. Listed inline because a map lookup would dominate
// the cost of the tokenizer step on small pages.
var blockTags = map[string]bool{
	"p":          true,
	"div":        true,
	"li":         true,
	"h1":         true,
	"h2":         true,
	"h3":         true,
	"h4":         true,
	"h5":         true,
	"h6":         true,
	"tr":         true,
	"table":      true,
	"ul":         true,
	"ol":         true,
	"section":    true,
	"article":    true,
	"blockquote": true,
	"pre":        true,
}

func htmlToText(s string) string {
	z := xhtml.NewTokenizer(strings.NewReader(s))
	var out strings.Builder
	out.Grow(len(s))
	// drop tracks how deep we are inside a dropTags element. Skip text
	// emission while drop > 0; entering a nested drop element bumps it
	// (e.g. <noscript><script>...</script></noscript>).
	drop := 0
	// dropName is the tag name we're currently dropping for. Tracked so
	// we can decrement on the matching end-tag and not on an unrelated
	// inner end-tag (real-world HTML has plenty of tag soup).
	var dropStack []string
	for {
		tt := z.Next()
		switch tt {
		case xhtml.ErrorToken:
			return finalizeStrippedText(out.String())
		case xhtml.TextToken:
			if drop > 0 {
				continue
			}
			out.Write(z.Text())
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			if dropTags[tag] {
				if tt == xhtml.StartTagToken {
					drop++
					dropStack = append(dropStack, tag)
				}
				continue
			}
			if drop > 0 {
				continue
			}
			if tag == "br" {
				out.WriteByte('\n')
				continue
			}
			if blockTags[tag] {
				out.WriteByte('\n')
			}
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			if drop > 0 && len(dropStack) > 0 && dropStack[len(dropStack)-1] == tag {
				dropStack = dropStack[:len(dropStack)-1]
				drop--
				continue
			}
			if drop > 0 {
				continue
			}
			if blockTags[tag] {
				out.WriteByte('\n')
			}
		case xhtml.CommentToken, xhtml.DoctypeToken:
			// silently dropped — neither carries useful content.
		}
	}
}

func isSSRFFetchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "ssrf guard") || strings.Contains(msg, "blocked ip")
}

// finalizeStrippedText collapses runs of whitespace and caps blank-line
// runs at 2, matching the old shape. Lives separately so unit tests can
// hit it directly without driving the whole tokenizer.
func finalizeStrippedText(s string) string {
	s = reWS.ReplaceAllString(s, " ")
	s = reNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// decodeHTMLEntities was the entity-decoder used by the regex stripper.
// The tokenizer in htmlToText now decodes entities natively, so this
// helper is unused — kept commented as a breadcrumb for anyone hunting
// for the old surface.
// func decodeHTMLEntities(s string) string { return html.UnescapeString(s) }
var _ = html.UnescapeString // keep the html import live for future use
