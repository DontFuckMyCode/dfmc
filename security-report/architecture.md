# Architecture — Security Perspective

## Trust Model

DFMC is designed as a **personal developer tool** running on a single user's workstation. The trust model assumes the operator is the sole user and has full control over the machine. This shapes the security posture: file permissions protect the store, loopback-only binding protects the web server, and config file permission checks are advisory rather than mandatory.

**DFMC does NOT currently support:**
- Multi-tenant deployments (no per-client isolation)
- Unencrypted bbolt at rest (appropriate for single-user local use)
- Credential rotation (static bearer token, no refresh/invalidation endpoint)

## Security Layers

### Layer 1: Entry Point Authentication
| Entry | Auth Method | Notes |
|-------|-------------|-------|
| CLI | None (local operator) | Direct execution |
| TUI | None (local operator) | Direct execution |
| Web `auth=token` | Bearer token (constant-time compare) | Single static token |
| Web `auth=none` | None — loopback only enforced | Warning printed |
| MCP stdio | None (local only) | stdio not network-exposed |
| Remote `dfmc remote` | gRPC token or mTLS | Configurable |

### Layer 2: Tool Approval Gate
All tool calls (user and agent) funnel through `executeToolWithLifecycle`:
```
askToolApproval (if non-user + gated)
  -> fire pre_tool hooks
  -> executeToolWithPanicGuard
  -> fire post_tool hooks
```

`RequireApproval` and `RequireApprovalNetwork` control which tools require explicit user approval. Default: `RequireApprovalNetwork=["*"]` (all network-originated calls require approval).

### Layer 3: Hook Execution
Hooks run with:
- Minimal env (scrubbed of API keys)
- Hard timeout (30s default)
- Process-group kill (`Setpgid`)
- Output cap (1 MiB per stream)
- Panic containment (defer/recover)

### Layer 4: File System Isolation
- `EnsureWithinRoot` dual-layer: lexical `isPathWithin` + `filepath.EvalSymlinks`
- `read_file` / `write_file` / `edit_file` / `apply_patch` all go through this gate
- read-before-mutation: hash-checked snapshots for `write_file`/`apply_patch`, snapshot-only for `edit_file`

### Layer 5: Network Isolation
- `web_fetch` SSRF guard: blocks loopback (127.0.0.0/8, ::1), private (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16), link-local (169.254.0.0/16)
- DNS resolution happens before the IP check (no DNS rebinding window)
- `X-Forwarded-For` only trusted when direct connection is from a loopback/CIDR trusted proxy

## Attack Surface Summary

| Surface | Risk Level | Primary Control |
|---------|------------|-----------------|
| Hook injection | High (if config compromised) | Config permission check |
| bbolt at rest | Medium (physical access) | OS-level disk encryption |
| Web SSE stream | Medium (local process) | Loopback-only bind |
| WS event drops | Low (by design) | Documentation |
| Intent fail-open | Low (intentional) | Main model still gated |
| Config permission | Medium (advisory only) | File permissions |

## Data Flows

```
User Input -> CLI/TUI/Web/MCP -> Intent Layer (sub-LLM) -> Engine.Ask
                                                          |
                                                     Tool Calls
                                                          |
                              +-------------+-------------+-------------+
                              |             |             |             |
                         executeToolWithLifecycle (approval gate + hooks)
                              |             |             |             |
                         Tools.Engine -> [read_file, write_file, grep,
                                          run_command, web_fetch, etc.]
                              |
                         OS syscalls (file, network, process)
```

## Secrets Surface

| Secret | Where Stored | Where Used | Exfiltration Risk |
|--------|-------------|-----------|-------------------|
| API keys | env vars, config.yaml | Provider clients | Scrubbed from hooks/MCP env |
| Bearer token | env (DFMC_WEB_TOKEN) | bearerTokenMiddleware | Not scrubbed from own env |
| bbolt data | ~/.dfmc/data/*.db | All subsystems | Physical access only |
| Conversation logs | ~/.dfmc/data/artifacts/ | Conversation manager | Same as bbolt |
| Hook output | /tmp or hook stdout | Hook system | Owner-readable only |

## Intentional Trade-offs

1. **Fail-open intent layer**: Intent classification errors fall back to `Fallback(raw)` rather than blocking. This prevents the intent layer from blocking the engine but could cause a confused classifier to misroute a prompt.

2. **WS event channel lossy delivery**: The SSE/WebSocket event channel uses a 64-element buffer with a `default` drop. This prevents slow consumers from blocking the engine but means some events may be silently dropped during high-throughput loops.

3. **Config permission advisory**: Group/world-writable config warnings fire but do not block loading. This prevents accidental breakage but allows a co-tenant with file write access to inject hooks.

4. **Single-tenant web serve**: One engine instance shared across all authenticated clients. Appropriate for personal use, unsuitable for multi-user hosting.
