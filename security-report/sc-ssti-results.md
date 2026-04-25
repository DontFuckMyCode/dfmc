# sc-ssti — Server-Side Template Injection Findings

**Target**: DFMC (`D:\Codebox\PROJECTS\DFMC`)
**Skill**: sc-ssti
**CWE**: CWE-94 (Code Injection), CWE-1336 (Improper Neutralization of
Special Elements Used in a Template Engine).

---

## Counts

| Severity | Count |
|---|---|
| High     | 0 |
| Medium   | 0 |
| Low      | 0 |
| Info     | 2 |
| **Total**| **2** |

No exploitable SSTI was found. Two informational findings document the
template-shaped surfaces that exist and the reasons they are *not* SSTI.

---

## Surface map

DFMC has three places that look superficially template-like and were each
investigated:

### 1. `internal/promptlib` overlay rendering — regex placeholder, NOT a template engine

- **File:line**: `internal/promptlib/promptlib.go:413-429`

```go
var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func renderBody(body string, vars map[string]string) string {
    if strings.TrimSpace(body) == "" { return "" }
    if vars == nil { return body }
    return placeholderRe.ReplaceAllStringFunc(body, func(match string) string {
        parts := placeholderRe.FindStringSubmatch(match)
        if len(parts) != 2 { return "" }
        return strings.TrimSpace(vars[parts[1]])
    })
}
```

This is the only "templating" applied to prompt overlay files
(`~/.dfmc/prompts/*.yaml`, `<project>/.dfmc/prompts/*.yaml`,
`internal/promptlib/defaults/*.yaml`). The substitution rules:

- The capture group is the strict identifier `[a-zA-Z0-9_]+`. There are
  no field selectors, no method calls, no pipes, no expressions.
- Lookup is a plain `map[string]string` access. Missing keys yield empty
  string (no panic, no exposure).
- The result is the variable's literal string value; it is **not**
  re-rendered, so a value containing `{{x}}` is not re-evaluated (no
  recursive expansion → no SSRF-via-template-recursion).

This is not Go's `text/template` or `html/template`. It cannot invoke
methods, walk struct fields, or read arbitrary properties of any engine-
internal data. The variable map is built explicitly by the engine
(`internal/engine/engine_prompt.go`'s `promptRuntimeVars`-like builders)
from a small allow-listed set: project root path, current working
directory, time, model, provider, language, etc. Even a malicious
overlay file in `~/.dfmc/prompts` cannot pivot to "introspect engine
internals" because the renderer literally does not know how.

**Verdict**: not SSTI. Recorded as **Info** (SSTI-001) for documentation
only.

### 2. `[[file:path#Lstart-Lend]]` injection markers — regex extraction, NOT template evaluation

- **File:line**: `internal/context/injected.go:32-101`

```go
var (
    injectionMarker  = regexp.MustCompile(`\[\[file:([^\]#]+?)(?:#L(\d+)(?:-L?(\d+))?)?\]\]`)
    queryCodeBlockRe = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*)\\r?\\n(.*?)\\r?\\n?```")
)
```

The user can place `[[file:internal/foo.go#L10-L40]]` markers in their
chat query. The handler:

1. Runs `injectionMarker.FindAllStringSubmatch` to extract `(rel, start,
   end)` triples. The `rel` capture is `[^\]#]+?` — anything that is
   not `]` or `#`, non-greedy.
2. Validates the path with `resolvePathWithinRoot(projectRoot, rel)`
   (`injected.go:13`, also documented in `architecture.md` §4 Sinks
   table). This is the same symlink-aware containment guard used by
   `read_file` and `EnsureWithinRoot` — it refuses `..`-escapes and
   resolved-symlink escapes.
3. Reads bytes via `os.ReadFile(abs)`; slices to the requested line
   range; renders as a fenced block in the system prompt.

There is **no template parsing** of the matched filename. A filename
containing `{{` or `{{ .Env }}` would be passed through to file I/O as
literal bytes and rejected if the path doesn't exist. Even if a file
*were* named `{{call .Whatever}}.go`, it would be read as a file —
neither the path nor the file body are evaluated by `text/template`.

**Verdict**: not SSTI. Recorded as **Info** (SSTI-002) for documentation
only.

### 3. Hooks command strings — `os/exec`, NOT a shell template

- **File:line**: `internal/hooks/hooks.go` (entire file); also
  `internal/tools/command.go:296-633` for the related `run_command`
  shell-detection guards.

Hooks (`hooks.entries.<event>[]`) are user-configured strings from
`~/.dfmc/config.yaml` (and project config when `hooks.allow_project=true`).
These are passed to `os/exec.Cmd`-style execution with the per-event
context as environment variables, NOT interpolated into a shell template.
There is no `Sprintf("hook: %s") | shell-eval` pattern. Argument splitting
uses standard tokenisation. This is properly an injection-/cmdi-domain
concern (see sc-cmdi for full coverage); it has no SSTI surface.

### 4. `text/template` / `html/template` use across the repo — none on user data

A repository-wide search for `template.New(`, `template.Must(`,
`.Parse(`, `.ParseFiles`, `.ParseGlob`, `.Execute(` returned:

- `internal/promptlib/*.go` — uses the word "template" as a *type name*
  (`type Template struct`) referring to the prompt-overlay struct, not
  Go's `text/template`. No `template.New` / `template.Parse` invocations.
- `internal/langintel/go_kb.go:206`, `:237` — the strings
  `"go-sql-injection"` and `"text/template does not auto-escape HTML"`
  appear inside knowledge-base entries (`Body:` field of `langintel`
  rule data). These are description strings the security scanner
  surfaces as advice, not actual template parsing.
- `ui/cli/cli_skills_data.go:205`, `:249` — the word "template" in
  natural-language skill descriptions ("Use read_file on a similar
  handler as a template").

No imports of the `text/template` or `html/template` packages exist
outside test code that exercises promptlib's regex placeholder.

---

## Findings

### SSTI-001 — Prompt overlay placeholder is regex, not Go template

- **File:line**: `internal/promptlib/promptlib.go:413-429`
- **Severity**: Info
- **Confidence**: H
- **CWE**: n/a (negative finding)

Documented above. A malicious overlay file in `~/.dfmc/prompts` cannot
escalate to template-engine introspection because the substitution
mechanism is a closed regex over a fixed string-map.

**Why this matters anyway**: the threat model still includes overlay
poisoning *as a prompt-injection vector* (a user-controlled prompt
overlay that re-instructs the LLM). That is a **prompt injection**
finding (out of scope for sc-ssti — see prompt-injection skill). For
SSTI specifically, the design is sound.

### SSTI-002 — `[[file:...]]` markers are regex-extracted, not template-evaluated

- **File:line**: `internal/context/injected.go:32-101`
- **Severity**: Info
- **Confidence**: H
- **CWE**: n/a (negative finding)

Documented above. The path-traversal guard
(`resolvePathWithinRoot`) is the actual security surface here, and it
is reviewed in sc-path-traversal / under "Sinks" in `architecture.md`.
For SSTI specifically, no template engine is involved.

---

## Negative findings (recap)

- No `template.Parse` / `template.New` / `template.Must` / `.Execute` /
  `.ParseFiles` / `.ParseGlob` invocations on user-controlled data
  anywhere in the codebase.
- No `html/template` import anywhere.
- No `text/template` import anywhere outside test fixtures of langintel
  rules (and even there, no actual template execution).
- The web workbench's prompt-debug endpoint (`GET /api/v1/prompt/debug`)
  surfaces *rendered* prompts (already-substituted regex output) as
  JSON; it does not parse new templates from user input.
- Drive planner LLM call passes the user's task to the LLM as a *string
  message*, not as a template (`internal/drive/planner.go`).

---

## Recommendations

None — the architecture deliberately uses regex-only substitution for
the prompt overlay surface, which is the single feature that "looks
templatable" to a curious user. Maintain that boundary if/when the
overlay format gains new features:

- Do not introduce `text/template` parsing of any file under
  `~/.dfmc/prompts/` or `<project>/.dfmc/prompts/` — overlays come from
  user-writable locations and a project repo can ship a malicious
  overlay (with `hooks.allow_project=true` analogue).
- If template power is genuinely needed, restrict to a sandboxed
  expression language (CEL, ucfg, or similar) and gate it behind an
  explicit project-level opt-in similar to `hooks.allow_project`.
