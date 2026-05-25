# syntax=docker/dockerfile:1.7
#
# Multi-stage Dockerfile for bitcoin-shard-manifest. Bundles two binaries:
#
#   - /usr/local/bin/bitcoin-shard-manifest  (the periodic announcement daemon)
#   - /usr/local/bin/manifest-emit           (one-shot CLI for ops/debug)
#
# The default ENTRYPOINT runs the daemon. Override with --entrypoint when
# invoking manifest-emit from kubectl/helm.

FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    mkdir -p /out; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -buildvcs=false \
        -ldflags "-s -w -X main.Version=${VERSION} -X github.com/lightwebinc/bitcoin-shard-manifest/metrics.Version=${VERSION}" \
        -o /out/bitcoin-shard-manifest .; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -buildvcs=false \
        -ldflags "-s -w -X main.Version=${VERSION}" \
        -o /out/manifest-emit ./cmd/manifest-emit/

FROM gcr.io/distroless/static:nonroot
USER nonroot:nonroot
COPY --from=builder /out/ /usr/local/bin/
EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/bitcoin-shard-manifest"]
