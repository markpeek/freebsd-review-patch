GO ?= go
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint

.PHONY: all build fmt fmt-check test test-race vet lint mod-check ci

all: build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/freebsd-review ./cmd/freebsd-review

fmt:
	$(GOFMT) -w cmd internal skills

fmt-check:
	@unformatted=$$($(GOFMT) -l cmd internal skills) || exit $$?; \
	if [ -n "$$unformatted" ]; then \
		printf '%s\n' "$$unformatted"; \
		printf '%s\n' 'Run make fmt to format these files.'; \
		exit 1; \
	fi

test:
	FREEBSD_REVIEW_LIVE_REPO= FREEBSD_REVIEW_LIVE_REVIEWER= $(GO) test ./...

test-race:
	FREEBSD_REVIEW_LIVE_REPO= FREEBSD_REVIEW_LIVE_REVIEWER= $(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run --config .golangci.yml ./...

mod-check:
	$(GO) mod tidy -diff
	$(GO) mod verify

ci: fmt-check mod-check vet test-race lint build
