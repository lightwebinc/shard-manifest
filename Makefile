BIN      := bitcoin-shard-manifest
CLI      := manifest-emit
PKG      := ./...
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
TAG      ?= $(VERSION)
IMAGE    ?= ghcr.io/lightwebinc/$(BIN)
COMMON   ?= ../bitcoin-shard-common
LDFLAGS  := -s -w -X main.Version=$(VERSION) -X github.com/lightwebinc/bitcoin-shard-manifest/metrics.Version=$(VERSION)

DAGGER_RUN := GOWORK=off go run .

.PHONY: all build build-cli test lint tidy clean install-source \
        ci ci-unit ci-lint ci-vuln ci-tidy ci-build ci-image ci-export ci-publish ci-shell \
        fmt help

all: build build-cli

build:                 ## build bitcoin-shard-manifest on the host
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) .

build-cli:             ## build manifest-emit CLI
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(CLI) ./cmd/manifest-emit

test:
	go test -race -count=1 $(PKG)

lint:
	golangci-lint run

tidy:
	go mod tidy

clean:
	rm -f $(BIN) $(CLI)
	rm -rf build

# Push the binary into a manifest LXD VM for end-to-end lab tests.
install-manifest: build
	lxc file push $(BIN) manifest/usr/local/bin/$(BIN)
	lxc exec manifest -- chmod +x /usr/local/bin/$(BIN)

# --- Dagger CI (containerised, reproducible) ---

ci:                    ## full pipeline: tidy + lint + vuln + unit + build + image
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) -version=$(VERSION) all

ci-unit:               ## go test -race ./... inside Dagger
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) unit

ci-lint:               ## go vet + golangci-lint inside Dagger
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) lint

ci-vuln:               ## govulncheck inside Dagger
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) vuln

ci-tidy:               ## go mod tidy diff check inside Dagger
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) tidy

ci-build:              ## go build ./... inside Dagger (no image)
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) build

ci-image:              ## build OCI image (cached only)
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) -version=$(VERSION) image

ci-export:             ## export image to build/$(BIN)-$(TAG).tar
	@mkdir -p build
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) -version=$(VERSION) \
	  -export=../build/$(BIN)-$(TAG).tar image

ci-publish:            ## publish image to $(IMAGE):$(TAG)
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) -version=$(VERSION) \
	  -address=$(IMAGE):$(TAG) image

ci-shell:              ## interactive shell in the builder container
	cd ci && $(DAGGER_RUN) -src=.. -common=../$(COMMON) dev-shell

fmt:                   ## gofmt -w
	gofmt -w .

help:                  ## list targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
