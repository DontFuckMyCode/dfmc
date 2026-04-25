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
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    && install -d -m 755 /etc/ssl/private

WORKDIR /app

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
ENTRYPOINT ["dfmc"]