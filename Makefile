.PHONY: test race lint check build cover

GOFMT_FILES := $(shell gofmt -l .)

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

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
