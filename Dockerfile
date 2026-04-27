# Build stage
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

# Install build dependencies for tree-sitter (CGO required)
RUN apk add --no-cache \
    gcc \
    musl-dev \
    git \
    make

WORKDIR /src

# Pre-fetch dependencies (layer caching)
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/mod,sharing=locked \
    --mount=type=cache,target=/go/pkg,sharing=locked \
    go mod download

# Copy source and build
COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags "-s -w" \
    -o /dfmc ./cmd/dfmc

# Runtime stage — minimal Alpine
FROM alpine:3.20

# Install runtime dependencies (C runtime for tree-sitter .so, ca-certificates for web fetches)
# tini for proper signal handling and child process reaping — prevents orphaned
# bbolt locks and stray MCP subprocesses after docker stop.
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    tini \
    && install -d -m 755 /etc/ssl/private

WORKDIR /app

# Ports: 7777 (web HTTP+SSE), 7778 (gRPC, reserved), 7779 (remote WS, reserved)
EXPOSE 7777 7778 7779

# Ports 7777-7779 are reserved for dfmc serve (HTTP), dfmc remote start (gRPC),
# and remote WebSocket. Non-loopback exposure requires --auth token and --bind 0.0.0.0.
# By default dfmc serve binds 127.0.0.1 and is unreachable from outside the container.
LABEL maintainer="dfmc"
LABEL description="DFMC code intelligence assistant. Default serve binds 127.0.0.1:7777. Use --auth token --bind 0.0.0.0 for network exposure."

# Copy binary from builder
COPY --from=builder /dfmc /usr/local/bin/dfmc

# Embed a default config that mirrors dfmc doctor expectations
# so the image works OOTB without a mounted project.
RUN mkdir -p /app/.dfmc && \
    echo '# auto-generated empty config — replace via volume mount\n' \
         '# provider keys can also be set via env: ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.\n' \
         > /app/.dfmc/config.yaml

# Shell completions installed to /usr/share/bash-completion/completions,
# /usr/share/zsh/site-contrib, /usr/share/fish/completions by dfmc init -c.

ENV HOME=/root
ENTRYPOINT ["/sbin/tini", "--", "dfmc"]