# sc-clickjacking — UI Redress / Frame Embedding Results

**Target:** DFMC `dfmc serve` workbench HTML + `/api/v1/*` JSON
responses
**Skill:** sc-clickjacking
**Counts:** Critical: 0 | High: 0 | Medium: 1 | Low: 2 | Info: 2 | Total: 5

---

## Summary

The workbench is reasonably hardened against framing-based UI redress:

- `X-Frame-Options: DENY` is set on **every response** by
  `securityHeaders` middleware (`ui/web/server.go:122-129`); pinned
  by `server_http_test.go:85+`.
- `Content-Security-Policy: default-src 'self'; script-src 'self';
  style-src 'self'` blocks third-party iframes from including the
  workbench as a sub-resource and (more importantly) blocks the
  workbench from being framed because `default-src 'self'`
  implicitly disallows being embedded under any non-self origin
  via the legacy frame-src interpretation, though this is XFO's
  job in practice.
- The embedded HTML (`ui/web/static/index.html`, 1777 lines) does
  **not** contain `frame-ancestors` in a `<meta http-equiv>` tag —
  but that's redundant given the response-header XFO+CSP.

The remaining concerns are minor and relate to (a) absence of the
modern equivalent (`Content-Security-Policy: frame-ancestors 'none'`)
which supersedes XFO and is preferred by some CSP linters, and (b)
the existence of sensitive UI controls that would matter if framing
were ever permitted (e.g. hooks editor, drive start/stop, drive
delete buttons).

The sensitive UI inventory in `ui/web/static/index.html`:

- **Drive start** (`drive-start` button, line 639) — POSTs to
  `/api/v1/drive` with the freeform task textarea content.
- **Drive stop** (line 1695) — POST `/api/v1/drive/{id}/stop`.
- **Drive resume** (line 1707) — POST
  `/api/v1/drive/{id}/resume`.
- **Drive delete** (line 1720) — DELETE `/api/v1/drive/{id}`.
- **Workspace apply** (line 1048) — POST
  `/api/v1/workspace/apply` (writes diff to project files).
- No explicit "tool approval" button visible in the embedded HTML —
  the deny-by-default web approver is process-level
  (`DFMC_APPROVE` env), not a per-call modal in the workbench.
  So clickjacking against an approval prompt is moot here.

---

## Findings

### CLICK-001 — `frame-ancestors` directive missing from CSP (XFO-only protection)
- **Severity:** Low
- **Confidence:** High
- **CWE:** CWE-1021 (Improper Restriction of Rendered UI Layers)
- **File:** `ui/web/server.go:122-129`.
- **Detail:** CSP is set to
  `default-src 'self'; script-src 'self'; style-src 'self'`
  — no `frame-ancestors` directive. The framing protection is
  carried entirely by `X-Frame-Options: DENY`. Modern browsers
  honour XFO, but the CSP3 spec deprecates it in favour of
  `frame-ancestors`, and some embedded contexts (CSP3-only browsers,
  hardened policies) ignore XFO.
- **Impact:** None today (every major browser respects
  `X-Frame-Options: DENY`). Defense-in-depth gap only.
- **Remediation:** Add `frame-ancestors 'none'` to the CSP string:
  `default-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'`.

### CLICK-002 — No `<meta http-equiv="X-Frame-Options">` fallback in embedded HTML
- **Severity:** Info
- **Confidence:** High
- **CWE:** N/A
- **File:** `ui/web/static/index.html` (verified `<head>` block,
  lines 1-30; no XFO/CSP meta tag).
- **Detail:** XFO is only effective when sent as an HTTP response
  header (the meta-tag form is a non-standard, unsupported variant
  per HTML spec). The header is set by middleware, so this is fine.
  Recorded as informational — confirms there is no defense-in-depth
  meta-tag, but none is needed.

### CLICK-003 — Drive start / stop / delete buttons would be high-impact targets if framing were permitted
- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-1021 (theoretical)
- **File:** `ui/web/static/index.html:639-644, 1695, 1707, 1720`.
- **Detail:** The Drive Cockpit panel exposes:
  - Start (kicks off LLM-driven autonomous run, costs API tokens)
  - Stop (cancels in-flight run)
  - Resume (re-enters parked run)
  - Delete (permanently drops a Drive run record)
  These would be high-value clickjacking targets — silent click
  on "Delete" wipes a run; silent click on "Start" with a
  pre-populated textarea kicks off arbitrary work. **Today,
  these are protected by `X-Frame-Options: DENY` on every
  response.** The risk is that any future relaxation of XFO
  (e.g. allowing an embed for an IDE host) would expose all four
  buttons simultaneously without per-action confirmation.
- **Remediation:** Add a confirm-modal for `Delete`. Keep XFO
  globally-DENY; if iframe support is ever needed, gate it
  behind an explicit allowlist of frame ancestors and re-evaluate
  per-button confirmation.

### CLICK-004 — Sensitive workbench inputs (drive task, patch apply) have no second-step confirmation
- **Severity:** Low
- **Confidence:** Medium
- **CWE:** CWE-1021 (defense-in-depth gap)
- **File:** `ui/web/static/index.html:635-644` (drive task box +
  start button), `:1048-1080` (workspace apply).
- **Detail:** Both flows are single-click → POST. No dual-step
  confirm-then-execute, no drag-confirm, no captcha. Behind
  XFO+CSP this is fine for clickjacking, but it makes any future
  framing weakness an immediate file-write or LLM-cost incident.
- **Remediation:** Optional — a confirm dialog for the patch-
  apply path (the most destructive action) would harden against
  any future regression in framing protection.

### CLICK-005 — `/api/v1/*` JSON endpoints inherit XFO/CSP via middleware (informational)
- **Severity:** Info
- **Confidence:** High
- **CWE:** N/A
- **File:** `ui/web/server.go:273-283` (Handler chain — every
  response goes through `securityHeaders`).
- **Detail:** Confirmed by inspection: the middleware order is
  `mux → limitRequestBodySize → securityHeaders → rateLimit →
  bearerToken`, so JSON endpoints also carry `X-Frame-Options:
  DENY`. This is technically over-broad (a JSON response cannot
  be framed in a meaningful way) but harmless and consistent.

---

## Notable Finding

The clickjacking surface is **already adequately defended** by
`X-Frame-Options: DENY` set on every response (including JSON
endpoints) by `securityHeaders` middleware. The two improvements
worth making are: **(1)** add `frame-ancestors 'none'` to the CSP
for CSP3-only browsers / future-proofing, and **(2)** add a
two-step confirm for `POST /api/v1/workspace/apply` from the
workbench (CLICK-003 + CLICK-004), which is the single most
destructive click in the UI and currently fires on the first
click. Neither is exploitable today.
