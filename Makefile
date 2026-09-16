MODULE  := github.com/tkdlabs/lanctl
BINARY  := lanctl
DIST    := dist

# Version: latest git tag, else short SHA, else "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE)/internal/version.Version=$(VERSION)

GOFLAGS := -trimpath
export CGO_ENABLED := 0

.PHONY: all build test cover vet fmt tidy run cross install clean

all: build

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/lanctl/

test:
	go test ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

run:
	go run ./cmd/lanctl/

# Cross-compile static linux binaries (amd64, arm64, armv7) into dist/.
cross:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64        go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-amd64 ./cmd/lanctl/
	GOOS=linux GOARCH=arm64        go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm64 ./cmd/lanctl/
	GOOS=linux GOARCH=arm GOARM=7  go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-arm   ./cmd/lanctl/

install:
	./install.sh

clean:
	rm -rf $(BINARY) $(DIST) coverage.out
