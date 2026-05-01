# sc-jwt Results

No issues found by sc-jwt — **DFMC does not use JWTs anywhere**.

## Verifications

1. **Module audit:** `go.mod` has no JWT library. Grep for the common
   names — `jwt`, `jose`, `paseto`, `golang-jwt`, `dgrijalva`,
   `lestrrat-go`, `go-jose` — across `go.mod` and `go.sum` returns no
   matches.
2. **Source audit:** Grep across the entire repository for
   `jwt`, `JWT`, `JWS`, `JWE`, `jwk`, `Bearer eyJ` (the canonical
   JWT prefix), `alg":"`, `RS256`, `HS256`, `none` (algorithm) — no
   JWT-shaped artifacts.
3. **Auth surface:** The only bearer-style auth is the static
   `DFMC_WEB_TOKEN` opaque string compared via
   `subtle.ConstantTimeCompare` (`ui/web/server.go:661-679`). It is
   not a JWT — it has no header, no claims, no signature, and no
   expiry semantics encoded in the value.
4. **MCP/CLI:** MCP uses stdio with no auth; the CLI uses the same
   opaque bearer token as the web surface. Neither uses JWT.
5. **LLM providers:** Anthropic uses `x-api-key`, OpenAI-compatible
   providers use `Authorization: Bearer <opaque>`, Google uses an
   API key in a query param. None of these are JWTs from the
   provider's perspective; even if a future provider switched to
   signed tokens, DFMC treats them as opaque secrets and never parses
   them.

## Risk

There is no JWT library to be vulnerable to (`alg:none` confusion,
RS/HS algorithm confusion, key-confusion, kid-injection, JWK header
abuse, expired-token replay, signing-key leakage). The category is
not applicable.

## When this finding becomes obsolete

If a future contributor adds `golang-jwt/jwt/v5` (or any JWT
library) to `go.mod`, this report MUST be re-run. JWT
implementations are infamously easy to misuse and the controls
needed (algorithm allowlist, key rotation, replay protection,
audience validation) are nontrivial. Recommended posture: stay
opaque-bearer-only unless a concrete protocol need (federation,
short-lived auth, OIDC) requires JWT.
