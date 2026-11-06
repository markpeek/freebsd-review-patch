GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint

.PHONY: all build fmt fmt-check test test-race vet lint mod-check ci

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/freebsd-review-prepare ./cmd/freebsd-review-prepare

fmt:
	$(GOFMT) -w cmd internal

fmt-check:
	@unformatted=$$($(GOFMT) -l cmd internal) || exit $$?; \
	if [ -n "$$unformatted" ]; then \
		printf '%s\n' "$$unformatted"; \
		printf '%s\n' 'Run make fmt to format these files.'; \
		exit 1; \
	fi

test:
	FREEBSD_REVIEW_LIVE_REPO= $(GO) test ./...

test-race:
	FREEBSD_REVIEW_LIVE_REPO= $(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run --config .golangci.yml ./...

mod-check:
	$(GO) mod tidy -diff
	$(GO) mod verify

ci: fmt-check mod-check vet test-race lint build
