# sc-xss + sc-ssti Results

## Findings

### [Medium] `renderBody` in promptlib performs variable substitution without HTML escaping

- **File**: `internal/promptlib/promptlib.go:415`
- **Description**: `renderBody` uses `placeholderRe.ReplaceAllStringFunc` to replace `{{.VarName}}` tokens with values from the `vars` map. There is no HTML escaping — if a user-controlled variable (e.g. `Query`, `ContextFiles`, `Vars` from `PromptRenderRequest`) contains HTML metacharacters, they are substituted verbatim into the rendered prompt string. If that prompt is served over HTTP or rendered in the web workbench (embedded static HTML), it could enable XSS.
- **Impact**: XSS via prompt injection — a user crafts a message containing HTML meta-characters that survive through the context/compression pipeline and appear in a rendered system prompt delivered to the LLM or served via HTTP.
- **Evidence**:
  ```go
  func renderBody(body string, vars map[string]string) string {
      return placeholderRe.ReplaceAllStringFunc(body, func(match string) string {
          parts := placeholderRe.FindStringSubmatch(match)
          return strings.TrimSpace(vars[parts[1]])  // no html.EscapeString
      })
  }
  ```
  `PromptRenderRequest.Vars` comes directly from HTTP API caller. `RenderRequest.ContextFiles` can contain resolved `[[file:...]]` user query markers with file content that may include HTML characters.
- **Mitigation**: Apply `html.EscapeString()` to each value before substitution. For raw code variables (e.g. from `[[file:...]]`), consider a separate escaping path. Or use `html/template` for rendering to get contextual escaping automatically.

### [Low] SSE/WebSocket streams tool output without re-sanitization at transport layer

- **File**: `ui/web/server_chat.go:97`, `ui/web/server_ws.go`
- **Description**: User messages flow through `engine.Ask` / `engine.StreamAsk` and tool output is published via SSE deltas (`type: delta`) or WS `event` frames. The engine has `stripTerminalControlBytes` at the publish boundary (`internal/engine/terminal_sanitize.go`) which strips ANSI escape sequences (C0/C1). However, `ev.Payload` (`map[string]any`) is relayed directly into JSON without a second sanitization pass.
- **Impact**: If a malicious tool output bypasses `stripTerminalControlBytes` (e.g. hostile terminal sequences beyond C0/C1, or HTML metacharacters for web rendering), it reaches every SSE/WS subscriber.
- **Evidence** (`server_chat.go:97`):
  ```go
  if !writeSSEWithDeadline(w, flusher, map[string]any{
      "type":  "delta",
      "delta": ev.Delta,  // ev.Delta is user-controlled tool output
  }) {
  ```
- **Mitigation**: Verify `stripTerminalControlBytes` also HTML-escapes strings destined for web SSE/WS rendering, or add HTML escaping at the SSE/WS serialization boundary.

### [Low] No `HttpOnly` / `Secure` / `SameSite` cookie attributes

- **File**: `ui/web/server.go` (bearer token handling)
- **Description**: `DFMC_WEB_TOKEN` is stored in memory and transmitted via `Authorization: Bearer` header — no `Set-Cookie` is used, so no cookie attributes to configure. Informational finding only.
- **Impact**: Not currently vulnerable — current design avoids cookies entirely.
- **Mitigation**: If cookie-based sessions are added in future, apply: `HttpOnly=true`, `Secure=true`, `SameSite=Strict`.

### [Info] CSP lacks `object-src 'none'` and `base-uri 'self'`

- **File**: `ui/web/server.go:130`
- **Description**: CSP `"default-src 'self'; script-src 'self'; style-src 'self'"` omits `object-src 'none'` and `base-uri 'self'` recommended by CSP Level 3.
- **Impact**: Lower risk given `script-src 'self'` prevents inline scripts, but defaults could be tightened.
- **Mitigation**: Add `object-src 'none'; base-uri 'self'` to the CSP string.

### [Info] TUI markdown rendering — no HTML injection path

- **File**: `ui/tui/theme/markdown.go`
- **Description**: `RenderMarkdownLite` applies only `lipgloss` style transforms. No raw HTML injection — output is terminal-styled strings via `lipgloss`, not an HTML renderer. Engine's `stripTerminalControlBytes` further sanitizes tool output before it reaches TUI.
- **Impact**: No finding — TUI is terminal-based, not vulnerable to HTML injection.

---

## No Issues Found

- **SSTI**: No `text/template` or `html/template.Execute` usage. Prompt library uses custom regex placeholder substitution, not Go templates.
- **Reflected XSS**: Query params from WS upgrade requests only used in allowlist logic (`EventBus.SubscribeFunc`), not reflected into responses.
- **DOM-based XSS**: Web workbench is embedded static HTML with no inline event handlers or `eval()`. Dynamic data arrives via SSE/WS as JSON and is rendered by vanilla JS — no `innerHTML` assignments of user-controlled strings found.
- **WebSocket frame injection**: Per-connection rate limit (5 rps, burst 10), global/IP connection caps, 64 KiB frame size cap (`wsReadLimit`).