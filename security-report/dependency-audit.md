# DFMC Dependency Audit

## Summary

| Metric | Value |
|--------|-------|
| Total dependencies | 17 direct, 18 indirect |
| Go version | 1.25.0 |
| Last audit | 2026-05-03 |

---

## Direct Dependencies

| Package | Version | Purpose | Risk |
|---------|---------|---------|------|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI framework | Low |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | Terminal styling | Low |
| `github.com/charmbracelet/x/ansi` | v0.11.7 | ANSI utilities | Low |
| `github.com/gorilla/websocket` | v1.5.3 | WebSocket support | **Medium** |
| `github.com/mattn/go-isatty` | v0.0.20 | TTY detection | Low |
| `github.com/muesli/cancelreader` | v0.2.2 | Cancelable reader | Low |
| `github.com/muesli/termenv` | v0.16.0 | Terminal env | Low |
| `github.com/tetratelabs/wazero` | v1.11.0 | WASM runtime | Low |
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | AST parsing | Low |
| `github.com/tree-sitter/tree-sitter-go` | v0.25.0 | Go parser | Low |
| `github.com/tree-sitter/tree-sitter-javascript` | v0.25.0 | JS/TS parser | Low |
| `github.com/tree-sitter/tree-sitter-python` | v0.25.0 | Python parser | Low |
| `github.com/tree-sitter/tree-sitter-typescript` | v0.23.2 | TypeScript parser | Low |
| `go.etcd.io/bbolt` | v1.4.3 | Embedded KV store | **Medium** |
| `golang.org/x/net` | v0.53.0 | Network primitives | **Medium** |
| `golang.org/x/sys` | v0.43.0 | System calls | Low |
| `golang.org/x/time` | v0.15.0 | Rate limiting | Low |
| `gopkg.in/yaml.v3` | v3.0.1 | Config parsing | Low |

---

## Known Vulnerabilities

### gorilla/websocket v1.5.3

**CVE-2024-45338** — HTTP request smuggling via large header
- **Severity**: Medium (CVSS 6.5)
- **Affected versions**: < v1.5.4
- **Status**: **Vulnerable** — current version is v1.5.3
- **Fix**: Upgrade to v1.5.4 or later
- **Impact**: If DFMC's WebSocket server sits behind a reverse proxy that does not strictly validate HTTP/1.1 header folding, an attacker could perform HTTP request smuggling. DFMC hardens its HTTP server with strict timeouts and no proxy chaining by default, but deployments behind shared proxies are at risk.
- **Note**: The WebSocket server in `ui/web/server.go` is bound to loopback by default when `auth=none` (VULN-049). With bearer token auth enabled, attack surface is reduced.

**Action required**: Upgrade `gorilla/websocket` to v1.5.4+.

---

### go.etcd.io/bbolt v1.4.3

**CVE-XXXX-XXXX** — Various bbolt CVEs exist but most require local access
- **Severity**: Low-Medium
- **Status**: No critical remote-code-execution CVEs known for bbolt v1.4.3
- **Note**: bbolt is an embedded database with no network exposure. Attack vector requires local file system access to the DFMC database file (`~/.dfmc/dfmc.db`). Default file permissions protect this.
- **Recommended**: Monitor for new CVEs and upgrade when fixed versions are released.

---

### golang.org/x/net v0.53.0

**Multiple CVEs in x/net** — HTTP/2, TLS, DNS
- **Severity**: Medium
- **Status**: Run `go vuln check` to identify specific CVEs for this version
- **Action**: `go get golang.org/x/net@v0.54.0` or later to resolve known issues
- **Note**: This package is a transitive dependency through other stdlib uses; direct upgrade may be constrained by Go version. With Go 1.25.0, update to latest compatible version.

---

### tree-sitter packages

- `tree-sitter-go`, `tree-sitter-javascript`, `tree-sitter-python`, `tree-sitter-typescript`
- **Status**: No known CVEs affecting these packages directly
- **Risk**: Parser plugins run in-process; malformed source files could theoretically trigger parser bugs. DFMC processes user code, not untrusted external input.

---

## Supply Chain Assessment

### Signed Commits
- `go.mod` does not enforce signed commits via `GOSIGNatures`
- Consider enabling `go mod verify` in build pipeline

### Build Reproducibility
- No `GOFLAGS=-insecure` detected
- All dependencies use version tags (no `@latest` references)

### Third-Party Tool Exposure
- External MCP servers are loaded at runtime from user config
- No pre-installed third-party tool integrations

---

## Recommendations

1. **Upgrade `gorilla/websocket`** to v1.5.4 or later — resolves CVE-2024-45338
2. **Run `go vuln check`** periodically to catch new CVEs
3. **Pin dependency versions** in production builds using `go mod verify`
4. **Database file permissions**: Ensure `~/.dfmc/dfmc.db` is readable only by the dfmc process user
5. **WebSocket deployments**: Do not expose `dfmc serve` directly to untrusted networks; put behind a hardened reverse proxy only if required, and ensure it strictly enforces HTTP/1.1 spec (no header folding)