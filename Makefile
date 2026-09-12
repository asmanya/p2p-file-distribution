.PHONY: test race lint check build cover

GOFMT_FILES := $(shell gofmt -l .)

# GOLANGCI_LINT_VERSION must match the version pinned in .github/workflows/ci.yml exactly. `go run` fetches and
# caches this specific version on first use, ignoring whatever golangci-lint (if any) happens to be installed
# globally - so a local install auto-upgrading itself, the way it did once already, can never again make `make
# lint` pass or fail differently from CI.
GOLANGCI_LINT_VERSION := v2.1.0
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

test:
	go test ./...

race:
	go test -race ./...

lint:
	$(GOLANGCI_LINT) run

check:
	$(if $(GOFMT_FILES),$(error gofmt needs to be run on: $(GOFMT_FILES)))
	go vet ./...
	$(MAKE) lint
	$(MAKE) race

build:
	go build -o bin/p2pget.exe ./cmd/p2pget

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out
