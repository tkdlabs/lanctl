MODULE  := github.com/tkdlabs/lanctl
BINARY  := lanctl
CLI     := lanctl-cli
DIST    := dist

# Version: latest git tag, else short SHA, else "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE)/internal/version.Version=$(VERSION)

GOFLAGS := -trimpath
export CGO_ENABLED := 0

.PHONY: all build build-cli test cover vet fmt tidy run cross install clean

all: build build-cli

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/lanctl/

build-cli:
	go build $(GOFLAGS) -o $(CLI) ./cmd/lanctl-cli/

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
	GOOS=linux GOARCH=amd64        go build $(GOFLAGS) -o $(DIST)/$(CLI)-linux-amd64 ./cmd/lanctl-cli/
	GOOS=linux GOARCH=arm64        go build $(GOFLAGS) -o $(DIST)/$(CLI)-linux-arm64 ./cmd/lanctl-cli/
	GOOS=linux GOARCH=arm GOARM=7  go build $(GOFLAGS) -o $(DIST)/$(CLI)-linux-arm   ./cmd/lanctl-cli/

install:
	./install.sh

clean:
	rm -rf $(BINARY) $(CLI) $(DIST) coverage.out
