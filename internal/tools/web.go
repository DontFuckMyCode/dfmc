package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// userAgent identifies DFMC when fetching URLs. Real UA string so servers
// don't rate-limit us as a bot.
const userAgent = "DFMC/1.0 (+https://github.com/dontfuckmycode/dfmc)"

// httpClient is shared across web tools so connection pooling works.
var httpClient = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("stopped after 5 redirects")
		}
		return nil
	},
}

// WebFetchTool does an HTTP GET, converts HTML to text, and returns a
// token-budgeted excerpt. Not a full browser — skips JavaScript, follows up
// to 5 redirects.
type WebFetchTool struct{}

func NewWebFetchTool() *WebFetchTool     { return &WebFetchTool{} }
func (t *WebFetchTool) Name() string     { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch a URL and return its text content (HTML stripped)."
}

func (t *WebFetchTool) Execute(ctx context.Context, req Request) (Result, error) {
	raw := strings.TrimSpace(asString(req.Params, "url", ""))
	if raw == "" {
		return Result{}, fmt.Errorf("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || !(u.Scheme == "http" || u.Scheme == "https") {
		return Result{}, fmt.Errorf("url must be http(s)")
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

// HTML-to-text stripping. Regex-based because pulling in a full HTML parser
// is overkill for "give the model a readable excerpt". Drops <script>/<style>
// bodies, replaces tags with spaces, collapses whitespace, decodes common
// entities.
var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reNav    = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	reHeader = regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`)
	reFooter = regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reWS     = regexp.MustCompile(`[ \t]+`)
	reNL     = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reNav.ReplaceAllString(s, " ")
	s = reHeader.ReplaceAllString(s, " ")
	s = reFooter.ReplaceAllString(s, " ")
	// Convert block-level closers to newlines so structure survives.
	for _, tag := range []string{"p", "div", "li", "br", "h1", "h2", "h3", "h4", "h5", "tr"} {
		s = strings.ReplaceAll(s, "</"+tag+">", "\n")
		s = strings.ReplaceAll(s, "<"+tag+">", "\n")
		s = strings.ReplaceAll(s, "<"+tag+" ", "\n<"+tag+" ")
	}
	s = reTag.ReplaceAllString(s, "")
	s = decodeHTMLEntities(s)
	s = reWS.ReplaceAllString(s, " ")
	s = reNL.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func decodeHTMLEntities(s string) string {
	replacements := []struct{ from, to string }{
		{"&amp;", "&"}, {"&lt;", "<"}, {"&gt;", ">"},
		{"&quot;", `"`}, {"&#39;", "'"}, {"&apos;", "'"},
		{"&nbsp;", " "}, {"&ndash;", "–"}, {"&mdash;", "—"},
		{"&hellip;", "…"}, {"&laquo;", "«"}, {"&raquo;", "»"},
	}
	for _, r := range replacements {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	return s
}

// WebSearchTool queries DuckDuckGo's HTML endpoint (html.duckduckgo.com/html)
// and extracts result title/url/snippet triples. Zero API keys, zero JS.
// Not as rich as a real search API but useful for pointing the model at
// external resources.
type WebSearchTool struct{}

func NewWebSearchTool() *WebSearchTool    { return &WebSearchTool{} }
func (t *WebSearchTool) Name() string     { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the web (DuckDuckGo) and return top N title/url/snippet results."
}

var (
	reDDGResult = regexp.MustCompile(`(?is)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>.*?<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)
)

func (t *WebSearchTool) Execute(ctx context.Context, req Request) (Result, error) {
	query := strings.TrimSpace(asString(req.Params, "query", ""))
	if query == "" {
		return Result{}, fmt.Errorf("query is required")
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
	for i, m := range matches {
		if i >= limit {
			break
		}
		href := decodeDuckRedirect(m[1])
		title := stripTags(m[2])
		snippet := stripTags(m[3])
		results = append(results, map[string]any{
			"title":   title,
			"url":     href,
			"snippet": snippet,
		})
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s\n   %s", i+1, title, href, snippet))
	}
	output := strings.Join(lines, "\n\n")
	if output == "" {
		output = "(no results)"
	}
	return Result{
		Output: output,
		Data: map[string]any{
			"query":   query,
			"count":   len(results),
			"results": results,
		},
	}, nil
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

func stripTags(s string) string {
	s = reTag.ReplaceAllString(s, "")
	s = decodeHTMLEntities(s)
	s = reWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
