# sc-crypto Results — DFMC

**Scanner:** sc-crypto v1.0.0
**Target:** D:\Codebox\PROJECTS\DFMC (Go 1.25, module `github.com/dontfuckmycode/dfmc`)
**Date:** 2026-04-25
**Skipped:** `bin/`, `vendor/`, `node_modules/`, `.dfmc/`, `.git/`, `security-report/`

---

## Summary

**No issues found by sc-crypto.**

The DFMC codebase contains no cryptography-misuse findings. All cryptographic primitives in production code are appropriate for their purpose, and every match for "weak-crypto-shaped" keywords resolves to one of three benign categories described under *Verification notes* below.

| Category | Count |
|---|---|
| Critical | 0 |
| High | 0 |
| Medium | 0 |
| Low | 0 |
| **Total** | **0** |

---

## Discovery summary

Phase 1 keyword sweeps across the Go source tree returned only the matches catalogued below. Each was inspected in source context.

| Pattern | Hits | Outcome |
|---|---|---|
| `crypto/md5` | 1 | Test fixture only — scanner self-test |
| `crypto/sha1` | 0 | — |
| `crypto/des`, `crypto/rc4` | 0 | — |
| `crypto/aes`, `cipher.New*` | 0 | DFMC performs no symmetric encryption |
| `"math/rand"` import | 0 | No production use |
| `rand.Read` | 2 | Both `crypto/rand` (taskstore + drive ID generation) |
| `InsecureSkipVerify` | 1 | Test fixture only — scanner self-test |
| `tls.Config` | 1 | Same test fixture |
| `http.Transport` | 2 | Both use default secure TLS — no override |
| `hmac.*`, `jwt`, `bcrypt`, `argon`, `scrypt`, `pbkdf` | 0 | DFMC stores no passwords / signs no tokens |
| `sha256` | 4 sites | All file-integrity / cache-key hashing |
| Hardcoded IV / nonce / key literals | 0 | — |
| AES-ECB / bespoke ECB | 0 | — |

---

## Verification notes (matches that look concerning but aren't)

### 1. `crypto/md5` and `InsecureSkipVerify` references

**File:** [`internal/security/astscan_test.go`](../internal/security/astscan_test.go) lines 95–113
**File:** [`internal/security/astscan_go.go`](../internal/security/astscan_go.go) lines 95–150
**File:** [`internal/langintel/go_kb.go`](../internal/langintel/go_kb.go) lines 256–266
**File:** [`internal/provider/offline_reports.go`](../internal/provider/offline_reports.go) lines 52, 103
**File:** [`ui/cli/cli_skills_data.go`](../ui/cli/cli_skills_data.go) line 273

These references are **DFMC's own security scanner** detecting weak crypto in *user* code, plus its language knowledge base teaching the model what to recommend. The MD5 import in `astscan_test.go` exists inside a Go *source string* passed to the scanner under test (`scanHelper(t, "h.go", src)`); no MD5 hashing actually happens at runtime. The `InsecureSkipVerify: true` literal on line 99 is the same pattern. These are scanner self-tests and detection rules — flagging them would be self-referential.

### 2. `rand.Read` in ID generation

**File:** [`internal/taskstore/id.go`](../internal/taskstore/id.go) line 12
**File:** [`internal/drive/persistence.go`](../internal/drive/persistence.go) line 149

Both files import `crypto/rand` (verified via line 4 and line 19 respectively), not `math/rand`. The 6-byte random suffix mixed with a Unix timestamp is suitable for non-secret task/run IDs and uses the cryptographic source.

### 3. `sha256.Sum256` in production

**File:** [`internal/tools/builtin.go`](../internal/tools/builtin.go) line 65
**File:** [`internal/tools/builtin_read.go`](../internal/tools/builtin_read.go) line 112
**File:** [`internal/tools/fileutil.go`](../internal/tools/fileutil.go) line 58
**File:** [`internal/tools/engine.go`](../internal/tools/engine.go) lines 680–683

All four sites compute SHA-256 over **file content** for the read-before-mutation gate (`content_sha256` is the cache key the engine compares to detect concurrent modification). This is integrity hashing, not password storage or message authentication. SHA-256 is the correct choice — and the gate is documented in `CLAUDE.md` as the strict-read-gate mechanism.

### 4. `http.Transport` instances

**File:** [`internal/provider/http_client.go`](../internal/provider/http_client.go) line 50
**File:** [`internal/tools/web.go`](../internal/tools/web.go) line 24

Neither transport sets `TLSClientConfig`, so both inherit the Go stdlib default (system CA pool, modern TLS, full certificate validation). The `web.go` transport additionally enforces an SSRF guard at dial time — no TLS knobs touched. No `MinVersion` is set, so Go's default minimum (TLS 1.2 since Go 1.18, TLS 1.2 by-policy since 1.22) applies. This is correct — and explicit pinning is unnecessary on Go 1.25.

### 5. JWT / HMAC / password hashing

DFMC has no authentication system of its own. The web UI (`dfmc serve`) binds to `127.0.0.1:7777` and trusts the local user; there are no passwords to hash, no tokens to sign, no JWTs to verify, no symmetric encryption keys to manage. The `secret_key` / `client_secret` keyword matches in [`ui/web/server_admin.go`](../ui/web/server_admin.go) and [`ui/cli/cli_config.go`](../ui/cli/cli_config.go) are **redaction allowlists** — they control which config keys get masked in admin output, not stored secrets.

---

## CWE coverage cleared

- **CWE-327 (Broken / Risky Crypto Algorithm):** No DES, RC4, MD5, or SHA1 in security context.
- **CWE-328 (Reversible / Weak Hash for Password Storage):** No password storage in DFMC.
- **CWE-329 (Generation of Predictable IV):** No symmetric encryption performed.
- **CWE-330 / CWE-338 (Insufficient Randomness / Weak PRNG for Security):** Both random ID sites use `crypto/rand`.
- **CWE-295 (Improper Certificate Validation):** No `InsecureSkipVerify` in production; default TLS config used.
- **CWE-310 (Cryptographic Issues — Key Management):** No hardcoded keys; no key material in tree.
- **CWE-326 (Inadequate Encryption Strength):** No bespoke ciphers; no encryption performed.

---

## Recommendation

**None.** The codebase's cryptographic posture is appropriate for its threat model: a local single-user developer tool that hashes file content for integrity, generates non-secret IDs with a CSPRNG, and makes outbound HTTPS calls under default-secure TLS. Continue this pattern — if a future feature introduces stored credentials, signed sessions, or at-rest encryption, route the work through `golang.org/x/crypto` (`bcrypt`, `argon2`, `nacl/secretbox`) rather than rolling primitives.

— sc-crypto, clean
