// Shared HTTP client for LLM providers.
//
// The providers used to set `http.Client.Timeout = 60s`, which caps the
// ENTIRE request lifecycle — headers, body, everything. Two problems:
//
//  1. 60s is not enough for non-streaming completions. A long tool-use
//     round trip against a slower model (observed: z.ai / GLM-4) can
//     legitimately take 90–120s to produce headers, and the caller
//     saw "Client.Timeout exceeded while awaiting headers".
//  2. For streaming (SSE) it silently truncates responses past 60s.
//     Client.Timeout covers body reads too.
//
// The fix: push the timeout onto Transport.ResponseHeaderTimeout. That
// bounds ONLY the time-to-first-byte; once the provider starts writing
// the response, body reads run under the caller's ctx with no artificial
// ceiling. 180s of header-wait headroom is generous but still catches a
// truly dead endpoint.

package provider

import (
	"net"
	"net/http"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// defaultResponseHeaderTimeout is the ceiling we give a provider to send
// back HTTP response headers after we issue the request. Complex tool-
// calling requests against slow inference backends can push this past a
// minute, so 180s is the empirical floor that avoids false "timeout
// while awaiting headers" under normal conditions. Once headers arrive,
// body reads use the caller's context — callers cancel by cancelling
// their own ctx.
const defaultResponseHeaderTimeout = 180 * time.Second

// newProviderHTTPClient returns an http.Client tuned for LLM endpoints.
// Reuses a single Transport so connections get pooled across calls; no
// Client.Timeout so streaming body reads aren't silently truncated.
// Callers still bound total call duration via the request context when
// they need to — e.g. router-level retry loops pass a WithTimeout ctx.
//
// SSRF guard: the dialer is wrapped with security.WrapDialWithSSRFGuard
// unless `endpoint` itself is a loopback URL (operator opt-in for local
// inference servers like Ollama or on-prem mirrors). Without this guard
// any provider whose endpoint got mistyped — or maliciously rewritten
// in a config file — would happily dial cloud-metadata IPs
// (169.254.169.254) or internal services and exfiltrate the response
// in the LLM body. Provider endpoints are operator-trusted, but
// defense-in-depth on the transport that carries every API key in the
// request headers is worth the ~one-syscall cost.
//
// responseHeaderTimeout is the ResponseHeaderTimeout on the transport;
// it bounds time-to-first-byte only (not total body read time).
// Pass 0 to use the default (180s).
func newProviderHTTPClient(responseHeaderTimeout time.Duration, endpoint string) *http.Client {
	if responseHeaderTimeout == 0 {
		responseHeaderTimeout = defaultResponseHeaderTimeout
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	dialContext := dialer.DialContext
	if !security.EndpointIsLoopback(endpoint) {
		dialContext = security.WrapDialWithSSRFGuard(dialContext)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: responseHeaderTimeout,
		},
	}
}
