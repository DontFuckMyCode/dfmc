# sc-path-traversal + sc-ssrf Results

## Findings

### [Medium] SSRF: DNS Rebinding Window in safeTransport

- **File**: `internal/tools/web.go:35-46`
- **Description**: `safeTransport.DialContext` resolves DNS via `LookupIPAddr` *before* the loopback/private check, then iterates over all resolved IPs trying to dial each with a 10-second timeout. If a hostname resolves to many IPs and most are blocked, the loop spends ~10s per blocked IP — an attacker who registers a hostname that sometimes resolves to a public IP and sometimes resolves to `169.254.169.254` (AWS metadata) could induce a timing side-channel that reveals which response type occurred.
- **Impact**: A hostname with variable DNS responses (round-robin / anycast / CDN) could produce observable timing differences between hits resolving publicly vs. to blocked IPs. Attacker cannot exfiltrate data but could confirm whether a target resolves to internal infrastructure.
- **Evidence**:
  ```go
  ips, err := net.DefaultResolver.LookupIPAddr(ctx, resolverHost)
  for _, ip := range ips {
      if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ... {  // block check first
          return nil, fmt.Errorf("blocked IP for %q: %s", resolverHost, ip.IP)
      }
  }
  for _, ip := range ips {  // dial happens here — 10s per IP
      conn, err := net.DialTimeout(network, net.JoinHostPort(ip.IP.String(), port), 10*time.Second)
  ```
- **Mitigation**: Fail fast when all resolved IPs are blocked, or add a global timeout across all dial attempts. Consider randomizing IP iteration order to prevent timing oracle.

### [Low] Path Handling: No Explicit Null Byte Rejection

- **File**: `internal/tools/engine.go:920` (`EnsureWithinRoot`), `ui/web/server_files.go:165` (`resolvePathWithinRoot`)
- **Description**: Neither function explicitly checks for `0x00` (null byte) in the input path. Go's `filepath.Abs`, `filepath.Join`, and `filepath.Clean` all reject null bytes implicitly, but there is no *explicit* defensive check — if a future refactor introduces string manipulation before the filepath calls, the null byte guard could be silently lost.
- **Impact**: Low — stdlib is safe, but defense-in-depth gap exists.
- **Evidence**: `EnsureWithinRoot` only checks `strings.TrimSpace(path) == ""` before filepath calls; no null byte check.
- **Mitigation**: Add explicit null-byte check before any filepath operations:
  ```go
  if strings.Contains(path, "\x00") {
      return "", fmt.Errorf("path contains forbidden character (null byte)")
  }
  ```

### [Low] SSRF: isBlockedHost Best-Effort DNS Lookup for Search Results

- **File**: `internal/tools/web.go:66-82`
- **Description**: `isBlockedHost` is display-filter only (not a security boundary), confirmed by inline comment at line 62-65. It does DNS lookups on untrusted hostnames from DuckDuckGo result pages — acceptable for its intended purpose.
- **Impact**: None directly — actual SSRF boundary is `safeTransport.DialContext`.
- **Mitigation**: No action needed; document clearly if code evolves.

---

## No Issues Found

### Path Traversal — all file tools correctly use EnsureWithinRoot
- **read_file** (`builtin_read.go:37`): `EnsureWithinRoot` before any file access
- **write_file** (`builtin.go:39`): `EnsureWithinRoot` with per-path locking and hash-verified overwrite
- **edit_file** (`builtin_edit.go:52`): `EnsureWithinRoot` with TOCTOU guard (per-path lock)
- **apply_patch** (`apply_patch.go:86-90`): `filepath.Clean` before `EnsureWithinRoot`, per-target read-before-mutate gate
- **list_dir** (`builtin_list.go:43`): `EnsureWithinRoot`
- **disk_usage** (`disk_usage.go:73`): `EnsureWithinRoot`
- **web server file API** (`server_files.go:58`): `resolvePathWithinRoot` (equivalent two-layer check)

### EnsureWithinRoot Two-Layer Check
1. **Lexical layer**: `filepath.Abs` + `filepath.Rel` — rejects `..` prefix
2. **Symbolic layer**: `filepath.EvalSymlinks` on both root and target, with fallback to `resolveExistingAncestor` for non-existent write targets

Tests in `path_test.go` cover: subpath, `../` traversal, absolute outside root, non-existent write targets, symlink escape, internal symlink, new file under symlinked escape, empty path.

### SSRF — web_fetch URL Validation
- **Scheme enforcement** (`web.go:114`): only `http` and `https` accepted; `javascript:`, `data:`, `file:`, `ftp:` rejected at parse time
- **safeTransport** (`web.go:24-48`): DNS resolution at connect time, IP-level loopback/private/link-local check, 10s per-IP dial timeout
- **Web search result filtering** (`web.go:430-443`): `isResultURLBlocked` on every DuckDuckGo result before surfacing

### Content-Type Enforcement
- `contentTypeEnforcementMiddleware` (`server.go:489-525`): rejects non-JSON content types before body decoding (VULN-050)

### Secret File Redaction
- `security.LooksLikeSecretFile` correctly redacts `.env`, `.pem`, `id_rsa`, credentials files at web file API level before raw bytes are returned.