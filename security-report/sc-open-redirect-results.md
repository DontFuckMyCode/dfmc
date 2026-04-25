# Open Redirect Results — DFMC

Scope: any HTTP handler that emits a `Location` header / `http.Redirect`
based on user input, OAuth callbacks, and tangentially `web_fetch`'s
final-URL exposure.

## Counts per file

| File | Findings |
|---|---|
| `D:/Codebox/PROJECTS/DFMC/ui/web/*.go` | 0 (positive) |
| `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go` | 1 (info — final URL exposure to LLM) |

---

## OREDIR-001 — No HTTP redirect handlers in the web server (Informational, positive)

- **Severity**: Info (positive finding)
- **Confidence**: High
- **Verification**:
  - `Grep "http\.Redirect|w\.Header\(\)\.Set\(\"Location\""` across the
    entire repo returned **no matches**.
  - `Grep "Set\(\"Location\""` across the entire repo returned **no
    matches**.
  - All 53 routes in `setupRoutes` (`ui/web/server.go:188-257`) verified
    to return JSON (via `writeJSON`) or SSE (via `writeSSE`) or HTML
    (handleIndex serves the embedded workbench HTML directly). None
    issue a 30x.
- **CWE**: N/A (positive)

The web server does not have an open-redirect surface. There are no
`return_to`, `next`, `redirect_uri`, or `?url=` query-parameter handlers
because DFMC does not have user authentication beyond bearer tokens —
the entire OAuth/SSO flow that typically introduces open-redirect bugs
is absent.

## OREDIR-002 — No OAuth callback handlers (Informational, positive)

- **Severity**: Info (positive)
- **Confidence**: High
- **Verification**:
  - `Grep "oauth|callback|authorize"` against `ui/web/` and `ui/cli/`
    returned no application-code matches (only test fixtures and
    documentation strings).
  - Bearer-token middleware
    (`D:/Codebox/PROJECTS/DFMC/ui/web/server.go:401-419`) is the only
    auth path; it returns 401 JSON, never a redirect.
- **CWE**: N/A (positive)

DFMC does not authenticate via OAuth — confirmed by the architecture
report and verified directly. So the OAuth-callback open-redirect class
does not apply.

## OREDIR-003 — `web_fetch` exposes final URL to the LLM after redirect chain (Informational, low-impact)

- **Severity**: Info / Low
- **Confidence**: High
- **File:line**:
  - Final URL exposure: `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:171`
    (`Data["url"]: u.String()`).
  - Redirect chain: `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:54-59`
    (5-redirect cap).
- **CWE**: CWE-601 (informational — not a true open-redirect bug, but
  worth noting for prompt-injection sensitivity).

`web_fetch`'s `Result.Data` map exposes:

```go
Data: map[string]any{
    "url":          u.String(),  // line 171 — REQUEST URL, not final
    "status":       resp.StatusCode,
    ...
}
```

Reading this carefully: `u` is the parsed REQUEST url (line 113), not
`resp.Request.URL`. So if the request 302'd to a different host, the
returned `url` field is the ORIGINAL caller-supplied URL, NOT the final
URL after redirects.

This is actually **safer** for an open-redirect-style concern (the LLM
sees what the user asked for, not what the server forwarded to), but
it's also **slightly misleading** — if a user posts `https://t.co/xxx`
and the response body is from `https://attacker.example/...`, the model
sees `url: https://t.co/xxx` and the body content together, with no
indication that the body came from a different host. A prompt-injection
payload in the redirected body would land alongside the trusted-looking
original URL.

This isn't an open-redirect vuln per se — there's no DFMC HTTP endpoint
emitting a Location header. It's a provenance-tracking gap on the
fetched content. Out-of-scope for the core open-redirect playbook;
flagged because the playbook brief asked for it.

**Recommendation (optional)**: include both `requested_url` and
`final_url` (`resp.Request.URL.String()`) in the `Data` map so a
follow-up tool call can see whether redirect-laundering happened.

---

## Summary

Zero classical open-redirect findings. DFMC's HTTP surface is all
JSON-API + SSE + embedded HTML; no `Location` header is ever set, no
OAuth flow exists, no `?next=` or `?return_to=` query params are
honoured. The closest "redirect handling" in the codebase is the
`web_fetch` 5-hop follow loop in the outbound HTTP client, which is
guarded by `safeTransport.DialContext` (see SSRF-002).

The single Info-grade item (OREDIR-003) is about provenance tracking
of the redirect chain in tool output, not an open-redirect vuln.
