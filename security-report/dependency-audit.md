# DFMC — Dependency Audit

**Phase:** 1 (Recon)
**Date:** 2026-04-30
**Manifest:** `go.mod` (Go 1.25.0)

---

## Summary

| Direct deps | Indirect deps | Total |
|------------|---------------|-------|
| 17 | 14 | 31 |

All dependencies are pinned to explicit versions (no `latest`, no replace directives, no local paths). Module checksums are committed in `go.sum`.

`govulncheck` cannot run in this sandbox; manual review against known-vulnerable patterns and recent advisories below.

---

## Direct Dependencies — Risk Review

| Module | Version | Role | Notes |
|--------|---------|------|-------|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI reactor | Active maintenance. No known CVEs. |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI styling | Pure layout; low surface. |
| `github.com/charmbracelet/x/ansi` | v0.11.7 | ANSI helpers | Internal helpers. |
| `github.com/gorilla/websocket` | v1.5.3 | WS server | **Maintained**: gorilla revived 2023. v1.5.x current. No outstanding CVEs. |
| `github.com/mattn/go-isatty` | v0.0.20 | TTY detect | Tiny. |
| `github.com/muesli/cancelreader` | v0.2.2 | Cancellable stdin | Tiny. |
| `github.com/muesli/termenv` | v0.16.0 | Color env detect | No CVEs. |
| `github.com/tetratelabs/wazero` | v1.11.0 | Wasm runtime | Pure-Go Wasm runtime; no syscall surface from guest. |
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | CGO bindings | C parser; CGO-only. Untrusted source could OOM if not bounded — DFMC limits parse content size. |
| `github.com/tree-sitter/tree-sitter-go` | v0.25.0 | Go grammar | Same. |
| `github.com/tree-sitter/tree-sitter-javascript` | v0.25.0 | JS grammar | Same. |
| `github.com/tree-sitter/tree-sitter-python` | v0.25.0 | Python grammar | Same. |
| `github.com/tree-sitter/tree-sitter-typescript` | v0.23.2 | TS grammar | Slightly older minor; check for upstream patches. |
| `go.etcd.io/bbolt` | v1.4.3 | Embedded KV | Active. v1.4.x current. No known CVEs. |
| `golang.org/x/net` | v0.53.0 | Net helpers (HTTP/2) | Recent; covers prior CVE-2023-39325 (h2 rapid reset). |
| `golang.org/x/sys` | v0.43.0 | OS syscalls | Stdlib-adjacent. |
| `golang.org/x/time` | v0.15.0 | Rate limiter | Stable. |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parser | v3.0.1 fixed CVE-2022-28948 (DoS); current. |

---

## Indirect Dependencies

All `// indirect` lines are pulled in by direct deps (mostly `bubbletea` ecosystem: ansi, cellbuf, term, colorprofile; `text` from `golang.org/x/...`). All on actively-maintained tracks.

---

## Supply Chain Hardening Notes

- ✅ `go.sum` committed; build is reproducible at the dep boundary.
- ✅ No `replace` directives; no local-path overrides.
- ✅ All deps pinned to non-`latest` semver.
- ⚠️ No CI integration of `govulncheck` visible in the repository (`Makefile` does not invoke it). Recommend wiring it into the test pipeline:

  ```
  go install golang.org/x/vuln/cmd/govulncheck@latest
  govulncheck ./...
  ```
- ⚠️ Tree-sitter bindings are CGO-only. The non-CGO build (`backend_stub.go`) silently falls back to a regex extractor — security-relevant analyzers (dead-code, find-symbol's "fallback: true") become best-effort. Documented but worth flagging when running in CI without CGO.
- ⚠️ `tree-sitter-typescript@v0.23.2` lags v0.25 of the other grammars by one minor — consider bump.
- No deps with known critical CVEs in their pinned ranges as of audit date.

---

## OWASP Dependency-Check Equivalent Findings

| Severity | Count | Notes |
|----------|-------|-------|
| Critical | 0 | — |
| High | 0 | — |
| Medium | 0 | — |
| Low | 1 | `tree-sitter-typescript` is on v0.23.2 while sibling grammars are v0.25.0 — version skew, no known vuln. |
| Info | 1 | `govulncheck` not wired into CI. |

---

## Recommended Actions

1. Add `govulncheck` invocation to the test pipeline (informational gate, not blocking).
2. Bump `tree-sitter-typescript` to v0.25.0 to align with other grammars.
3. Review the tree-sitter grammar advisories list quarterly (low frequency historically, but C parsers are a perennial OOM/parse-stall risk).

---
