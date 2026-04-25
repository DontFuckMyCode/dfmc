# sc-docker — DFMC Container Image Security Review

**Target:** `D:\Codebox\PROJECTS\DFMC\Dockerfile`
**Compose files:** none found
**`.dockerignore`:** **NOT PRESENT**
**Date:** 2026-04-25

## Counts

| Severity  | Count |
|-----------|-------|
| Critical  | 0     |
| High      | 3     |
| Medium    | 5     |
| Low       | 4     |
| Info      | 2     |
| **Total** | **14** |

| Confidence | Count |
|------------|-------|
| High       | 11    |
| Medium     | 3     |
| Low        | 0     |

---

## Findings

### DOCKER-001 — Missing `.dockerignore` (build-context exfil risk)

- **Severity:** High
- **Confidence:** High
- **File:** repo root (no `.dockerignore` present)
- **CWE:** CWE-538 (Insertion of Sensitive Information into Externally-Accessible File or Directory), CWE-200
- **Evidence:** Dockerfile:20 `COPY . .` copies the entire build context. With no `.dockerignore`, the build sucks in:
  - `.git/` — full version history (intermediate layer leak even if a later stage drops it; a `docker history` viewer or registry probe of the builder image can still reach it if the builder is ever pushed/cached publicly)
  - `.dfmc/` — bbolt files (memory, conversations, task store, drive runs) — may contain user prompts, API responses, secrets cached in memory tier
  - `.env` — explicitly auto-loaded by DFMC at startup; per CLAUDE.md "Project-root `.env` is auto-loaded at startup" — almost certainly contains `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / etc. on a developer machine running `docker build` locally
  - `.project/` — design specs (gitignored, not git-checkout safe)
  - IDE files (`.vscode/`, `.idea/`)
  - Build artifacts (`bin/`)
  - `security-report/` — the very report you are reading
- **Impact:** The builder layer caches the entire repo including secrets; `RUN go mod download` runs after `COPY go.mod go.sum ./` so the module-download layer is OK, but the second `COPY . .` (line 20) brings in everything for the build step. Any subsequent `docker push` of the builder image (multi-stage doesn't push it by default, but `--target=builder` builds, BuildKit cache exports, or a misconfigured CI absolutely will) leaks every secret that was on disk at build time.
- **Fix:** Add a `.dockerignore` at repo root with at minimum:
  ```
  .git
  .github
  .dfmc
  .env
  .env.*
  .project
  .vscode
  .idea
  bin/
  *.exe
  security-report/
  shell/
  assets/
  Makefile
  CLAUDE.md
  README*.md
  *.test
  coverage*
  .claude/
  ```

---

### DOCKER-002 — Base image not pinned by digest (`golang:1.25-alpine`, `alpine:3.20`)

- **Severity:** High
- **Confidence:** High
- **File:** Dockerfile:2, Dockerfile:29
- **CWE:** CWE-829 (Inclusion of Functionality from Untrusted Control Sphere), CWE-494 (Download of Code Without Integrity Check)
- **Evidence:**
  - L2 `FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder`
  - L29 `FROM alpine:3.20`
  Both use mutable tags. A compromised or re-tagged upstream image is silently pulled on every rebuild. `golang:1.25-alpine` will also drift across minor Alpine bumps; reproducibility is lost.
- **Impact:** Supply-chain risk. CVE rolls in/out without the maintainer noticing; reproducibility lost; SBOM unstable.
- **Fix:**
  ```dockerfile
  FROM --platform=$BUILDPLATFORM golang:1.25-alpine@sha256:<digest> AS builder
  FROM alpine:3.20@sha256:<digest>
  ```
  Refresh digests via `docker pull <tag>` + `docker inspect --format='{{index .RepoDigests 0}}' <tag>`. Wire to Renovate/Dependabot for managed bumps.

---

### DOCKER-003 — Container runs as root (no `USER` directive)

- **Severity:** High
- **Confidence:** High
- **File:** Dockerfile:29-53 (runtime stage)
- **CWE:** CWE-250 (Execution with Unnecessary Privileges), CWE-269
- **Evidence:** Runtime stage does not set `USER`. Default UID is 0 (root). DFMC runs `run_command` (shells out arbitrary binaries), `write_file`/`apply_patch`, `web_fetch`, hooks (user-defined shell commands), and external MCP server subprocesses — every one of those inherits root inside the container. If someone mounts the host filesystem (likely, given DFMC is a code-intel tool intended to operate on a project), a tool-call escape becomes a host-root compromise.
- **Impact:** Container escape from a malicious LLM tool call, hostile MCP server tool description, or malicious project hook lands as root inside the container; if the container is privileged/capable or has a host bind mount, escalates.
- **Fix:** Create and switch to a non-root user in the runtime stage:
  ```dockerfile
  RUN addgroup -S dfmc && adduser -S -G dfmc -u 10001 -h /home/dfmc -s /sbin/nologin dfmc \
      && mkdir -p /home/dfmc /app && chown -R dfmc:dfmc /home/dfmc /app
  USER 10001:10001
  ENV HOME=/home/dfmc
  ```
  Drop the `ENV HOME=/root` on Dockerfile:52.

---

### DOCKER-004 — Runtime stage carries Alpine package manager + extra tooling

- **Severity:** Medium
- **Confidence:** High
- **File:** Dockerfile:29-35
- **CWE:** CWE-1021, CWE-693 (Protection Mechanism Failure)
- **Evidence:** Runtime is `alpine:3.20`, not `gcr.io/distroless/static-debian12` or `scratch`. `apk` is present, BusyBox shell is present, `wget`/`nc`/`vi` are reachable post-exploit. The DFMC binary is a self-contained Go binary with embedded HTML — a distroless image is feasible, especially because the runtime stage does NOT need libc beyond what's baked into the binary. Tree-sitter `.so` linkage requires a libc, so `gcr.io/distroless/base-debian12` (with glibc) or `gcr.io/distroless/static` after switching to `CGO_ENABLED=0` would both work.
- **Impact:** Larger attack surface for post-exploit pivoting, larger CVE footprint, larger image size.
- **Fix:** Move runtime to `gcr.io/distroless/base-debian12:nonroot` (handles musl/glibc, ships `nonroot` UID 65532 and ca-certificates). If staying on Alpine, at minimum strip apk after install:
  ```dockerfile
  RUN apk add --no-cache ca-certificates tzdata && rm -rf /var/cache/apk/*
  ```

---

### DOCKER-005 — `CGO_ENABLED=1` build into musl, runtime is Alpine but no explicit musl runtime check

- **Severity:** Medium
- **Confidence:** High
- **File:** Dockerfile:24, Dockerfile:29-32
- **CWE:** CWE-1188 (Insecure Default Initialization of Resource)
- **Evidence:** Builder is `golang:1.25-alpine` (musl), runtime is `alpine:3.20` (musl). The C runtime IS available in `alpine:3.20` (musl is part of the base image), so this happens to work — but the Dockerfile comment on L31 ("C runtime for tree-sitter .so") is misleading: `apk add ca-certificates tzdata` does not install a C runtime; musl just happens to be in the base. If anyone copies this pattern onto a glibc base, the binary will fail with "no such file or directory" on the dynamic linker. **Not a vulnerability per se**, but a fragility note for the deploy chain.
- **Impact:** Misleading comment; no security impact on this image as-shipped.
- **Fix:** Update the comment: `# musl is provided by the alpine base; ca-certificates for HTTPS, tzdata for time-zone aware output`. Or build with `CGO_ENABLED=0` and `-tags=osusergo,netgo` and use `gcr.io/distroless/static` — accepts the AST-regex-fallback tradeoff documented in CLAUDE.md.

---

### DOCKER-006 — No `HEALTHCHECK` directive

- **Severity:** Medium
- **Confidence:** High
- **File:** Dockerfile (entire file)
- **CWE:** CWE-693
- **Evidence:** No `HEALTHCHECK` instruction. DFMC's `dfmc serve` exposes `GET /healthz` (architecture.md §3.2 line 140 "always 200, no auth") which is the perfect target. Without a HEALTHCHECK, orchestrators (Docker Swarm, Compose `condition: service_healthy`, ECS) cannot detect a wedged process; `docker ps` shows "Up" even when the listener is dead.
- **Impact:** Operational. Failed-but-running containers attract traffic.
- **Fix:**
  ```dockerfile
  HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:7777/healthz || exit 1
  ```
  (or use a baked-in `dfmc doctor` invocation if the entry-mode is CLI rather than `serve`).

---

### DOCKER-007 — No `EXPOSE` directive; ports 7777 / 7778 / 7779 undocumented

- **Severity:** Low
- **Confidence:** High
- **File:** Dockerfile (entire file)
- **CWE:** CWE-1059 (Insufficient Technical Documentation)
- **Evidence:** Per architecture.md §3.2 the web server binds `127.0.0.1:7777` (default) and `dfmc remote start` reserves 7778/7779. Dockerfile has no `EXPOSE`. Combined with `dfmc serve`'s loopback-default bind (architecture.md §5 "Authentication"), running this image and `docker run -p 7777:7777 dfmc serve` will silently fail to bind anything reachable: the in-process `normalizeBindHost` rewrites non-loopback to `127.0.0.1` when `auth=none`. The image gives operators no signal about this.
- **Impact:** Operators run `dfmc serve` in the container, see no listener, blame Docker. Or worse, run `dfmc serve --insecure --bind 0.0.0.0` and expose an unauthenticated agent.
- **Fix:** Add `EXPOSE 7777 7778 7779` and a `LABEL` block documenting that `dfmc serve --bind 0.0.0.0 --auth token --token <secret>` (or a reverse proxy) is required for non-loopback exposure.

---

### DOCKER-008 — `ENTRYPOINT ["dfmc"]` with no default `CMD`; no signal handling notes

- **Severity:** Low
- **Confidence:** Medium
- **File:** Dockerfile:53
- **CWE:** CWE-693
- **Evidence:** No default `CMD`, so `docker run dfmc` just prints help. Acceptable for a CLI tool. However, DFMC's bbolt store grabs an exclusive lock (architecture.md §6, `ErrStoreLocked`); if `docker stop` SIGTERM doesn't reach the Go process cleanly (PID 1 in non-init mode doesn't reap children + dfmc spawns subprocesses for hooks/MCP), the lock can be left orphaned at next start. Tini/dumb-init recommended.
- **Impact:** Stuck bbolt locks across container restarts; orphaned MCP subprocesses.
- **Fix:**
  ```dockerfile
  RUN apk add --no-cache tini
  ENTRYPOINT ["/sbin/tini", "--", "dfmc"]
  ```

---

### DOCKER-009 — `RUN echo` to construct config.yaml uses `\n` literal that may not be interpreted

- **Severity:** Low
- **Confidence:** High
- **File:** Dockerfile:44-47
- **CWE:** CWE-1188
- **Evidence:**
  ```dockerfile
  RUN mkdir -p /app/.dfmc && \
      echo '# auto-generated empty config — replace via volume mount\n' \
           '# provider keys can also be set via env: ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.\n' \
           > /app/.dfmc/config.yaml
  ```
  BusyBox `echo` does not honour `\n` escapes by default (no `-e`). The resulting file is a single line containing the literal `\n` characters. This file then gets parsed as YAML — comments survive intact (the `#` is at the start) but the file is one long comment line and likely fine, *but the intent is clearly multi-line and the result silently isn't*.
- **Impact:** Functional bug, not a security bug, *unless* someone later adds key-value YAML using the same echo-with-`\n` pattern and ships an unparseable config. Adjacent risk: the line-3 comment contains `ANTHROPIC_API_KEY, OPENAI_API_KEY` strings which a careless `grep -r ANTHROPIC` inside the image will surface.
- **Fix:** Use a heredoc or `printf`:
  ```dockerfile
  RUN mkdir -p /app/.dfmc && cat > /app/.dfmc/config.yaml <<'EOF'
  # auto-generated empty config - replace via volume mount
  # provider keys can also be set via env: ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.
  EOF
  ```

---

### DOCKER-010 — `git` in builder image (build-time supply chain widened)

- **Severity:** Medium
- **Confidence:** Medium
- **File:** Dockerfile:5-9
- **CWE:** CWE-1357 (Reliance on Insufficiently Trustworthy Component)
- **Evidence:** `apk add --no-cache gcc musl-dev git make`. `git` is installed in the builder. `go mod download` does NOT need git for the listed `go.mod` deps (none are git-direct refs that I can see in this repo's `go.mod` — all are versioned modules served by `proxy.golang.org`). If a `go.mod` change later introduces a `replace` pointing at a git URL or a non-proxied private module, having `git` quietly enables that. With the builder also doing `COPY . .` (which includes `.git` if `.dockerignore` is fixed naively), there is also a path for a hostile `.git/hooks/*` to run at build time if a future RUN invokes any git command in the source tree.
- **Impact:** Build-time supply-chain widening. Low likelihood today, but pairs badly with the missing `.dockerignore`.
- **Fix:** Drop `git` and `make` unless concretely needed. Confirm all modules resolve via `GOPROXY=https://proxy.golang.org`. If git is truly needed, set `GOFLAGS=-mod=readonly GONOSUMCHECK=0` and disable git config inheritance.

---

### DOCKER-011 — `--mount=type=cache` with `sharing=locked` + `COPY . .` ordering

- **Severity:** Info
- **Confidence:** High
- **File:** Dockerfile:15-20
- **CWE:** N/A
- **Evidence:** `--mount=type=cache,target=/go/mod` and `target=/go/pkg`. The mount targets are wrong for Go: the canonical paths are `/root/.cache/go-build` (build cache) and `/go/pkg/mod` (module cache). `/go/mod` is not used by the toolchain — that cache mount is essentially dead. `/go/pkg` does cover the module cache, but only because `GOPATH=/go` defaults inside the official `golang` image and `pkg/mod` is under it.
- **Impact:** Slower builds, no security impact.
- **Fix:**
  ```dockerfile
  RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
      --mount=type=cache,target=/go/pkg/mod,sharing=locked \
      go mod download
  ```
  And add the same caches to the `go build` step.

---

### DOCKER-012 — `WORKDIR /app` set in runtime, but binary copied to `/usr/local/bin/dfmc`

- **Severity:** Info
- **Confidence:** High
- **File:** Dockerfile:37, Dockerfile:40
- **CWE:** N/A
- **Evidence:** `WORKDIR /app` makes the *current directory* `/app` at container start. DFMC's project-config discovery walks the cwd looking for `.dfmc/config.yaml`. Operators bind-mounting their project at `/app` will pick up the right config; operators bind-mounting elsewhere will NOT, because cwd is fixed at `/app`. This is a footgun, not a vuln — but it interacts with DOCKER-009: the auto-generated `/app/.dfmc/config.yaml` *will* get loaded as the project config at startup if the user runs DFMC inside the image without overlaying a real project.
- **Impact:** Misleading default behavior; the image looks "configured" but is empty.
- **Fix:** Document the bind-mount expectation in image labels; OR drop the `/app/.dfmc/config.yaml` skeleton entirely (DFMC degrades gracefully without it).

---

### DOCKER-013 — `install -d -m 755 /etc/ssl/private` — useless, possibly misleading

- **Severity:** Low
- **Confidence:** Medium
- **File:** Dockerfile:35
- **CWE:** CWE-1188
- **Evidence:** `install -d -m 755 /etc/ssl/private`. Mode 755 on `/etc/ssl/private` is wider than the conventional 700 for a private-keys directory, and DFMC isn't a TLS terminator (no HTTPS listener — architecture.md §9 explicitly notes "No HTTPS termination in-process"). Creating this directory at all suggests a planned-but-unused TLS feature, and at 755 it's open to read by any user/process on the container — which today is fine because there's nothing in it, but it's a setup that *invites* someone to drop a private key in there and inherit the wrong permissions.
- **Impact:** Future-fragility. Not a present-day leak.
- **Fix:** Remove the line, OR set `-m 700` if there's a concrete plan to use it.

---

### DOCKER-014 — No `LABEL` metadata (image provenance / OCI annotations)

- **Severity:** Info
- **Confidence:** High
- **File:** Dockerfile (entire file)
- **CWE:** N/A
- **Evidence:** No OCI `LABEL` block (`org.opencontainers.image.source`, `org.opencontainers.image.version`, `org.opencontainers.image.licenses`). Without these, GHCR/Docker Hub provenance pages and tools like `docker scout` show no link back to the source repo or git SHA.
- **Impact:** Provenance / supply-chain transparency.
- **Fix:**
  ```dockerfile
  ARG VERSION=dev
  ARG REVISION=unknown
  LABEL org.opencontainers.image.title="dfmc" \
        org.opencontainers.image.description="Don't Fuck My Code — code intelligence assistant" \
        org.opencontainers.image.source="https://github.com/dontfuckmycode/dfmc" \
        org.opencontainers.image.version="${VERSION}" \
        org.opencontainers.image.revision="${REVISION}" \
        org.opencontainers.image.licenses="<SPDX>" \
        org.opencontainers.image.vendor="dontfuckmycode"
  ```

---

## What was NOT found (good)

- No `ARG` / `ENV` carrying API keys (DOCKER-001 is the indirect risk path through `COPY . .` + `.env`)
- No `--privileged`, no Docker socket mount (no compose file at all)
- No `latest` tag — `1.25-alpine` and `3.20` are at least major.minor pinned (just not digest-pinned)
- Multi-stage build correctly separates the `gcc`/`musl-dev`/`git`/`make` build deps from the runtime image — runtime does NOT have a compiler
- No `chmod 777` / `chown -R 0:0` antipatterns

## Summary

The Dockerfile is structurally sound (multi-stage, slim runtime, no compilers in the final image) but ships three high-severity gaps:

1. **No `.dockerignore`** — `COPY . .` will eat `.env` / `.git` / `.dfmc` on any developer machine that runs `docker build` locally (DOCKER-001).
2. **Mutable base tags** — `golang:1.25-alpine` and `alpine:3.20` are pinned by minor tag only; supply-chain reproducibility is broken (DOCKER-002).
3. **Runs as root** — combined with DFMC's broad tool surface (`run_command`, hooks, MCP subprocesses), a tool-call escape lands as container-root (DOCKER-003).

The medium findings (no HEALTHCHECK, runtime is full Alpine rather than distroless, dead build-cache paths) are operational-quality issues that do not block deploy.
