# sc-rate-limiting Results

No issues found by sc-rate-limiting.

## Layered limits in place

| Layer | Limit | Code |
|---|---|---|
| Per-IP HTTP (every endpoint) | 30 req/s, burst 60, token-bucket via `golang.org/x/time/rate` | `ui/web/server.go:386-389, 510-572` |
| Per-IP bucket GC | Stale buckets evicted every 10 min | `ui/web/server.go:546-560` |
| Trusted-proxy XFF | XFF only honored when direct peer is a trusted proxy (`127.0.0.1`/`localhost`/`::1` by default; CIDR supported); rightmost IP wins (VULN-010 fix) | `ui/web/server.go:585-634` |
| Body-size cap | 4 MiB on POST/PUT/PATCH/DELETE via `http.MaxBytesReader` | `ui/web/server.go:420-438` |
| Header-size cap | 1 MiB | `ui/web/server.go:401, 416` |
| Read-header timeout | 5 s — closes Slowloris pre-body | `ui/web/server.go:397, 412` |
| Read timeout | 30 s | `ui/web/server.go:398, 413` |
| Write timeout | 2 min on non-streaming; cleared per-handler for SSE/WS | `ui/web/server.go:399, 414`; `ui/web/server_chat.go:67, 123, 199-201` |
| Idle timeout | 2 min | `ui/web/server.go:400, 415` |
| WS connection cap (global) | 64 | `ui/web/server_ws.go:41, 83-134` |
| WS connection cap (per-IP) | 8 | `ui/web/server_ws.go:42, 99-134` |
| WS frame size | 64 KiB per frame | `ui/web/server_ws.go:48, 233` |
| WS read deadline | 60 s, slid by pong handler | `ui/web/server_ws.go:53-58, 253-256` |
| WS ping interval | 30 s | `ui/web/server_ws.go:58, 471-490` |
| WS per-connection inbound | 5 rps, burst 10 | `ui/web/server_ws.go:66-67, 245, 270-272` |
| LLM provider 429 throttle | Per-provider exponential backoff in `internal/provider/throttle.go` | `internal/provider/` |
| Agent loop budget | `max_tool_steps=60`, `max_tool_tokens=250000`, `meta_call_budget=64`, `meta_depth_limit=4`, `parallel_batch_size=4` | `internal/config/defaults.go`, `internal/engine/agent_loop_native.go` |
| Drive concurrency | Global cap on parallel autonomous runs (`drive.IsActive` registry) | `internal/drive/` |
| Drive list/task list pagination cap | `taskListLimitMax = 500` (server.go:402); drive list capped at 1000 (`server_drive.go:158`) | server_task.go:68-72, server_drive.go:155-160 |
| `/api/v1/files` listing cap | 500 default, 2000 max | `ui/web/server_files.go:30-34` |
| Memory listing cap | 1000 max | `ui/web/server_tools_skills.go:138-141` |
| Hook output | 1 MiB per stream (stdout + stderr) | `internal/hooks/hooks.go:220` |
| Hook timeout | 30 s default per hook | `internal/hooks/hooks.go:110, 234-262` |

## Brute-force resistance on the bearer token

- 30 rps per-IP cap (60-burst) limits a brute-force loop to
  ~2.6M requests/day per source IP. Combined with the bearer token's
  expected entropy (operator-chosen `DFMC_WEB_TOKEN`; recommended ≥32
  hex chars by docs), even a sustained attack against a 64-bit token
  has expected time-to-hit > 10^9 years.
- `subtle.ConstantTimeCompare` blocks the timing oracle that would
  otherwise leak the token byte-by-byte (`ui/web/server.go:673`).
- 401 responses do not differentiate between "no token sent",
  "wrong token", and "wrong header shape" — same JSON payload for
  all three (`ui/web/server.go:677`). No oracle.

## Application-layer DoS surfaces verified bounded

1. `/api/v1/chat` (SSE) — write deadline cleared but bounded by
   provider response; cancel-on-disconnect via `r.Context()` cuts
   the upstream provider call when client drops.
2. `/api/v1/ws` long-lived connection — global+per-IP caps,
   per-connection rate limit, deadlines + ping/pong.
3. `/api/v1/drive` — `MaxParallel` capped at 1000, `AutoApprove`
   slice capped at 5000 (VULN-034); request body 4 MiB.
4. `/api/v1/tasks` listing — `taskListLimitMax = 500` (VULN-042).
5. `/api/v1/files` listing — `min(n, 2000)` server-side floor.
6. Workspace `apply` — patches go through git's own size handling
   plus 4 MiB body cap; `applyTimeout = 60s` on the spawned `git
   apply`.

## Adjacent controls

- **Per-IP bucket GC** prevents the per-IP map from growing
  unbounded if a botnet rotates IPs (the bucket leaks for 10 min,
  then is reaped).
- **Trusted-proxy gating on XFF** (VULN-010) closes the bypass where
  an attacker rotates the XFF header per request to reset their
  bucket. Verified by `ui/web/server_ratelimit_test.go`.

## Why this is "no issues, not just N/A"

Every documented application-layer DoS surface is bounded at
multiple levels (connection count, request count, body size, time).
Bearer-token brute force is gated by per-IP rate plus token entropy.
Per-IP buckets are GC'd. The XFF bypass class is closed and pinned.
The only "limit" that's loose by design is the streaming write
deadline on SSE/WS — which would otherwise close legitimate
long-lived chat streams — and even that is bounded by per-connection
inbound rate, ping/pong deadline, and connection-count caps.
