# SSRF Results — DFMC

Scope: outbound HTTP from `web_fetch`, `web_search`, LLM provider clients,
MCP transport, hooks. All findings verified against source.

Note: there is no `internal/tools/web_fetch.go` — the `web_fetch` and
`web_search` tools both live in `internal/tools/web.go` (Phase 1
architecture report mentioned a separate file; that's not the layout).

## Counts per file

| File | Findings |
|---|---|
| `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go` | 3 |
| `D:/Codebox/PROJECTS/DFMC/internal/provider/openai_compat.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/internal/provider/anthropic.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/internal/provider/google.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/internal/provider/http_client.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/internal/config/config_models_dev.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/ui/cli/cli_update.go` | 1 |
| `D:/Codebox/PROJECTS/DFMC/internal/mcp/client.go` | 0 (info) |
| `D:/Codebox/PROJECTS/DFMC/internal/hooks/hooks.go` | 0 (info) |

---

## SSRF-001 — `web_fetch` SSRF guard verified solid (Informational)

- **Severity**: Info — the Phase 1 claim ("DNS-rebind-safe, dial-time
  check") is accurate.
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:24-48`
- **CWE**: N/A (positive finding)

The `safeTransport.DialContext` resolves the host *at dial time* and
rejects any IP that is loopback, RFC1918 private, link-local unicast, or
link-local multicast — closing the DNS rebinding window. Verified
coverage:

- `127.0.0.0/8` → `IsLoopback()` → blocked.
- `::1` → `IsLoopback()` → blocked.
- `0.0.0.0` → `IsLoopback()` → blocked (Go's `IsLoopback` returns true).
- `169.254.169.254` (AWS/Azure/GCP/Hetzner metadata) →
  `IsLinkLocalUnicast()` → blocked.
- `fe80::/10` IPv6 link-local → `IsLinkLocalUnicast()` → blocked.
- RFC1918 (`10/8`, `172.16/12`, `192.168/16`) → `IsPrivate()` → blocked.
- IPv4-mapped variants (`::ffff:127.0.0.1`) → `IsLoopback()` → blocked.
- Multi-A-record DNS rebind: `LookupIPAddr` returns *all* IPs and the
  loop rejects the request if **any** IP is private/loopback (see lines
  35-39). This means the attacker cannot mix one public IP plus one
  private IP and rely on `Dial` happening to pick the public one.

URL scheme is constrained to `http`/`https` at lines 113-125 — `file://`,
`ftp://`, `data:`, `javascript:`, and bare hostnames are rejected with a
self-teaching error.

NOT covered (acceptable, but worth knowing):

- ULA `fc00::/7` (IPv6 unique-local). Go's `net.IP.IsPrivate()` does
  return true for `fc00::/7`, so this IS in fact covered by the existing
  check.
- The hand-rolled CGNAT `100.64.0.0/10` is NOT in `net.IP.IsPrivate()`
  but is also not really a public attack target for SSRF — it's used by
  ISPs and most corp networks won't have a metadata service there.

## SSRF-002 — Redirects routed through SSRF-guarded transport (Informational)

- **Severity**: Info
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:51-60`

The shared `httpClient` uses `safeTransport` for the original AND every
redirected request, because Go's `http.Client.Do` reuses the same
`Transport` for the whole redirect chain. Each redirect target is
re-resolved at dial time through `safeTransport.DialContext`, so a
public→private 30x cannot escape the guard. Redirect cap = 5 (line 55).

User-Agent (`DFMC/1.0 (+https://github.com/dontfuckmycode/dfmc)`) and
the Accept/Accept-Language headers are static constants — no
attacker-controlled header content. The URL is the only attacker input,
and it goes through the IP guard.

## SSRF-003 — `web_search` pre-flight DNS check is best-effort, NOT a security boundary (Informational)

- **Severity**: Info — the comment at line 62-65 explicitly says so.
- **Confidence**: High
- **File:line**:
  - Pre-flight check: `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:62-82`
    (`isBlockedHost`)
  - Hot path: `D:/Codebox/PROJECTS/DFMC/internal/tools/web.go:364-365`
- **CWE**: CWE-918 (informational — defence still holds via SSRF-001
  because the same `httpClient` does the actual fetch)

`isBlockedHost` runs before the search request, but the comment is honest:
"this is not a security boundary by itself — the actual SSRF guard lives
in `safeTransport.DialContext`." It performs an extra DNS lookup that
introduces a TOCTOU window between pre-flight check (line 364) and the
actual `httpClient.Do` (line 370). That window doesn't matter because
`safeTransport` re-resolves at dial time. So **pre-flight removal would
have no security impact** — it's a UX nicety that filters out search
result URLs pointing at private IPs (line 397 `isResultURLBlocked`)
before they reach the model.

## SSRF-004 — LLM provider base URL is operator-controlled config, not request-controlled (Low / Config-Trust)

- **Severity**: Low (config-trust dependent)
- **Confidence**: High
- **File:line**:
  - `D:/Codebox/PROJECTS/DFMC/internal/provider/openai_compat.go:30-39, 103, 209`
  - `D:/Codebox/PROJECTS/DFMC/internal/provider/anthropic.go:31-44, 84, 178`
  - `D:/Codebox/PROJECTS/DFMC/internal/provider/google.go:54-56, 75, 147`
  - `D:/Codebox/PROJECTS/DFMC/internal/provider/http_client.go:45-64` (no SSRF guard on transport)
- **CWE**: CWE-918 (Server-Side Request Forgery)

`newProviderHTTPClient` builds a transport with `Proxy:
http.ProxyFromEnvironment` and a stdlib `net.Dialer` — **no
loopback/private-IP filter on dial**. The base URL comes from
`Providers.Profiles[name].BaseURL` (`config_types.go:240`), which is
operator-set in `~/.dfmc/config.yaml` or `<project>/.dfmc/config.yaml`.

Concrete attack paths only when the threat model includes a hostile
project config:

1. A malicious `<project>/.dfmc/config.yaml` shipped via `git clone`
   could set `providers.profiles.openai.base_url:
   http://169.254.169.254/latest/meta-data/iam/security-credentials/`
   and the next `dfmc ask` would POST `model=..., messages=[...]` to the
   metadata endpoint. The endpoint will 4xx, but the request is sent —
   request timing or differential 4xx codes can fingerprint internal
   services.
2. `base_url: http://localhost:9200/_search` could probe internal
   Elasticsearch / admin panels.

Mitigation is **trust-boundary based**, not technical: project configs
are read at engine init (`internal/config/config.go`), so a user
unsuspectingly cloning a hostile repo and running `dfmc` exposes
themselves. There is no "block private IPs in provider base URLs" guard.
This is documented behaviour (CLAUDE.md describes provider profile
override) but worth flagging for an operator who wants to defend against
hostile project configs.

**Recommendation**: gate non-loopback provider `base_url` behind the
same global `Hooks.AllowProject` opt-in that already gates project-level
hooks (`internal/config/config.go:39-58`), or ship the
`safeTransport` SSRF guard on `newProviderHTTPClient` when
`base_url` is non-default.

## SSRF-005 — `models.dev` catalog fetch URL accepts override but only via CLI flag (Low)

- **Severity**: Low
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/internal/config/config_models_dev.go:77-103`
- **CWE**: CWE-918 (informational)

`FetchModelsDevCatalog(ctx, apiURL)` builds an `http.Client{Timeout:
20s}` with the **stdlib default Transport** — no SSRF guard. `apiURL`
defaults to `https://models.dev/api.json` but can be overridden by the
`dfmc config sync-models` flag plumbing. Since the override is a
CLI-only flag (not from the network or LLM tool calls), the practical
attack surface is "operator types in a private URL" — same trust level
as setting `base_url` (SSRF-004).

## SSRF-006 — `dfmc update` GitHub API client uses default transport, no SSRF guard (Low)

- **Severity**: Low
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/ui/cli/cli_update.go:157-214`
- **CWE**: CWE-918 (informational)

The `--host` flag for `dfmc update` lets the user point at a custom
GitHub API host; the resulting `http.Client.Do` calls (lines 173, 202)
have no SSRF guard. Again CLI-controlled and not exposed to LLM tool
calls — operator trust boundary.

## SSRF-007 — MCP client transport is stdio-only, no HTTP transport (Informational)

- **Severity**: Info
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/internal/mcp/client.go:35-95`

The MCP client spawns a subprocess and talks JSON-RPC over stdio
(`exec.Command(command, args...)` line 36). There is no MCP-over-HTTP
mode in the codebase — `grep "http" internal/mcp/` returns zero matches
for the import path. So MCP doesn't add an HTTP SSRF surface; the
relevant risk for MCP is RCE-via-server-config (covered in the
architecture report's "External Integrations" section, not this
playbook).

## SSRF-008 — Hooks system does no HTTP, only shell exec (Informational)

- **Severity**: Info
- **Confidence**: High
- **File:line**: `D:/Codebox/PROJECTS/DFMC/internal/hooks/hooks.go:188-241`

Hooks `runOne` invokes `exec.CommandContext` (line 248/261/263). No
`net/http` import in `internal/hooks/`. Hook command shell injection
is its own surface (mitigated by `Hooks.AllowProject` gate and
`hookOutputCap`), but it's not SSRF.

---

## Summary

The user-controlled SSRF surface (LLM-driven `web_fetch` / `web_search`)
is well-defended. The remaining SSRF-shaped surfaces are all
operator/config-controlled (provider `base_url`, `models.dev` URL,
`dfmc update --host`) and the trust boundary is a hostile project config
or a typo'd CLI flag — not an LLM-coerced URL.

The only meaningful hardening opportunity is SSRF-004: add the
`safeTransport` IP filter to `newProviderHTTPClient` when the configured
`base_url` is non-default, or gate non-loopback `base_url` behind
`Hooks.AllowProject`-style opt-in.
