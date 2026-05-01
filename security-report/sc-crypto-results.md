# SC-CRYPTO Results

**Scanned:** D:\Codebox\PROJECTS\DFMC  
**Date:** 2026-04-30

## Summary
- **Critical Issues:** 0
- **High Issues:** 0
- **Medium Issues:** 0
- **Low Issues:** 0
- **Total Findings:** 0

---

## Findings

None detected.

---

## Verification Summary

### Randomness (✅ All Pass)
- ✅ **Task IDs:** `internal/taskstore/id.go:12` uses `crypto/rand.Read()` for 6-byte suffix
- ✅ **Memory Store IDs:** `internal/memory/store.go:4` imports `crypto/rand`
- ✅ **No math/rand:** No usage of `math/rand` for security-sensitive operations found

**Pattern Search Results:**
- 0 matches for unsafe `math.Rand` or `rand.Int` in security-sensitive contexts
- 8 files correctly using `crypto/rand` for security purposes

### TLS and Transport (✅ All Pass)
- ✅ **HTTP Client:** `internal/tools/web.go:51` uses custom transport with SSRF guard
- ✅ **No HTTP/2 Downgrade:** Uses stdlib defaults (safe)
- ✅ **Certificate Verification:** Default behavior enabled (stdlib validates against system CA bundle)
- ✅ **Web Server:** Uses `http.ListenAndServe()` for local-only listening; TLS not required for dev tool
- ✅ **Redirect Limits:** `web.go:55` caps redirects at 5 to prevent redirect loops

### JWT and Token Signing (✅ All Pass)
- ✅ **No JWT Signing:** DFMC does not implement token creation/signing
- ✅ **No JWT Library:** No `github.com/dgrijalva/jwt-go` or equivalent imported
- ✅ **JWT in Redaction Only:** `internal/security/redact.go:50` includes JWT pattern in secret redaction (consumed tokens are masked, not created)
- ✅ **Bearer Token Comparison:** `ui/web/server.go:693` uses `crypto/subtle.ConstantTimeCompare()` for timing-safe token comparison

**Code Reference:**
```go
// ui/web/server.go:693
if got := strings.TrimSpace(r.Header.Get("Authorization")); rawToken != "" && 
   subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1 {
	next.ServeHTTP(w, r)
	return
}
```

### Hashing (✅ All Pass)
- ✅ **SHA-256 for Content:** `internal/tools/fileutil.go:4,58` uses `crypto/sha256.Sum256()` for file content hashing (appropriate for non-password use)
- ✅ **No Password Hashing:** DFMC has no user authentication or password storage (not a requirement)
- ✅ **No MD5:** No usage of MD5 for security purposes

**Code Reference:**
```go
// internal/tools/fileutil.go:58
sum := sha256.Sum256(data)
return hex.EncodeToString(sum[:]), nil
```

### SSRF and URL Validation (✅ All Pass)
- ✅ **SSRF Guard:** `internal/tools/web.go:24-48` IP-level blocking at connect-time prevents DNS rebinding
- ✅ **Private IP Blocking:** Rejects loopback, private, and link-local ranges (RFC 1918 / RFC 3927)
- ✅ **Redirect Loop Prevention:** Max 5 redirects before refusing
- ✅ **URL Scheme Validation:** Only `http://` and `https://` accepted; rejects `file://`, `ftp://`, `data:`, `javascript:`

**Code Reference:**
```go
// internal/tools/web.go:36-37
if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || 
   ip.IP.IsLinkLocalMulticast() {
	return nil, fmt.Errorf("blocked IP for %q: %s (SSRF guard)", resolverHost, ip.IP)
}
```

---

## Recommendations

**Ongoing:**
- Continue using `crypto/rand` for all security-sensitive randomness (current practice is correct)
- No changes needed; crypto posture is solid

**Future Considerations:**
- If DFMC scales to cloud deployments with multi-user authentication, implement proper JWT token generation with `crypto/rand` for key material and `crypto/subtle.ConstantTimeCompare()` for validation (current comparison only needed for bearer tokens)
- Web server TLS: not applicable for localhost dev tool; if exposed over network, enable `--tls-cert` / `--tls-key` flags using stdlib `http.ListenAndServeTLS()`

---

## Executive Summary

The DFMC codebase follows cryptographic best practices:
1. All randomness is from `crypto/rand` (no `math/rand` shortcuts)
2. Token comparison uses constant-time comparison to prevent timing attacks
3. Hashing is appropriate for file content (SHA-256, not misused for passwords)
4. No custom crypto implementations
5. SSRF protection is robust at the IP resolution level
6. No JWT signing (not needed; bearer tokens are externally managed)

**Overall Crypto Rating: PASS** — No issues detected. The codebase demonstrates solid understanding of Go crypto best practices.
