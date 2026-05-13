package tools

// web_search.go — WebSearchTool and the DuckDuckGo HTML-endpoint
// scraper. The tool queries https://html.duckduckgo.com/html and
// extracts {title, url, snippet} triples without an API key. Output is
// run through stripTags + htmlToText so the model sees clean text and
// each result URL is checked against the same private/loopback/link-
// local guard web_fetch's pre-flight uses, so a search result that
// resolves to an internal address never reaches the model.
//
// HTTP client + SSRF guard + WebFetchTool + htmlToText machinery live
// in web.go.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("search returned HTTP %d", resp.StatusCode)
	}

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
		if len(m) < 4 {
			continue
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
