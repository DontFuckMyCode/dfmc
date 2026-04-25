# Dependency Audit — DFMC

**Target:** `github.com/dontfuckmycode/dfmc`
**Manifest:** `go.mod` (Go 1.25.0)
**Lock-equivalent:** `go.sum`
**Date:** 2026-04-25
**Method:** Static reading of `go.mod` / `go.sum` / `ui/web/static/*` only. No network calls, no `go list`, no `govulncheck` invocation. CVE knowledge is drawn from training-data familiarity with public Go ecosystem advisories; **all version-specific CVE attribution is best-effort and confidence is explicitly noted per finding.**

---

## 1. Inventory

### 1.1 Direct dependencies (22 modules)

| Module | Version | Role |
|---|---|---|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI framework |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI styling |
| `github.com/charmbracelet/x/ansi` | v0.11.7 | ANSI helpers |
| `github.com/mattn/go-isatty` | v0.0.20 | TTY detection |
| `github.com/muesli/cancelreader` | v0.2.2 | Cancellable stdin reads |
| `github.com/muesli/termenv` | v0.16.0 | Terminal env detection |
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | **CGO** — tree-sitter Go bindings |
| `github.com/tree-sitter/tree-sitter-go` | v0.25.0 | **CGO** — Go grammar |
| `github.com/tree-sitter/tree-sitter-javascript` | v0.25.0 | **CGO** — JS grammar |
| `github.com/tree-sitter/tree-sitter-python` | v0.25.0 | **CGO** — Python grammar |
| `github.com/tree-sitter/tree-sitter-typescript` | v0.23.2 | **CGO** — TS grammar |
| `go.etcd.io/bbolt` | v1.4.3 | Embedded KV store |
| `golang.org/x/net` | v0.53.0 | HTTP/2 etc. |
| `golang.org/x/sys` | v0.43.0 | Syscall surface |
| `golang.org/x/time` | v0.15.0 | Rate limiter |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parser |

### 1.2 Indirect dependencies (declared in go.mod indirect block)

| Module | Version |
|---|---|
| `github.com/aymanbagabas/go-osc52/v2` | v2.0.1 |
| `github.com/charmbracelet/colorprofile` | v0.4.3 |
| `github.com/charmbracelet/x/cellbuf` | v0.0.15 |
| `github.com/charmbracelet/x/term` | v0.2.2 |
| `github.com/clipperhouse/displaywidth` | v0.11.0 |
| `github.com/clipperhouse/uax29/v2` | v2.7.0 |
| `github.com/erikgeiser/coninput` | v0.0.0-20211004153227-1c3628e74d0f (pseudo-version, 2021-10-04) |
| `github.com/gorilla/websocket` | v1.5.3 |
| `github.com/lucasb-eyer/go-colorful` | v1.4.0 |
| `github.com/mattn/go-localereader` | v0.0.1 |
| `github.com/mattn/go-pointer` | v0.0.1 |
| `github.com/mattn/go-runewidth` | v0.0.23 |
| `github.com/muesli/ansi` | v0.0.0-20230316100256-276c6243b2f6 (pseudo-version, 2023-03-16) |
| `github.com/rivo/uniseg` | v0.4.7 |
| `github.com/tetratelabs/wazero` | v1.11.0 |
| `github.com/xo/terminfo` | v0.0.0-20220910002029-abceb7e1c41e (pseudo-version, 2022-09-10) |
| `golang.org/x/text` | v0.36.0 |

### 1.3 Additional transitives present in go.sum but not declared as direct/indirect in go.mod

These are pulled in for tree-sitter sub-grammars and test utilities. Their hashes appear in `go.sum`:

| Module | Version | Notes |
|---|---|---|
| `github.com/davecgh/go-spew` | v1.1.1 | testify transitive |
| `github.com/pmezard/go-difflib` | v1.0.0 | testify transitive |
| `github.com/stretchr/testify` | v1.10.0 | test-only |
| `github.com/tree-sitter/tree-sitter-c` | v0.23.4 | grammar (CGO) |
| `github.com/tree-sitter/tree-sitter-cpp` | v0.23.4 | grammar (CGO) |
| `github.com/tree-sitter/tree-sitter-embedded-template` | v0.23.2 | grammar |
| `github.com/tree-sitter/tree-sitter-html` | v0.23.2 | grammar |
| `github.com/tree-sitter/tree-sitter-java` | v0.23.5 | grammar |
| `github.com/tree-sitter/tree-sitter-json` | v0.24.8 | grammar |
| `github.com/tree-sitter/tree-sitter-php` | v0.23.11 | grammar |
| `github.com/tree-sitter/tree-sitter-ruby` | v0.23.1 | grammar |
| `github.com/tree-sitter/tree-sitter-rust` | v0.23.2 | grammar |
| `golang.org/x/exp` | v0.0.0-20231006140011-7918f672742d | experimental APIs |
| `golang.org/x/sync` | v0.20.0 | sync primitives |
| `gopkg.in/check.v1` | v0.0.0-20161208181325-20d25e280405 | yaml.v3 test transitive |

### 1.4 Notable absences

- **No SDK clients for Anthropic, OpenAI, DeepSeek, Kimi, Z.ai, Alibaba, MiniMax, Google AI** are pulled in. DFMC rolls its own HTTP clients against `golang.org/x/net` + stdlib — this **reduces** the attack surface dramatically (no third-party JSON Schema generators, no SDK auth-helper sprawl).
- **No gRPC dependency** is present despite the architecture mention of `dfmc remote`. Either remote uses raw HTTP/WebSocket (`gorilla/websocket` is present) and gRPC is planned-but-not-implemented, or it's wired through a sub-module not currently in go.mod. **Worth verifying** that no removed gRPC code path remains that would need re-pinning.
- **No JWT library** (no `github.com/golang-jwt/jwt`, no deprecated `github.com/dgrijalva/jwt-go`). Good — no JWT-class CVE exposure.
- **No SQL driver** — bbolt only.
- **No package.json in repo.** Web UI is a single self-contained `ui/web/static/index.html` with inline CSS, no external scripts, no CDN references, no vendored JS libraries. `go:embed` ships it.

---

## 2. Findings

Findings use the `DEP-NNN` ID format. Severity follows the skill spec rubric (critical / high / medium / low / info).

---

### DEP-001 — `gopkg.in/yaml.v3` v3.0.1 — historical DoS CVE-2022-28948 — pinned at FIXED version

- **Severity:** info (not vulnerable, recorded for context)
- **Confidence:** high
- **Module:** `gopkg.in/yaml.v3` v3.0.1
- **Reachability:** used pervasively for prompt library, config files, `.dfmc/config.yaml`. Parses untrusted-ish input (project config from disk; user editable).
- **Detail:** CVE-2022-28948 (panic on malformed YAML alias) was **fixed in v3.0.1**. The project is pinned at the fixed version, not the vulnerable v3.0.0. `gopkg.in/yaml.v2` (RCE-class issues in old branches) is **not present** in `go.sum` — confirmed.
- **Action:** none required. Keep pin at >= v3.0.1.

---

### DEP-002 — `golang.org/x/net` v0.53.0 — version postdates known HTTP/2 CVE waves

- **Severity:** info
- **Confidence:** medium
- **Module:** `golang.org/x/net` v0.53.0
- **Reachability:** HTTP server in `ui/web/server.go` (port 7777), web fetch tool, provider HTTP calls. Internet-exposed when `dfmc serve` is run with non-localhost bind.
- **Detail:** Notable historical CVEs in this module include CVE-2023-39325 / CVE-2023-44487 ("HTTP/2 Rapid Reset", fixed at v0.17.0), CVE-2023-3978 (HTML tokenizer XSS, fixed at v0.13.0), CVE-2024-45338 (HTML parser non-linear parse, fixed at v0.33.0), and a stream-of `x/net/http2` advisories through 2024–2025. v0.53.0 is **well past** all of these. I do not have specific awareness of an unfixed advisory at v0.53.0.
- **Caveat:** my training data may not cover any advisory published between v0.53.0's release and the audit date. **Recommendation:** run `govulncheck ./...` once with network access to confirm — that's the only authoritative source for golang.org/x/* CVEs at exact pinned versions.
- **Action:** verify with `govulncheck` periodically. No code change required from static review.

---

### DEP-003 — `golang.org/x/sys` v0.43.0 — current, no known issues

- **Severity:** info
- **Confidence:** medium
- **Module:** `golang.org/x/sys` v0.43.0
- **Reachability:** syscall wrappers; pulled in by bubbletea, isatty, tree-sitter bindings, etc.
- **Detail:** `golang.org/x/sys` historically does not accumulate CVEs the way `x/net` and `x/crypto` do — it is largely thin syscall wrappers. v0.43.0 is recent. No known advisories in my training set apply.
- **Caveat:** confirm via `govulncheck`.
- **Action:** none.

---

### DEP-004 — `golang.org/x/text` v0.36.0 (indirect) — postdates CVE-2022-32149

- **Severity:** info
- **Confidence:** high
- **Module:** `golang.org/x/text` v0.36.0
- **Reachability:** indirect (transitive via TUI / locale handling). Not directly invoked from DFMC source but parses locale data when present.
- **Detail:** CVE-2022-32149 (`golang.org/x/text/language` DoS via crafted Accept-Language) was fixed at v0.3.8. v0.36.0 is far past that. No additional unfixed CVE known to me at this version.
- **Action:** none.

---

### DEP-005 — `golang.org/x/sync` v0.20.0 (transitive) — no known CVEs

- **Severity:** info
- **Confidence:** medium
- **Module:** `golang.org/x/sync` v0.20.0
- **Detail:** sync primitives; the package historically has no CVE record I am aware of.
- **Action:** none.

---

### DEP-006 — `gorilla/websocket` v1.5.3 (indirect) — current maintained branch

- **Severity:** info
- **Confidence:** medium
- **Module:** `github.com/gorilla/websocket` v1.5.3
- **Reachability:** powers `ui/web/server.go` SSE/WS for the `/ws` event stream when `dfmc serve` is running. Network-facing.
- **Detail:** the older `gorilla/websocket` had CVE-2020-27813 (DoS via integer overflow in `decompressNoContextTakeover`), fixed in v1.4.1. There was a brief period of project archival followed by re-adoption. v1.5.3 is the post-revival maintained line. I have no awareness of an unfixed CVE at v1.5.3.
- **Caveat:** confirm via `govulncheck`. This is a network-facing dependency, so it deserves periodic re-checks.
- **Action:** none required now; revisit on next audit cycle.

---

### DEP-007 — `go.etcd.io/bbolt` v1.4.3 — local-disk KV store

- **Severity:** info
- **Confidence:** high
- **Module:** `go.etcd.io/bbolt` v1.4.3
- **Reachability:** local file (`.dfmc/*.db`). Not network-facing. Single-process file lock (`ErrStoreLocked` is documented in CLAUDE.md as the design).
- **Detail:** bbolt has historically had no high-severity CVEs. v1.4.x is the current major. The `ErrStoreLocked` failure mode is documented and handled in `cmd/dfmc/main.go`'s degraded-startup allow-list, which is appropriate.
- **Action:** none.

---

### DEP-008 — Tree-sitter native bindings — CGO supply-chain consideration

- **Severity:** medium
- **Confidence:** high
- **Modules:**
  - `github.com/tree-sitter/go-tree-sitter` v0.25.0
  - `github.com/tree-sitter/tree-sitter-go` v0.25.0
  - `github.com/tree-sitter/tree-sitter-javascript` v0.25.0
  - `github.com/tree-sitter/tree-sitter-python` v0.25.0
  - `github.com/tree-sitter/tree-sitter-typescript` v0.23.2
  - (transitive grammars: `-c` v0.23.4, `-cpp` v0.23.4, `-embedded-template` v0.23.2, `-html` v0.23.2, `-java` v0.23.5, `-json` v0.24.8, `-php` v0.23.11, `-ruby` v0.23.1, `-rust` v0.23.2)
- **Reachability:** parses **untrusted source code** from the user's repo (every `read_file`, `find_symbol`, `codemap`, `ast_query` call). High-throughput parser exposure.
- **Detail:** these bindings ship vendored C source which is compiled at install time when `CGO_ENABLED=1`. This is a **build-time C compilation surface**:
  1. The vendored C source is committed in the dependency module — `go.sum` hashes pin it, so tampering is detectable at module-fetch time.
  2. The build requires a working C toolchain on the developer's machine; a malicious toolchain could inject code at build time. This is the standard CGO supply-chain threat.
  3. Tree-sitter parsers historically have had panic / out-of-bounds bugs on malformed input (`tree-sitter` upstream has fixed several segfault-class bugs over the years; CLAUDE.md notes the regex fallback is intentional).
  4. Parsers run in the same address space as DFMC. A parser segfault crashes the agent loop. CLAUDE.md flags this with the regex fallback.
- **No specific CVE** is attributed to these exact pinned versions in my training set, but tree-sitter as a supply-chain element warrants attention because (a) untrusted input flows through it and (b) it's CGO with vendored C.
- **Action:**
  - Track upstream `tree-sitter/tree-sitter` security advisories.
  - Confirm CI builds with a deterministic C toolchain (Go's CGO uses the system gcc/clang — pin via Docker for reproducibility).
  - Consider sandboxing AST parses behind a recover()/timeout boundary if not already (the regex fallback is a partial mitigation).

---

### DEP-009 — `tetratelabs/wazero` v1.11.0 (indirect) — WASM runtime

- **Severity:** medium
- **Confidence:** high
- **Module:** `github.com/tetratelabs/wazero` v1.11.0
- **Reachability:** referenced by `internal/pluginexec/wasm.go` (untracked file in the working tree per `git status`). Executes WASM plugins.
- **Detail:** wazero is a pure-Go WASM runtime, so no CGO/C surface. It is currently maintained by Tetrate. The runtime has been the subject of public hardening work but I am not aware of an unfixed CVE at v1.11.0. The **higher-tier risk** here is the *use* of wazero — if `internal/pluginexec` allows arbitrary user-supplied WASM modules to call host functions or read filesystem, that's a privilege boundary worth a dedicated review (out of scope for a dependency audit). The dependency itself appears clean.
- **Action:** confirm `internal/pluginexec` host-function exposure in a separate review (skill: `sc-go-deep-scan` or per-package threat model).

---

### DEP-010 — Pseudo-versioned (commit-pinned) indirect dependencies

- **Severity:** low
- **Confidence:** high
- **Modules:**
  - `github.com/erikgeiser/coninput` v0.0.0-20211004153227-1c3628e74d0f (Oct 2021)
  - `github.com/muesli/ansi` v0.0.0-20230316100256-276c6243b2f6 (Mar 2023)
  - `github.com/xo/terminfo` v0.0.0-20220910002029-abceb7e1c41e (Sep 2022)
  - `golang.org/x/exp` v0.0.0-20231006140011-7918f672742d (Oct 2023)
  - `gopkg.in/check.v1` v0.0.0-20161208181325-20d25e280405 (Dec 2016, test-only via yaml.v3)
- **Reachability:** all are tiny TUI/terminal helpers or test utilities. None is network-facing.
- **Detail:** these are commit-pinned (Go module `vX.Y.Z-DATE-COMMIT` pseudo-versions), which is **functionally equivalent to a hash pin** and is fine — go.sum verifies them. The age is worth noting because:
  1. Stale commit pins suggest unmaintained upstream → unpatched bugs may accumulate silently.
  2. `xo/terminfo` and `erikgeiser/coninput` are particularly old (2021–2022) and `xo/terminfo` is an archived-feeling project.
  3. None of these are known to me to have CVEs at the pinned commits, but their maintenance status is the actual risk.
- **Action:** no immediate change. On the next bubbletea/charmbracelet upgrade, these may be replaced or freshened automatically.

---

### DEP-011 — No `replace` directives — clean

- **Severity:** info
- **Confidence:** high
- **Detail:** `go.mod` contains zero `replace` directives. There are no local-path replacements (e.g. `=> ../foo`), no fork redirects (e.g. `=> github.com/myfork/...`), and no version overrides bypassing go.sum verification. **All dependencies resolve through the module proxy with hash verification.** This is the desired state.
- **Action:** none.

---

### DEP-012 — Go toolchain version `go 1.25.0`

- **Severity:** info
- **Confidence:** medium
- **Detail:** `go.mod` declares `go 1.25.0`. Go 1.25 is current; older Go runtimes (≤1.21) had several stdlib CVEs (`net/http` request smuggling, `crypto/tls` issues). 1.25.0 is past those.
- **Caveat:** Go 1.25.x patch releases ship security fixes regularly. Whichever **patch** version compiles the binary at release time matters more than the `go.mod` floor. Ensure CI uses an up-to-date 1.25.x patch.
- **Action:** keep CI Go version current; no go.mod change.

---

### DEP-013 — Lock-file (go.sum) integrity

- **Severity:** info
- **Confidence:** high
- **Detail:** `go.sum` is present, contains both `h1:` (module zip) and `/go.mod h1:` (go.mod) hashes for every module referenced — **complete coverage**. With no `replace` directives in go.mod, every module fetched at build time is hash-verified against go.sum. No bypass surface.
- **Action:** none.

---

### DEP-014 — Build-time risks: no `go:generate`, but CGO present

- **Severity:** low
- **Confidence:** high
- **Detail:** Recursive grep across the working tree found **zero `//go:generate` directives** in DFMC's own source. There are no pre-build code generators or external-binary invocations that the build process triggers.
  - The only build-time external surface is **CGO compilation** of the tree-sitter C sources (DEP-008). That requires a system C compiler (`gcc` / `clang`) at build time, which is the conventional CGO threat model.
- **Action:** none for go:generate. CGO pinning per DEP-008 is the relevant mitigation.

---

### DEP-015 — License compliance — no GPL/AGPL conflicts identified

- **Severity:** info
- **Confidence:** medium
- **Detail:** spot-check of declared dependencies (based on the upstream project licensing I am aware of):
  - `charmbracelet/*` — MIT
  - `bubbletea`, `lipgloss`, `termenv`, `cancelreader` — MIT
  - `go-isatty`, `go-runewidth`, `go-localereader`, `go-pointer` — MIT
  - `gorilla/websocket` — BSD-2-Clause
  - `bbolt` — MIT
  - `golang.org/x/*` — BSD-3-Clause
  - `gopkg.in/yaml.v3` — Apache 2.0 / MIT (dual)
  - `tree-sitter/go-tree-sitter` — MIT
  - `tree-sitter/tree-sitter-*` grammars — MIT (most) / Apache (some)
  - `tetratelabs/wazero` — Apache 2.0
  - `rivo/uniseg` — MIT
  - `lucasb-eyer/go-colorful` — MIT
  - `xo/terminfo` — MIT
  - `clipperhouse/*` — MIT
  - `aymanbagabas/go-osc52` — MIT
  - `erikgeiser/coninput` — MIT (small project)
  - `muesli/ansi` — MIT
  - `mattn/*` — MIT
  - `stretchr/testify` — MIT (test-only)
  - `davecgh/go-spew`, `pmezard/go-difflib` — ISC / BSD (testify deps)
  - `gopkg.in/check.v1` — BSD-2-Clause (test-only)
- **No GPL or AGPL dependency was identified.** All licenses appear permissive (MIT / BSD / Apache 2.0 / ISC) and compatible with an MIT-style downstream license.
- **Caveat:** the DFMC repo itself does **not contain a `LICENSE` file** at the root (Glob for `LICENSE*` returned no files). This is a **separate compliance issue** worth flagging — distributors of the binary cannot confirm DFMC's own license terms. CLAUDE.md says "MIT-ish" but there's no SPDX evidence in-tree. Not strictly a dependency concern, but a license-compliance concern downstream consumers will hit.
- **Action:**
  - Add a top-level `LICENSE` file declaring DFMC's license (matches the project's stated MIT-ish intent).
  - For each dependency, redistributing the DFMC binary technically requires preserving NOTICE/license text. Consider generating a `NOTICES.md` from `go-licenses` at release time.

---

### DEP-016 — Embedded JS/CSS — no vendored libraries, no CDN references

- **Severity:** info
- **Confidence:** high
- **Detail:** `ui/web/static/index.html` is the **only** file under `ui/web/static/` (Glob confirmed). It contains inline `<style>`/`<script>` blocks only — no `<script src="...">`, no `<link href="...">` to external resources, no `cdn.` references. All CSS variables are defined inline. No vendored JS library files (no `lib/`, no `vendor/`, no minified bundles).
- **This is a strong supply-chain posture** for the embedded web UI: there is no third-party JS attack surface and no risk of pulling a compromised CDN script at runtime.
- **Action:** none. If JS frameworks are added later, prefer `go:embed`-ed local copies with SRI (subresource integrity) attributes if any external sourcing is introduced.

---

### DEP-017 — `mattn/go-runewidth` v0.0.23 / `rivo/uniseg` v0.4.7 — Unicode width handling

- **Severity:** info
- **Confidence:** medium
- **Detail:** TUI input/display-width helpers. Both are well-known, widely used. v0.0.23 of `go-runewidth` is recent; older versions had display-width bugs but no security CVEs I am aware of. uniseg v0.4.7 is similarly mature.
- **Action:** none.

---

### DEP-018 — Test-only dependencies — `stretchr/testify`, `gopkg.in/check.v1`

- **Severity:** info
- **Confidence:** high
- **Detail:** testify v1.10.0 is current. `gopkg.in/check.v1` is test-only via yaml.v3. Neither ships in the production binary (they are not imported by non-test code paths). Test-only deps are out-of-scope for runtime CVE risk but are in-scope for CI supply-chain risk (a malicious testify could exfiltrate during CI).
- **Action:** none beyond keeping CI environment trusted.

---

## 3. Summary statistics

```
Total dependencies (go.sum):         32 modules (excluding self)
  Direct (declared in go.mod):       16 (note: go.mod groups some as indirect — see below)
  Indirect (declared in go.mod):     17
  Transitive (sum-only):              ~15 grammars + test deps + golang.org/x/exp, x/sync
Network-facing deps:                  3  (golang.org/x/net, gorilla/websocket, charmbracelet — TUI not network)
CGO-required deps:                    5 direct + 8 transitive grammars
replace directives:                   0
go:generate directives:               0
External CDN/JS in web UI:            0

Vulnerabilities (this static audit):
  Critical:  0
  High:      0
  Medium:    2  (DEP-008 tree-sitter CGO supply-chain surface, DEP-009 wazero WASM runtime — neither has a known CVE; both are flagged on attack-surface grounds, not specific advisories)
  Low:       1  (DEP-010 stale commit-pinned indirects)
  Info:     15

Confidence note:
  Static reading without `govulncheck` cannot authoritatively confirm the absence of unfixed CVEs at exact pinned versions for golang.org/x/* and gorilla/websocket. Run `govulncheck ./...` with network access for ground truth on those modules. The DEP-002, DEP-006 findings should be re-validated that way.
```

## 4. Recommended follow-ups

1. **Add a top-level `LICENSE` file** (DEP-015) — required for downstream redistribution clarity.
2. **Run `govulncheck ./...`** in CI on every PR. This is the only authoritative Go vulnerability source and complements this static audit.
3. **Pin CGO toolchain** in CI/release builds (Docker base image with a known gcc/clang version) — supports tree-sitter reproducibility (DEP-008).
4. **Consider a `NOTICES.md`** generated by `go-licenses` for the redistributed binary, listing each dep's license text per Apache 2.0 / BSD requirements.
5. **Confirm `internal/pluginexec/wasm.go` host-function exposure** (DEP-009) — out of scope for a dependency audit, but the wazero-using package warrants a separate threat-model review.
6. **Refresh stale commit-pinned indirects** (DEP-010) opportunistically when bumping bubbletea / charmbracelet, so security fixes in `xo/terminfo` and similar do not silently lag.
