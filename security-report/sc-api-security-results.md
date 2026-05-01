# sc-api-security Results

No issues found by sc-api-security.

## Surfaces audited

OWASP API Security Top 10 (2023) review of the DFMC HTTP+SSE+WebSocket
surface served by `dfmc serve` (default `127.0.0.1:7777`) and the
parallel `dfmc remote start` server (`7779`). Out of scope: `dfmc mcp`
(stdio JSON-RPC, no API in the OWASP-API sense), reserved gRPC port
7778 (unimplemented).

## API-Top-10 walkthrough

| Item | Verdict | Code / Cross-ref |
|---|---|---|
| **API1: BOLA** | N/A — single principal | `sc-authz-results.md` |
| **API2: Broken Authentication** | Clean — opaque bearer, constant-time compare, loopback default | `sc-auth-results.md`, `ui/web/server.go:661-679` |
| **API3: Broken Object Property Level Authorization** | Clean — every handler maps DTO → domain field by field with allowlists | `sc-mass-assignment-results.md`; `ui/web/server_task.go:178-233` |
| **API4: Unrestricted Resource Consumption** | Clean — multi-layer rate/size/time limits | `sc-rate-limiting-results.md` |
| **API5: Broken Function Level Authorization** | Clean — non-`user` sources hit the approval gate via `executeToolWithLifecycle` | `sc-privilege-escalation-results.md`; `internal/engine/engine_tools.go:290-388` |
| **API6: Unrestricted Access to Sensitive Business Flows** | Clean — Drive runs go through approval gate; concurrency-capped | `sc-rate-limiting-results.md`; `internal/drive/` |
| **API7: SSRF** | Out of scope here — see `sc-ssrf-results.md` |
| **API8: Security Misconfiguration** | Clean — security headers (CSP/XCTO/XFO), host/origin allowlists, content-type enforcement, rate limits all on by default | `ui/web/server.go:128-394` |
| **API9: Improper Inventory Management** | Clean — single versioned prefix `/api/v1/`; no v0/v2 ghosts; admin endpoints live under `/api/v1/{scan,doctor,hooks,config}` not a separate prefix | `ui/web/server.go:300-363` |
| **API10: Unsafe Consumption of APIs** | LLM provider responses are parsed defensively (provider clients in `internal/provider/`); tool-call shapes unified by `stream_unify.go`; `_reason` virtual field stripped before tool execute | architecture-report §4 |

## HTTP method correctness

Reviewed `setupRoutes` (`ui/web/server.go:300-363`). Verified:

- **No GET mutations.** Every state-changing route uses
  `POST`/`PATCH`/`DELETE`. GETs return JSON only.
- **Unsafe methods route through content-type enforcement.** All
  `POST`/`PATCH`/`PUT` body-bearing requests must declare
  `application/json` or get 415 (`ui/web/server.go:469-505`).
- **`DELETE` accepts no body**: `handleTaskDelete`,
  `handleDriveDelete` read only path params.
- **PATCH semantics:** `handleTaskUpdate` applies a partial update
  via allowlist (`ui/web/server_task.go:178-233`).

## Error leakage

Every handler returns errors as `{"error": err.Error()}`. Verified
the underlying error paths:

- `json.NewDecoder().Decode(&req)` errors are JSON-parse messages
  ("unexpected EOF", "invalid character"). No internal type info or
  stack trace.
- File-system errors go through `os.PathError` whose `Error()`
  string includes the path the caller already supplied — no
  additional path leakage. Path containment errors return
  generic strings ("path escapes project root via symlink",
  `ui/web/server_files.go:194-203`).
- Tool errors come from `tools.Engine.Execute` and are sanitized at
  the tool layer (`missingParamError` etc., `internal/tools/builtin.go`).
- Engine errors are mapped to specific HTTP codes — not all 500.
  Notable: `apply_patch` "denied" → 403 (`ui/web/server_workspace.go:103`),
  task validation errors → 400 (`ui/web/server_task.go:240-247`),
  drive resume on terminal run → 409 (`ui/web/server_drive.go:248-253`).
- **No stack traces in HTTP responses.** The panic guard in
  `executeToolWithPanicGuard` keeps stacks server-side
  (`internal/engine/engine_tools.go:246-269`); the recovered error
  string is "tool X panicked: <recover-value>" + truncated stack —
  this is intentionally surfaced to the LLM (so the model can
  retry/avoid the bad path) but it's still bounded by
  `truncateStackForError`. For the HTTP path the same string is
  returned, but contains only Go runtime frames, not configuration
  values.

## Versioning / inventory hygiene

- One version prefix: `/api/v1/`. No `/api/v0`, no `/v2`. No
  alternative routes for the same resource.
- Workbench HTML at `/` — same-origin, served once at startup,
  embedded via `//go:embed`.
- `/healthz` — auth-bypass, returns `{"status":"ok"}` only. Does
  NOT leak version or git commit.
- `/api/v1/status` and `/api/v1/doctor` DO surface engine state
  (provider, model, hook inventory). Auth-gated (`bearerTokenMiddleware`)
  in `auth=token` mode; loopback-only in `auth=none` mode. Acceptable
  under the threat model.

## Verifications

1. Confirmed every handler decodes into a request DTO (see
   `sc-mass-assignment-results.md`), never directly into a domain
   type.
2. Confirmed routes use Go 1.22+ method-prefixed `mux.HandleFunc`
   syntax — the same path with a different method returns 405 from
   the stdlib mux, not a silent fall-through.
3. Confirmed no `r.URL.Path`, no `r.URL.RawQuery`, no
   `r.Header.Get` value is ever reflected into a response body
   without sanitisation (cross-checked in `sc-xss-results.md` /
   `sc-header-injection-results.md`).

## Why this is "no issues, not just N/A"

The DFMC HTTP API is small (54 routes), versioned, methodically
guarded by a single middleware chain, and fully tested. Each OWASP
API Top 10 item maps either to a concrete implemented control or to
a documented threat-model carve-out (single-user / loopback). The
controls compose — body-size limit + content-type enforcement +
rate limit + bearer middleware + host allowlist + WS conn-limiter —
so a single bypass on one layer doesn't topple the rest.
