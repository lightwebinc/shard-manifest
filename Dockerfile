# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
#
# Multi-stage Dockerfile for shard-manifest. Bundles two binaries:
#
#   - /usr/local/bin/shard-manifest  (the periodic announcement daemon)
#   - /usr/local/bin/manifest-emit           (one-shot CLI for ops/debug)
#
# The default ENTRYPOINT runs the daemon. Override with --entrypoint when
# invoking manifest-emit from kubectl/helm.

FROM golang:1.26-alpine AS builder
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
        -ldflags "-s -w -X main.Version=${VERSION} -X github.com/lightwebinc/shard-manifest/metrics.Version=${VERSION}" \
        -o /out/shard-manifest .; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
      go build -trimpath -buildvcs=false \
        -ldflags "-s -w -X main.Version=${VERSION}" \
        -o /out/manifest-emit ./cmd/manifest-emit/

FROM gcr.io/distroless/static:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7
USER nonroot:nonroot
COPY --from=builder /out/ /usr/local/bin/
EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/shard-manifest"]
