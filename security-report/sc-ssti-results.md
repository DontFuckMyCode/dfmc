# sc-ssti — Server-Side Template Injection

**Date:** 2026-04-29
**Scope:** D:\Codebox\PROJECTS\DFMC
**Status:** NO ISSUES FOUND — promptlib uses a regex-based variable substituter, not a template engine

## Verdict

No findings. DFMC's prompt library does not use `text/template`, `html/template`, or any other expression-evaluating engine. Variable substitution in [internal/promptlib/promptlib.go](../internal/promptlib/promptlib.go) is a regex-based key→value lookup that cannot evaluate code. Template *bodies* (the strings interpreted by `Render`) are loaded from trusted on-disk locations only — embedded defaults, `~/.dfmc/prompts`, and `<project>/.dfmc/prompts`. Untrusted user query text is fed in as a *value* (`Vars["query"]`), never as a template body.

## Verification

### 1. No template-engine import in promptlib

[internal/promptlib/promptlib.go:1-19](../internal/promptlib/promptlib.go) imports:

```go
"embed", "encoding/json", "errors", "fmt", "io/fs", "os",
"path/filepath", "regexp", "sort", "strings", "sync",
"github.com/dontfuckmycode/dfmc/internal/config",
"github.com/dontfuckmycode/dfmc/pkg/types",
"gopkg.in/yaml.v3"
```

No `text/template`, no `html/template`, no `pongo2`, no `quicktemplate`, no `mustache`, no `handlebars`. The only "template engine" in DFMC is a `regexp` over `{{ name }}` placeholders.

### 2. The substitution function — no expression evaluation

[internal/promptlib/promptlib.go:413-429](../internal/promptlib/promptlib.go):

```go
var placeholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func renderBody(body string, vars map[string]string) string {
    if strings.TrimSpace(body) == "" {
        return ""
    }
    if vars == nil {
        return body
    }
    return placeholderRe.ReplaceAllStringFunc(body, func(match string) string {
        parts := placeholderRe.FindStringSubmatch(match)
        if len(parts) != 2 {
            return ""
        }
        return strings.TrimSpace(vars[parts[1]])
    })
}
```

Reads:
- The placeholder regex matches **only** `{{ <ident> }}` where `<ident>` is `[a-zA-Z0-9_]+`. No spaces, no dots, no pipes, no parens, no method calls.
- Substitution is a literal `vars[name]` map lookup with `strings.TrimSpace`. No evaluation, no chained pipelines, no `printf`/`exec`/`include`.
- Unknown placeholders resolve to the empty string.

This is not Go's `text/template` and **cannot invoke methods, call functions, or reach outside the `vars` map.**

### 3. Template-body loader — trusted sources only

[internal/promptlib/promptlib.go:84-124](../internal/promptlib/promptlib.go) `LoadOverrides` reads from two roots:

1. `filepath.Join(config.UserConfigDir(), "prompts")` — `~/.dfmc/prompts` (user-owned).
2. `filepath.Join(projectRoot, ".dfmc", "prompts")` — project local (gitignored).

Plus the `//go:embed defaults/*.yaml` set baked into the binary at build time. **No HTTP body, no HTTP query parameter, and no LLM output is ever passed in as a template body** — the only ingress is local files the operator placed on disk. (Anyone who can write to your `~/.dfmc/prompts` already has full local code-execution by editing the binary path or `.dfmc/config.yaml`'s hook list.)

### 4. `compose: replace | append` — no body interpolation

[internal/promptlib/promptlib.go:175-197](../internal/promptlib/promptlib.go) `spliceAppendBeforeCacheBreak` is **string concatenation** (`strings.Join`, `strings.Cut`, `Builder.WriteString`). It splices overlay bodies around a fixed `CacheBreakMarker`. The bodies are not re-evaluated after splicing — they were rendered earlier by `renderBody` (the regex substituter above) using the same trusted sources. There is no second interpretation pass that would let an overlay body inject template syntax into another overlay's expression context.

### 5. `Vars` payload — user query goes in as a value, not a body

The `engine.buildSystemPrompt` callers populate `RenderRequest.Vars` with strings like `"query"`, `"project_root"`, `"language"`. These are plugged into the `vars` map and substituted by `renderBody`. Even if a user query contains `{{ ... }}` literally, it lands in the **rendered output string**, not in the template body — `placeholderRe` is only applied to `body`, not to the substitution values themselves. The substitution is single-pass; the result is not re-rendered.

Confirmed: the regex runs over `body` and the `func` returns the looked-up value verbatim. No recursive expansion.

### 6. `text/template` / `html/template` use across the repo — none on user data

```
Pattern: text/template|html/template (production *.go files only)
Hits:
- internal/langintel/go_kb.go:236-237 — knowledge-base prose strings teaching
  the LLM about Go's templating packages. NOT a parser invocation.
```

There are no `template.New(...)`, `template.Parse(...)`, `template.Must(...)`, `(*template.Template).Execute(...)` calls anywhere in production code.

### 7. YAML / JSON / Markdown loaders — data-mode only

The decoders in [internal/promptlib/promptlib.go:477-590](../internal/promptlib/promptlib.go) call `yaml.Unmarshal` and `json.Unmarshal` on file bytes, plus a hand-rolled YAML-frontmatter splitter for `.md`. None of them evaluate Go reflection callbacks (no `UnmarshalYAML` / `UnmarshalJSON` methods on `Template` — fields are plain strings). gopkg.in/yaml.v3 does not support YAML tag execution that could trigger code paths.

## Phases not run

These sc-ssti probes were skipped because there is no template engine to probe:
- Engine-fingerprinting payloads (`{{7*7}}`, `${7*7}`, `<%= 7*7 %>`, `#{7*7}`)
- `(System|Runtime).getRuntime().exec` chains (Java)
- `os.popen` / `subprocess` chains (Python Jinja/Mako)
- `__class__.__mro__` / `__subclasses__` SSTI escapes
- Pongo2/Quicktemplate / Go `text/template` `.FuncMap` injection

## Bottom line

DFMC's prompt rendering is a **regex variable-substituter, not a template engine**. There is no expression evaluation, no method invocation, no function-map exposure. Template bodies come exclusively from trusted on-disk sources (embedded defaults + user/project `.dfmc/prompts` directories the operator owns). User-controlled text only enters as a *value* in the `vars` map, not as a body. **sc-ssti is not exploitable in this codebase.**

## Recommendations (defensive, no current finding)

- Do not introduce `text/template` or `html/template` parsing of any file under `~/.dfmc/prompts` or `.dfmc/prompts` without first re-running this scan — the current model is "values are scalars only," and a template engine would change that contract.
- If an HTTP endpoint is ever added that lets a remote caller upload a prompt-template file, the trust assumption ("operator placed it on disk") breaks. Add such an endpoint behind an explicit "I know what I'm doing" flag and forbid expression-evaluating engines in that path.
