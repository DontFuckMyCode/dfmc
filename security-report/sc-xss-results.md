# sc-xss — Cross-Site Scripting & Terminal Injection Findings

**Target**: DFMC (`D:\Codebox\PROJECTS\DFMC`)
**Skill**: sc-xss (browser XSS + TUI ANSI/terminal injection variant)
**CWE**: primarily CWE-79 (Improper Neutralization of Input During Web Page
Generation), CWE-150 (Improper Neutralization of Escape Sequences) for the TUI
findings.

---

## Counts

| Severity | Count |
|---|---|
| High     | 1 |
| Medium   | 4 |
| Low      | 1 |
| Info     | 1 |
| **Total**| **7** |

Confidence column: H = high, M = medium, L = low.

---

## Surface summary

The DFMC web workbench at `ui/web/static/index.html` is a single embedded SPA
that renders LLM streaming output, tool-call activity, drive-run TODOs,
codemap nodes, and conversation transcripts. Most rendering is correctly
defensive (`textContent`, `createElement`+`appendChild`). The few `.innerHTML`
sites pass values through an `escapeHTML` helper that escapes `&<>` only —
**not quote characters** — which is correct for *element body* contexts but
unsafe inside *HTML attribute values*. One SVG-builder uses such a value in
a quoted `title=` attribute (XSS-001). The file-list endpoint and SSE event
stream paths use `textContent`, no markdown rendering, no `dangerouslySetInnerHTML`,
no `eval`, no `document.write`, no `new Function`. Server-side, all HTTP
error responses go through `writeJSON` (Content-Type `application/json` plus
`X-Content-Type-Options: nosniff` from the security-headers middleware), so
echoed user values cannot be MIME-sniffed into HTML by browsers — reflected
XSS via JSON error bodies is not exploitable.

The TUI side has a separate problem class: **ANSI / terminal-control
injection** (CWE-150). Tool stdout (notably `run_command`, `web_fetch`) is
captured raw and the engine's `compactToolPayload` truncates it to one
line/180 chars but does **not** strip `\x1b` (ESC), `\x07` (BEL), or other
control bytes before publishing it as `output_preview` for chip rendering.
Bubbletea+lipgloss render those bytes verbatim through the user's terminal.
A hostile remote URL fetched by `web_fetch`, or a hostile `run_command`
target, can therefore inject:

- OSC 0/2 — change terminal title (`\x1b]0;evil\x07`)
- OSC 8 — embed clickable hyperlinks pointing anywhere
  (`\x1b]8;;http://evil/\x1b\\spoofed text\x1b]8;;\x1b\\`)
- CSI cursor-move / clear-screen — overwrite earlier transcript lines
- BEL flooding (`\x07`)

Bubbletea's own UI bytes (alt-screen, mouse mode, cursor) are emitted by the
framework, but content the framework *prints* via lipgloss `Render` is not
sanitized for nested escapes. There are tests that strip ANSI from rendered
output (`ui/tui/table_render_test.go:293 stripANSI`) — proof the team is
aware of ANSI semantics — but no production-side stripping of *incoming*
tool output before it becomes an InnerLine / Verb / Reason / Preview.

The separate concern of the LLM itself producing ANSI in its assistant text
also flows through the same path: assistant message bodies render through
`addMessage`/`pre` (web — escaped) and through lipgloss-rendered chat blocks
(TUI — *not* control-byte-stripped). The web path is safe; the TUI path is
not.

---

## Findings

### XSS-001 — `escapeHTML` does not escape quotes; SVG `title=` attribute is interpolated

- **File:line**: `ui/web/static/index.html:676-681` (`escapeHTML` definition);
  `ui/web/static/index.html:983-984` (use site).
- **Severity**: Medium
- **Confidence**: M
- **CWE**: CWE-79

The helper:

```js
function escapeHTML(value) {
    return String(value ?? "")
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
}
```

does not escape `"`, `'`, or `` ` ``. It is then used in an attribute
context inside the codemap SVG renderer:

```js
const title = escapeHTML((n.name || n.id || "") + (n.kind ? (" (" + n.kind + ")") : ""));
svgContent += `<circle ... title="${title}"/>`;
```

`n.name` originates from the codemap JSON returned by
`GET /api/v1/codemap`, which the server builds from AST-extracted symbol
names (`internal/codemap/`). Symbol names are extracted from project source
files in the workspace. A repository whose code contains a function or type
name with an embedded `"` (legal in some grammars and definitely possible in
languages DFMC's regex fallback parses) breaks out of the attribute. Since
`escapeHTML` already neutralised `<`, the attacker cannot directly inject a
new tag, but they can inject additional attributes such as
`onmouseover=...` because the value is interpolated **before** the rest of
the tag is serialised — `title="x" onmouseover="alert(1)" foo="`.

This SVG is then committed to the DOM via `svg.outerHTML = svgContent`
(line 990), so injected event-handler attributes execute. CSP
`script-src 'self'` does **not** block inline event handlers (handlers in
markup are governed by `script-src 'unsafe-inline'` / `style-src` and most
critically by `unsafe-hashes` for inline events) — the DFMC CSP has none of
those, but Chrome enforces script-src on inline event handlers only when
`'unsafe-inline'` is *absent* from `script-src`, which is the case here, so
the handler should be blocked. **However** the attacker can still:

1. Inject `style="..."` (CSP does not gate inline styles in this build the
   way it gates scripts; `style-src 'self'` does block inline styles in
   modern browsers, partially mitigating this) — note `style-src 'self'` is
   set at `server.go:122-129`, so inline `style=` is also blocked.
2. Inject a malformed attribute that breaks the SVG and corrupts the DOM
   downstream — DoS / UI confusion only.

So the **practical** exploitability is bounded by CSP; this is graded
Medium because (a) the unsafe pattern would be exploitable in any
deployment that loosens CSP, and (b) the `escapeHTML` helper is exported
for use in **any** future innerHTML site, where a non-attribute quote-bug
might land.

**Recommended fix**: extend `escapeHTML` to also replace `"` → `&quot;` and
`'` → `&#39;`. Equivalently, set the title via `setAttribute` on a real
node rather than string-concatenating SVG markup.

---

### XSS-002 — `innerHTML` with `escapeHTML` for codemap hotspot rows (defence-in-depth)

- **File:line**: `ui/web/static/index.html:894-898` and `1021-1025`
- **Severity**: Low
- **Confidence**: H
- **CWE**: CWE-79

Same `escapeHTML` is used to build hotspot rows:

```js
el.innerHTML = '<strong>' + escapeHTML(node.name || node.id || "node") + '</strong><span>' +
    escapeHTML((node.kind || "node") + (node.path ? " • " + node.path : "")) +
    '</span>';
```

These are *element-body* contexts, so `&<>` escaping is sufficient and the
sites are safe today. Flagged as Low because the pattern is fragile: any
future edit that adds an attribute interpolation here without first fixing
`escapeHTML` (XSS-001) would be exploitable. Recommended to migrate to
`createElement`+`textContent` for parity with the rest of the file (the
Drive panel does this consistently from line 1407 onwards).

---

### XSS-003 — `outerHTML` overwrite with attacker-influenced SVG string

- **File:line**: `ui/web/static/index.html:990` (also lines 968-989 building
  `svgContent`)
- **Severity**: Medium
- **Confidence**: M
- **CWE**: CWE-79

`svg.outerHTML = svgContent` replaces a node with a string assembled by
template literals around codemap data. As detailed in XSS-001, the only
non-static input is the symbol `name` field, escaped only for `&<>`. The
SVG is otherwise safe (numeric coordinates from server arithmetic, tag
shapes from constants). The risk is concentrated in the title attribute
and is the same finding as XSS-001; logged separately because the sink
(`outerHTML`) is the actual delivery mechanism and is the line that should
be re-read whenever the SVG builder is touched.

---

### XSS-004 — TUI tool-output ANSI / OSC injection (terminal injection)

- **File:line**:
  `internal/engine/agent_loop_events.go:139` (publish);
  `internal/engine/agent_loop.go:376-389` (`compactToolPayload` — does not
  strip control bytes);
  `ui/tui/engine_events_tool.go:69` and `ui/tui/engine_events_tool.go:122`
  (consume `output_preview` into `chip.Preview`);
  `ui/tui/theme/tool_chips.go:62-115` (lipgloss rendering of preview /
  innerLines).
- **Severity**: High
- **Confidence**: H
- **CWE**: CWE-150 (improper neutralization of escape sequences); also
  consider CWE-79 family for the "untrusted output rendered to a UI
  control" framing.

Tool output flows from the tool implementation back into the engine in
`agent_loop_events.go`, where the engine emits the `tool:result` event with:

```go
"output_preview": compactToolPayload(outputText, 180),
```

`compactToolPayload`:

```go
func compactToolPayload(raw string, maxChars int) string {
    text := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
    if text == "" { return "" }
    if idx := strings.IndexByte(text, '\n'); idx >= 0 { ... text = first + " ..." }
    return truncateRunesWithMarker(text, maxChars, "...")
}
```

This collapses to the first line, trims whitespace, truncates — but does
not remove `\x1b`, `\x07`, `\x9b` (CSI), or any C1/C0 control bytes other
than what `TrimSpace` strips. The TUI consumer reads the field unchanged
and writes it through lipgloss. lipgloss styles do not neutralise embedded
ANSI inside the styled string — they prepend SGR codes around content,
preserving any escape sequences inside it.

Concrete attack paths:

1. **Hostile `web_fetch`** — `internal/tools/web.go:161-167` returns the
   HTML body text after a tokenizer pass. The tokenizer decodes HTML
   entities but does not filter raw bytes inside text nodes. A page that
   includes raw `\x1b[2J\x1b[H` in a paragraph clears the user's terminal;
   `\x1b]0;You have been pwned\x07` rewrites their window title;
   `\x1b]8;;http://evil/\x1b\\Click for documentation\x1b]8;;\x1b\\`
   embeds a hyperlink pointing to an attacker URL while displaying
   plausible-looking "documentation" text (OSC 8). Many terminals
   (kitty, iTerm2, Windows Terminal recent builds, WezTerm) honour OSC 8.
2. **`run_command`** — although DFMC blocks `sudo`/shell-interpreters,
   it allows e.g. `cat`, `echo`, `printf`. A repository checked out by a
   user could contain a file whose content is pure ANSI; running
   `cat poisoned-file` returns those bytes as stdout, which the TUI
   prints in the chip preview.
3. **External MCP server** — `internal/mcp/client.go` imports tools from
   user-configured external servers. The MCP server decides what its
   `tools/call` results contain. A hostile server can poison previews.
4. **LLM-emitted tool args / reasoning** — assistants can be prompt-
   injected via earlier tool results to embed escape sequences in their
   own tool args; those flow into `Verb` / `paramsPreview` (built from
   tool parameters in `agent_loop.go:181-183`).

Impact: terminal-content spoofing, screen overwrite, malicious clickable
hyperlinks, terminal title hijack, BEL flooding. In a few legacy
terminals, DECRQSS / DA1 responses or font-loading escapes have been
weaponised for code execution (see the long history of CVE-2003-0063
class bugs); modern emulators have largely closed those, but OSC 8
phishing remains universally effective.

**Recommended fix**: strip C0 (`\x00`-`\x1f` except `\t` and possibly `\n`)
and C1 (`\x80`-`\x9f`) plus the OSC opener `\x9d` from any string that
flows from tool output / LLM output / MCP payload into a TUI chip field.
A small dedicated helper applied at the **engine** publish boundary
(`agent_loop_events.go`) is the right layer — both the TUI and the web
client benefit, and the web client cannot rely on browser DOM APIs to
neutralise these bytes anyway. Reference implementation: a regexp like
``[\x00-\x08\x0b-\x1f\x7f-\x9f]`` replaced with `�` or omitted.

---

### XSS-005 — TUI chat assistant streaming may render LLM-emitted ANSI

- **File:line**:
  `ui/tui/transcript.go:118` (chat digest preview);
  general path: assistant streamed deltas → chat transcript lipgloss render.
- **Severity**: Medium
- **Confidence**: M
- **CWE**: CWE-150

The assistant's streamed text is appended to the chat transcript and
rendered with lipgloss. The web path uses `target.textContent +=` (safe).
The TUI path stores the delta in a message struct and renders it with
markdown-aware lipgloss (`ui/tui/theme/render.go`, the markdown chain).
That chain processes markdown structure but does not enumerate and strip
C0 / OSC / DCS sequences from inline runs. Consequently, an LLM (which
can be steered by prompt injection from any tool output above) can emit
the same OSC-8 / OSC-0 / cursor-move attacks listed in XSS-004 directly
in its visible response.

The same fix as XSS-004 applies — sanitise at the boundary where
external bytes enter the TUI, ideally engine-side so all UIs benefit.

---

### XSS-006 — `web_fetch` HTML-to-text retains raw control bytes from response body

- **File:line**: `internal/tools/web.go:182-217` (`htmlToText` and the
  tokenizer walk; control bytes inside text-node content are not filtered)
- **Severity**: Medium
- **Confidence**: H
- **CWE**: CWE-150

`htmlToText` walks the tokenizer output, collapses whitespace via
`reWS = regexp.MustCompile([ \\t]+)` and `reNL = regexp.MustCompile(\\n{3,})`,
but those regexes only operate on space/tab/newline. ESC, BEL, and other
C0/C1 bytes pass through untouched into the returned `output`, which becomes
the tool `Output` and then the LLM-visible tool result *and* the TUI chip
preview/inner-lines. This is the upstream half of XSS-004 for the
`web_fetch` codepath specifically; calling it out separately because it is
a single function whose contract should arguably be "produce safe plain
text" — the fix is local to `htmlToText`.

---

### XSS-007 — JSON-error reflection of user-supplied path values (informational, not exploitable)

- **File:line**: `ui/web/server_task.go:153`, `server_tools_skills.go:40`,
  `:82`, `:196` (and similar across the package)
- **Severity**: Info
- **Confidence**: H
- **CWE**: CWE-79 (would-be)

Several handlers reflect the user-supplied `{name}` or `{id}` path values
into JSON error bodies, e.g.

```go
writeJSON(w, http.StatusNotFound, map[string]any{"error": "task " + id + " not found"})
writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("unknown tool: %s", name)})
```

These are not exploitable as XSS because:

1. `writeJSON` sets `Content-Type: application/json` (no MIME sniff to
   HTML in modern browsers).
2. The server middleware sets `X-Content-Type-Options: nosniff`
   (`server.go:122-129`).
3. JSON.parse on the client treats the value as a string; it would only
   become DOM if a developer later wrote it via `innerHTML` — currently
   no such code exists.

Recorded as Info so the pattern is on the radar if the workbench ever
starts rendering server error bodies into innerHTML rather than
textContent.

---

## Negative findings (checked, not present)

- No `eval`, `Function`, `setTimeout(string, ...)`, `setInterval(string, ...)`,
  `document.write`, `dangerouslySetInnerHTML` in the workbench.
- No `marked` / `markdown-it` / `DOMPurify` / framework-style markdown
  rendering on the client; the workbench presents LLM output inside `<pre>`
  via `textContent`.
- No `html/template` / `text/template` execution on user-controlled data
  anywhere in the Go codebase. The single `text/template`-shaped string is
  inside `internal/langintel/go_kb.go:208` (a security-rule description
  string for the scanner — not a template).
- `WebSocket Upgrader.CheckOrigin` always returns `true` is documented in
  `architecture.md` and is gated by the bind-host normalisation that forces
  loopback when auth=none — out of scope for sc-xss but flagged for
  cors / cross-origin work.

---

## Suggested remediations (priority order)

1. Add a single engine-boundary control-byte stripper (XSS-004 fix)
   applied at `internal/engine/agent_loop_events.go` to every string that
   becomes a TUI / web chip field. Keep `\t` and `\n`; drop the rest of
   C0/C1.
2. Extend `escapeHTML` in `ui/web/static/index.html:676` to escape `"`
   and `'` (XSS-001).
3. Migrate the two innerHTML hotspot blocks (XSS-002) to
   `createElement`+`textContent` for consistency.
4. Add a control-byte filter inside `htmlToText` (XSS-006).
