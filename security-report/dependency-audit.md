# Dependency Audit

## Direct Dependencies (go.mod)

| Package | Version | Purpose | Risk Level | Notes |
|---------|---------|---------|------------|-------|
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI framework | Low | Charm ecosystem, active |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI styling | Low | Same ecosystem |
| `github.com/charmbracelet/x/ansi` | v0.11.7 | ANSI handling | Low | Same ecosystem |
| `github.com/gorilla/websocket` | v1.5.3 | WebSocket | Low | Community-maintained since 2023 |
| `github.com/mattn/go-isatty` | v0.0.20 | Terminal detection | Very Low | Read-only |
| `github.com/muesli/cancelreader` | v0.2.2 | Reader cancellation | Very Low | Small utility |
| `github.com/muesli/termenv` | v0.16.0 | Terminal env | Very Low | Read-only |
| `github.com/tetratelabs/wazero` | v1.11.0 | WASM runtime | Medium | Code execution; funded org |
| `github.com/tree-sitter/go-tree-sitter` | v0.25.0 | AST parsing (CGO) | Medium | C code via CGO |
| `github.com/tree-sitter/tree-sitter-go` | v0.23.4 | Go grammar (CGO) | Low | Official tree-sitter org |
| `github.com/tree-sitter/tree-sitter-javascript` | v0.23.1 | JS grammar (CGO) | Low | Official |
| `github.com/tree-sitter/tree-sitter-python` | v0.23.6 | Python grammar (CGO) | Low | Official |
| `github.com/tree-sitter/tree-sitter-typescript` | v0.23.2 | TS grammar (CGO) | Low | Official |
| `go.etcd.io/bbolt` | v1.4.3 | Embedded KV store | Low | etcd team, well-audited |
| `golang.org/x/net` | v0.53.0 | Networking | Low | Official Go project |
| `golang.org/x/sys` | v0.43.0 | System calls | Low | Official Go project |
| `golang.org/x/time` | v0.15.0 | Rate limiting | Very Low | Official Go project |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML parsing | Low | Stable, no RCE in Go |

## Supply Chain Assessment

- **No typosquatting risks detected** — all paths are canonical org repos
- **No abandoned packages** — all have commits within last 12 months
- **CGO dependencies** (tree-sitter) pull in C code — memory safety handled by input being local project files only
- **WASM runtime** (wazero) is the highest-privilege dependency — used for plugin sandboxing with no host FS access grant
- **go.sum integrity** — all dependencies have verifiable hashes

## Known Vulnerabilities

No known CVEs found for any dependency at their pinned versions (as of 2026-05-05).

## Recommendations

1. Run `govulncheck ./...` periodically to catch newly-disclosed CVEs
2. Consider pinning the Go toolchain version in CI (currently `go1.26.2`)
3. The `gorilla/websocket` package is stable but consider migration to `nhooyr.io/websocket` for long-term maintenance confidence
