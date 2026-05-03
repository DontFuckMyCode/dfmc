# sc-crypto + sc-jwt Results

## Findings

### No crypto vulnerabilities found.

All security-sensitive cryptographic operations are correctly implemented:

### Bearer Token — Constant-Time Comparison
- **File**: `ui/web/server.go:693`, `ui/cli/cli_remote_server.go:84,88`
- Uses `crypto/subtle.ConstantTimeCompare` — prevents timing side-channel attacks
- Status: **Correct**

### All Security-Sensitive IDs Use crypto/rand
- `internal/taskstore/id.go:12` — task IDs use `crypto/rand.Read()` for 6-byte suffix
- `internal/memory/store.go:4,143` — memory store IDs use `crypto/rand.Read()`
- `internal/drive/persistence.go:19,149` — drive run IDs use `crypto/rand.Read()`
- Status: **Correct**

### math/rand Only Used for Non-Security Purposes
- `internal/tools/subagent_retry.go:214` — jittered backoff in retry layer, explicitly documented as intentional
- No tokens, nonces, or passwords generated with math/rand
- Status: **Correct**

### SHA-256 for File Content Hashing
- `internal/tools/fileutil.go:4,58` — `crypto/sha256.Sum256()` for parse cache and atomic write integrity
- Appropriate use (content integrity, not password hashing)
- Status: **Correct**

### No Custom Cryptography
- DFMC delegates all crypto to Go's standard library
- No custom cipher implementations, no custom RNGs
- Status: **Correct**

### No Password Hashing
- DFMC has no user accounts, no authentication database, no password storage
- Provider API keys loaded from env vars/config, never stored hashed
- Status: **Not applicable**

---

## JWT Findings

**No JWT implementation found.**

- No JWT library in `go.mod` (no `dgrijalva/jwt-go`, `golang-jwt/jwt`, etc.)
- Grep for `jwt`, `JWT`, `JWS`, `JWE`, `jwk`, `Bearer eyJ`, `alg:`, `RS256`, `HS256`, `none` — zero matches in production code
- The only JWT-related code is `internal/security/redact.go:50` which **detects and masks** JWTs in output — it does not create or verify them
- Auth layer uses opaque bearer token with `subtle.ConstantTimeCompare` — no claims, no signatures, no JWT structure

---

## Summary

| Category | Status |
|----------|--------|
| Custom crypto | Pass — none found |
| Weak algorithms | Pass — stdlib only |
| crypto/rand for IDs | Pass — all 3 generators correct |
| Constant-time token compare | Pass |
| math/rand for security | Pass — only jitter (intentional) |
| SHA-256 usage | Pass — appropriate non-password use |
| JWT implementation | Pass — not used |
| Password hashing | Pass — not applicable |