# sc-xss — Cross-Site Scripting

**Date:** 2026-04-29
**Scope:** D:\Codebox\PROJECTS\DFMC
**Status:** NO ISSUES FOUND — workbench escapes correctly, no Go HTML template renders user data

## Verdict

The embedded workbench at [ui/web/static/index.html](../ui/web/static/index.html) is the only HTML surface DFMC produces. All five `innerHTML` writes either use static literals or wrap user-controlled content in a correct `escapeHTML()` helper. Server-side, no `html/template` or `text/template` is executed against attacker-controlled data — `handleIndex` writes the embedded HTML byte-for-byte.

## Verification

### 1. `escapeHTML` helper — correct character set

[ui/web/static/index.html:670-677](../ui/web/static/index.html):

```
function escapeHTML(value) {
    return String(value ?? "")
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#39;");
}
```

All five OWASP-recommended characters (`&`, `<`, `>`, `"`, `'`) are escaped, in the correct order (`&` first so subsequent entities aren't double-escaped). Safe for both element-text and attribute-value contexts under double or single quotes. **No bypass found.**

### 2. `innerHTML` audit — five sites

All five HTML-property writes in the workbench, with verdicts:

| Line | Source | User-controlled fragment | Escaped? | Verdict |
|---|---|---|---|---|
| [683](../ui/web/static/index.html) | `addMessage(role, content)` chat transcript | `role`, `content` (server-streamed) | `escapeHTML(role)`, `escapeHTML(content)` | SAFE |
| [844](../ui/web/static/index.html) | file-list reset (empty string) | none | n/a | SAFE |
| [881](../ui/web/static/index.html) | codemap reset (empty string) | none | n/a | SAFE |
| [886](../ui/web/static/index.html) | "CodeMap is still cold" empty-state | static literal | n/a | SAFE |
| [892-894](../ui/web/static/index.html) | codemap node card | `node.name`, `node.id`, `node.kind`, `node.path` | All wrapped in `escapeHTML(...)` | SAFE |

Empty-string assignment cannot inject. The other three writes either contain only static markup or pass every interpolated value through `escapeHTML()`. No raw user content reaches the HTML-property sink.

Other potential sinks searched (`outerHTML`, the legacy `document` write API, `insertAdjacentHTML`, the React-style raw-HTML prop, `eval`): **0 matches** in the workbench.

### 3. CSP backstop

[ui/web/server.go](../ui/web/server.go) `securityHeaders` sets `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'` on every response (per [architecture.md:325](architecture.md)). Even if a reflected XSS slipped past the JS escape, external scripts would be blocked. The workbench HTML itself ships with inline script and inline style blocks, so the CSP allows `'self'` only — meaning a JS injection that managed to land would still need a same-origin script source.

### 4. Server-side HTML rendering — none on user data

```
Pattern: html/template|text/template
Result (production code):
- internal/langintel/go_kb.go:236-237 — string literals in a Go knowledge-base
  entry that explains text/template's lack of auto-escaping. NOT a parser
  invocation; not even an import of the package.
```

[D:\Codebox\PROJECTS\DFMC\internal\promptlib\promptlib.go](../internal/promptlib/promptlib.go) (read in full) does NOT import `html/template` or `text/template`. Variable substitution uses a regex over `{{ name }}` placeholders against a `vars map[string]string` — no expression evaluator, no method invocation, no chained pipelines (see sc-ssti).

`handleIndex` in [ui/web/server.go:651-654](../ui/web/server.go) writes `[]byte(renderWorkbenchHTML())` which is the `//go:embed`-loaded literal HTML — there is no template execution, no interpolation, no user data spliced into the page.

### 5. JSON responses — `Content-Type: application/json`

All `/api/v1/*` JSON responses are written with `application/json` Content-Type via `writeJSON` and `application/x-ndjson` for SSE. With `X-Content-Type-Options: nosniff` set globally, browsers will not re-interpret these as HTML even if a payload contains markup-like text.

### 6. Tool / chat transcript content

LLM-generated text (chat messages, tool outputs) flows back through SSE to `addMessage` (line 683), which wraps the body in `<pre>escapeHTML(content)</pre>`. Because the chat content is rendered into a `<pre>` element with HTML-escaped text, neither markup injected by the model nor by an attacker controlling the LLM provider can execute as script.

## Bottom line

**No XSS findings.** The embedded workbench's five HTML-property writes are either static or correctly escaped. `escapeHTML` covers the full OWASP character set in the right order. Server-side HTML response is a static embedded asset — there is no Go template execution against user data anywhere in the codebase. CSP `default-src 'self'` is a defence-in-depth backstop.

## Recommendations (defensive, no current finding)

- Consider replacing the remaining HTML-property writes with `textContent` + `createElement` for the rare-touch paths (line 886) — purely cosmetic, removes the escape-helper dependency for static markup.
- The `<pre>` containing chat transcript benefits from `white-space: pre-wrap; word-break: break-word` (already in workbench CSS) — keep it; long unbreakable URLs in untrusted output otherwise overflow the layout.
