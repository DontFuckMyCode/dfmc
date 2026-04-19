package tools

import (
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

// userAgent identifies DFMC when fetching URLs. Real UA string so servers
// don't rate-limit us as a bot.
const userAgent = "DFMC/1.0 (+https://github.com/dontfuckmycode/dfmc)"

// safeTransport dials with an IP-level SSRF guard. The resolved IP is
// checked at connect time (not before), closing the DNS rebinding window.
var safeTransport = &http.Transport{
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", addr, err)
		}
		resolverHost := normalizeResolverHost(host)
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, resolverHost)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", resolverHost, err)
		}
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
				return nil, fmt.Errorf("blocked IP for %q: %s (SSRF guard)", resolverHost, ip.IP)
			}
		}
		for _, ip := range ips {
			conn, err := net.DialTimeout(network, net.JoinHostPort(ip.IP.String(), port), 10*time.Second)
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("no reachable IP for %q", host)
	},
}

// httpClient is shared across web tools so connection pooling works.
var httpClient = &http.Client{
	Timeout:   20 * time.Second,
	Transport: safeTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("stopped after 5 redirects")
		}
		return nil
	},
}

// isBlockedHost is a best-effort resolver used only for filtering search
// results before they are shown to the model. Unlike web_fetch, this is not
// a security boundary by itself — the actual SSRF guard lives in
// safeTransport.DialContext at connect time.
func isBlockedHost(host string) bool {
	h := host
	if strings.Contains(h, ":") {
		h, _, _ = net.SplitHostPort(h)
	}
	h = normalizeResolverHost(h)
	ips, err := net.LookupIP(h)
	if err != nil {
		return true
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
			return true
		}
	}
	return false
}

func normalizeResolverHost(host string) string {
	host = strings.TrimSpace(host)
	if zoneIdx := strings.LastIndex(host, "%"); zoneIdx > 0 {
		base := host[:zoneIdx]
		if ip := net.ParseIP(base); ip != nil {
			return base
		}
	}
	return host
}

// WebFetchTool does an HTTP GET, converts HTML to text, and returns a
// token-budgeted excerpt. Not a full browser — skips JavaScript, follows up
// to 5 redirects.
type WebFetchTool struct{}

func NewWebFetchTool() *WebFetchTool { return &WebFetchTool{} }
func (t *WebFetchTool) Name() string { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch a URL and return its text content (HTML stripped)."
}

func (t *WebFetchTool) Execute(ctx context.Context, req Request) (Result, error) {
	raw := strings.TrimSpace(asString(req.Params, "url", ""))
	if raw == "" {
		return Result{}, missingParamError("web_fetch", "url", req.Params,
			`{"url":"https://example.com/docs/api"} or {"url":"https://...","max_bytes":524288}`,
			`url must be a full http(s) URL. Returns text content with HTML stripped. Loopback / private / link-local addresses are blocked (SSRF guard). Cap raw bytes with max_bytes (128KB-1MB).`)
	}
	u, err := url.Parse(raw)
	if err != nil || !(u.Scheme == "http" || u.Scheme == "https") {
		scheme := ""
		if u != nil {
			scheme = u.Scheme
		}
		return Result{}, fmt.Errorf(
			"web_fetch: url must be a full http(s) URL, got %q (scheme=%q). "+
				"Use the absolute form: %s. "+
				"file://, ftp://, data:, javascript:, and bare hostnames without a scheme are rejected.",
			raw, scheme,
			`{"name":"web_fetch","args":{"url":"https://example.com/path"}}`)
	}
	maxBytes := asInt(req.Params, "max_bytes", 128*1024)
	if maxBytes <= 0 {
		maxBytes = 128 * 1024
	}
	if maxBytes > 1024*1024 {
		maxBytes = 1024 * 1024
	}
	rawOut := asBool(req.Params, "raw", false)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,*/*;q=0.5")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.8")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if isSSRFFetchError(err) {
			return Result{}, fmt.Errorf("url resolves to a blocked (private/loopback/link-local) address — SSRF protection")
		}
		return Result{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, int64(maxBytes)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Result{}, fmt.Errorf("read body: %w", err)
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	body := string(data)
	contentType := resp.Header.Get("Content-Type")

	output := body
	if !rawOut && strings.Contains(strings.ToLower(contentType), "html") {
		output = htmlToText(body)
	}

	return Result{
		Output: output,
		Data: map[string]any{
			"url":          u.String(),
			"status":       resp.StatusCode,
			"content_type": contentType,
			"bytes":        len(data),
			"truncated":    truncated,
		},
		Truncated: truncated,
	}, nil
}

// HTML-to-text stripping. Tokenizer-based via golang.org/x/net/html so
// nested / malformed input doesn't break extraction. The previous regex
// chain produced corrupted text on real-world pages where <script> bodies
// contained `>` characters in template literals, or where tag attributes
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

// WebSearchTool queries DuckDuckGo's HTML endpoint (html.duckduckgo.com/html)
// and extracts result title/url/snippet triples. Zero API keys, zero JS.
// Not as rich as a real search API but useful for pointing the model at
// external resources.
type WebSearchTool struct{}

func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }
func (t *WebSearchTool) Name() string  { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the web (DuckDuckGo) and return top N title/url/snippet results."
}

var (
	reDDGResult = regexp.MustCompile(`(?is)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>.*?<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
)

func (t *WebSearchTool) Execute(ctx context.Context, req Request) (Result, error) {
	query := strings.TrimSpace(asString(req.Params, "query", ""))
	if query == "" {
		return Result{}, missingParamError("web_search", "query", req.Params,
			`{"query":"go context cancellation pattern"} or {"query":"...","limit":5}`,
			`query is the search string sent to DuckDuckGo HTML. Returns up to limit results (1-25, default 8) with title/snippet/url. For docs you already have a URL for, use web_fetch directly.`)
	}
	limit := asInt(req.Params, "limit", 8)
	if limit <= 0 {
		limit = 8
	}
	if limit > 25 {
		limit = 25
	}

	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	if isBlockedHost(httpReq.URL.Host) {
		return Result{}, fmt.Errorf("url resolves to a blocked (private/loopback/link-local) address — SSRF protection")
	}
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("Accept", "text/html")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return Result{}, fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return Result{}, err
	}
	body := string(data)

	matches := reDDGResult.FindAllStringSubmatch(body, -1)
	results := make([]map[string]any, 0, limit)
	var lines []string
	filtered := 0
	for _, m := range matches {
		if len(results) >= limit {
			break
		}
		href := decodeDuckRedirect(m[1])
		// L3: filter result URLs that resolve to private/loopback IPs.
		// web_fetch's safeTransport already prevents the actual SSRF, but
		// emitting the URL into the model's context lets a confused
		// follow-up step (e.g. user asks "open the third result") still
		// reach an internal resource via copy-paste outside the tool
		// guard. Drop the result before it becomes an attractive nuisance.
		if isResultURLBlocked(href) {
			filtered++
			continue
		}
		title := stripTags(m[2])
		snippet := stripTags(m[3])
		results = append(results, map[string]any{
			"title":   title,
			"url":     href,
			"snippet": snippet,
		})
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s\n   %s", len(results), title, href, snippet))
	}
	output := strings.Join(lines, "\n\n")
	if output == "" {
		output = "(no results)"
	}
	resultData := map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
	}
	if filtered > 0 {
		resultData["filtered_blocked_urls"] = filtered
	}
	return Result{Output: output, Data: resultData}, nil
}

// isResultURLBlocked tests whether a search-result URL points at a
// host we don't want surfaced to the model. Best-effort: parses the
// URL, then runs the same private/loopback/link-local check used by
// web_fetch's pre-flight guard. Bare strings that don't parse are
// rejected to avoid emitting suspicious junk.
func isResultURLBlocked(href string) bool {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil || u == nil {
		return true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		// non-http(s) results aren't directly fetchable by web_fetch
		// anyway; emitting them just adds clutter.
		return true
	}
	if u.Host == "" {
		return true
	}
	return isBlockedHost(u.Host)
}

// DuckDuckGo's HTML results go through an /l/?uddg= redirect wrapper. Unwrap
// it so the model sees the real target URL.
func decodeDuckRedirect(href string) string {
	if !strings.Contains(href, "uddg=") {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if u.Path != "/l/" {
		return href
	}
	if target := u.Query().Get("uddg"); target != "" {
		if decoded, err := url.QueryUnescape(target); err == nil {
			return decoded
		}
	}
	return href
}

// stripTags removes HTML markup from a single fragment (used by the
// DuckDuckGo result extractor on title/snippet snippets). Reuses the
// tokenizer-based htmlToText so nested/malformed input is handled the
// same way as the full web_fetch pipeline.
func stripTags(s string) string {
	return htmlToText(s)
}
