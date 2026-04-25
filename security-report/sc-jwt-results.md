# sc-jwt — JWT Implementation Flaws

**Target:** D:\Codebox\PROJECTS\DFMC (Go 1.25)
**Date:** 2026-04-25
**Result:** No issues found by sc-jwt — no JWT usage detected.

## Rationale

DFMC does not implement, issue, parse, or verify JWTs. All authentication paths use static shared-secret bearer tokens compared in constant time, not JWTs.

## Evidence

### 1. No JWT libraries in dependency graph
Searched `go.mod` and `go.sum` for `jwt`, `JWT`, `jose`, `jwx`. Zero matches.

Common Go JWT libraries explicitly NOT present:
- `github.com/golang-jwt/jwt` — absent
- `github.com/lestrrat-go/jwx` — absent
- `github.com/square/go-jose` / `gopkg.in/square/go-jose.v2` — absent
- `github.com/dgrijalva/jwt-go` — absent
- `github.com/cristalhq/jwt` — absent

### 2. No token-validation paths
Searched all `*.go` files for `jwt`, `jsonwebtoken`, `jose`, `Bearer`, `Authorization`. Every `Authorization: Bearer` site uses static shared-secret comparison, not JWT decoding/verification:

- `ui/web/server.go:400-413` — `requireAuth` middleware: builds `expected := "Bearer " + rawToken` and compares with `subtle.ConstantTimeCompare`. No parsing, no claims, no signature.
- `ui/cli/cli_remote_server.go:66-83` — identical static-token pattern for the gRPC/WS sidecar.
- `ui/cli/cli_remote_client.go`, `ui/cli/cli_remote_drive.go` — client side, just sets `Authorization: Bearer <token>` from local config.
- `internal/provider/openai_compat.go:110,216`, `internal/provider/alibaba_test.go` — outbound calls to LLM provider APIs (OpenAI/Alibaba/etc.), DFMC is the client passing its own API key. Out of scope for sc-jwt.

### 3. JWT-shaped strings appear only in the secrets scanner
- `internal/security/scanner.go:243` — regex `eyJ[...].eyJ[...].[...]` flags leaked JWTs in OTHER people's code being scanned. Detection-only; no decode or verify.
- `internal/security/astscan_credentials.go:296` — `"eyJ"` heuristic for the same leak-detection purpose.

### 4. Architecture confirms no auth backend
Per `CLAUDE.md` and `security-report/architecture.md`, DFMC is a localhost-by-default code-intelligence binary. Its only HTTP surfaces (`dfmc serve` on :7777, `dfmc remote start` on :7778/7779) accept an optional shared-secret token configured by the operator. No user accounts, no sessions, no token issuance, no third-party identity provider integration — therefore no JWT attack surface (alg=none, RS256→HS256 confusion, weak HMAC, missing exp/aud/iss, kid injection, JWK injection, localStorage theft) applies.

## Findings

None.

## Confidence

100 — verified by absence in dependency manifest, absence of JWT decode/verify call sites, and architectural fit (localhost tool, no auth backend).
