.PHONY: build test vet clean

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -s -w \
	-X github.com/adham90/opentrace/internal/version.Version=$(VERSION) \
	-X github.com/adham90/opentrace/internal/version.Commit=$(COMMIT) \
	-X github.com/adham90/opentrace/internal/version.Date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o opentrace ./cmd/opentrace

test:
	go test -short -race ./...

vet:
	go vet ./...

clean:
	rm -f opentrace
	rm -rf tmp
