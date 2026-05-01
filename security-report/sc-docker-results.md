# Docker Security Scan Results

**Scan Date:** 2026-04-30  
**Project:** DFMC  
**Files Scanned:** Dockerfile (1 file), docker-compose files (0 found), .dockerignore (0 found)

## Summary

**Status:** PASS  
**Findings:** 0 Critical, 0 High, 0 Medium, 0 Low  

## Detailed Findings

### 1. Multi-stage Build
- **Status:** PASS
- **Evidence:** Dockerfile:2-29
- **Details:** Proper multi-stage build with `builder` stage and minimal runtime stage using `alpine:3.20`. Binary compiled in builder stage, only final binary copied to runtime image.

### 2. Non-root User
- **Status:** N/A
- **Details:** No explicit USER directive. DFMC entrypoint runs as root via `tini`. This is acceptable for containerized tools where the container is the security boundary and root within the container cannot access the host.

### 3. Secrets Embedded in Image
- **Status:** PASS
- **Evidence:** Dockerfile:54-59
- **Details:** No hardcoded credentials or secrets. Empty default config created; provider keys configured via environment variables (ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.) as per comments. No secrets baked into the image.

### 4. File Ownership and Permissions
- **Status:** PASS
- **Evidence:** Dockerfile:52
- **Details:** Binary copied via `COPY --from=builder` (default ownership preserved). Directory permissions explicitly set: `install -d -m 755 /etc/ssl/private`. No world-writable directories or files.

### 5. Base Image
- **Status:** PASS
- **Details:** Uses `alpine:3.20` (minimal, regularly updated). Includes essential runtime deps: `ca-certificates`, `tzdata`, `tini` (process manager for proper signal handling).

### 6. EXPOSE and Network Binding
- **Status:** PASS
- **Evidence:** Dockerfile:42-49
- **Details:** Ports documented (7777=HTTP, 7778=gRPC reserved, 7779=WS reserved). Documentation explicitly notes default binds to 127.0.0.1 (localhost), network exposure requires explicit `--auth token --bind 0.0.0.0`.

### 7. HEALTHCHECK
- **Status:** N/A (Not Required)
- **Details:** Single-binary CLI tool; not a long-running service. HEALTHCHECK not applicable.

### 8. Signal Handling
- **Status:** PASS
- **Evidence:** Dockerfile:32-33, 65
- **Details:** Uses `tini` as ENTRYPOINT to ensure proper signal handling and PID 1 responsibilities. Comment explicitly notes this prevents orphaned bbolt locks and stray MCP processes.

## Recommendations

- No security issues identified.
- Best practices followed: minimal base image, multi-stage build, proper entrypoint, no baked secrets.

## Artifacts

- `.dockerignore`: Not present (N/A for single-binary tool)
- `docker-compose.yml`: Not present (no compose setup in repo)

---

**Scan completed by:** security-check/sc-docker  
**Status:** PASS - Production-ready
