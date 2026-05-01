# SC-Privilege Escalation Security Scan Results

**Date:** 2026-04-30  
**Project:** DFMC  
**Total Findings:** 0  

## Summary

No privilege escalation vulnerabilities detected in DFMC.

### Verification Findings

#### Privilege Model 1: Tool Approval Gate
**File:** `internal/engine/engine_tools.go`  
**Status:** SECURE ✓ (No Role Manipulation)

**Design:** DFMC is single-user with a simple approval model:
- **User-initiated calls:** Auto-allowed (explicit user consent via CLI/TUI)
- **Agent/network-initiated calls:** Require approval (gated tools like `run_command` need human consent via `DFMC_APPROVE=yes`)

**No escalation path:**
- No JWT role claims (no token roles to forge)
- No request body role field that user can set
- No user registration endpoint (single operator)
- No admin/user role hierarchy (single-user)

#### Privilege Model 2: Source-Based Access Control
**File:** `internal/engine/engine_source.go`  
**Status:** SECURE ✓

```go
type Source string

const (
    SourceUser Source = "user" // TUI/CLI real user input; always allowed
    SourceWeb  Source = "web"
    SourceWS   Source = "ws"
    SourceMCP  Source = "mcp"
    SourceCLI  Source = "cli" // dfmc tool run — operator's own tooling
)
```

**Verification:**
- **Source determination:** Assigned by entry point (`CallTool` → SourceUser, `CallToolFromSource(web)` → SourceWeb) (engine_tools.go:159, 188)
- **No user tampering:** Source cannot be overridden by HTTP/WS client (embedded in engine, not from request)
- **Approval enforcement:** Applied per-source by `engine.Approver` callback (CLAUDE.md verified)

#### Privilege Model 3: No Default Admin Accounts
**Status:** SECURE ✓ (Not Applicable)

DFMC has no user accounts, authentication database, or admin/regular user distinction. No seeds, migrations, or default credentials.

#### Privilege Model 4: No Configuration Role Tampering
**File:** `internal/config/`  
**Status:** SECURE ✓

Configuration is loaded from:
- Global: `~/.dfmc/config.yaml` (user-controlled)
- Project: `.dfmc/config.yaml` (developer-controlled)
- Environment: `.env` (developer-controlled)
- CLI flags: (operator provides)

No user-supplied fields (`request.body`, URL params, HTTP headers) can modify roles or approval settings. All auth decisions are enforced server-side.

#### Privilege Model 5: Drive Auto-Approve Scoping
**File:** `ui/cli/cli_mcp_drive.go:80, 183, 213`  
**Status:** SECURE ✓

```go
"auto_approve":     map[string]any{"array": "description": "Tool names to auto-approve for this run (use ['*'] for all)", "items": map[string]any{"type": "string"}},
```

and in drive config:
```go
AutoApprove: args.AutoApprove,
```

**Verification:**
- **Config-supplied only:** Auto-approve list is specified in MCP call or HTTP request (not user-controllable at runtime)
- **Scoped per-run:** Auto-approve applies only to the drive run that specified it; not global privilege escalation
- **Explicit opt-in:** Operator must call `dfmc_drive_start` with `auto_approve: ["*"]` or list specific tools; defaults to require-approval

#### Privilege Model 6: No Shell Interpretation in Hooks
**File:** `internal/hooks/hooks.go:348-389`  
**Status:** SECURE ✓ (Env Value Sanitization)

```go
// sanitizeEnvValue quotes the value so that shell expansion cannot break out
// of the env-var assignment. Unix uses single-quote wrapping with embedded
// quote escaping (' -> '\''); Windows cmd.exe uses double-quote wrapping with
// % doubling (%%) to block %VAR% expansion inside quoted strings...
func sanitizeEnvValue(raw string) string {
    if raw == "" {
        return "''"
    }
    if runtime.GOOS == "windows" {
        // cmd.exe expands %VAR% inside double quotes, so escape % as %%.
        // Also escape " and \ to prevent quote-breakout and path interpretation.
        var b strings.Builder
        b.Grow(len(raw) * 2)
        for _, r := range raw {
            switch r {
            case '%':
                b.WriteString("%%")
            case '"':
                b.WriteString("^\"")
            case '\\':
                b.WriteString("^\\")
            // ...
        }
        return "\"" + b.String() + "\""
    }
    // Unix: single quotes prevent all $ expansion. Escape embedded single
    // quotes as '\'' (close, escaped ', reopen).
    var b strings.Builder
    b.Grow(len(raw) + 4)
    b.WriteByte('\'')
    for _, r := range raw {
        if r == '\'' {
            b.WriteString("'\\''")
        } else {
            b.WriteRune(r)
        }
    }
    b.WriteByte('\'')
    return b.String()
}
```

**Verification:**
- **Shell-safe quoting:** Unix single-quotes + escape, Windows double-quotes + percent-escape (line 348-389)
- **No eval in hook dispatch:** Hooks are shell-wrapped only when `useShell=true` (line 288), otherwise argv array (line 289)
- **Payload sanitization:** All event payloads projected to env as `DFMC_<KEY>=<sanitized_value>` (line 311)

No privilege escalation via hook environment injection.

### False Positives Cleared

- No request body role field (single-user)
- No JWT role tampering (no JWT; fixed bearer token)
- No admin endpoint without auth (all endpoints protected by `bearerTokenMiddleware`)
- No test/fixture admin accounts in production (single-user, no user model)
- No path-based access control bypass (`..` traversal rejected by `EnsureWithinRoot()`)

## Conclusion

**Risk Level:** LOW  
DFMC has no role hierarchy or privilege escalation surface. Single-user model with source-based approval gate prevents network-initiated privilege amplification. No roles, users, or authentication database means no tampering surface.

