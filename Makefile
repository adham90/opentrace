.PHONY: build dev test vet clean generate css

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -s -w \
	-X github.com/adham90/opentrace/internal/version.Version=$(VERSION) \
	-X github.com/adham90/opentrace/internal/version.Commit=$(COMMIT) \
	-X github.com/adham90/opentrace/internal/version.Date=$(DATE)

generate:
	templ generate

css:
	npx @tailwindcss/cli -i ./assets/css/input.css -o ./assets/css/output.css --minify

build: generate css
	go build -ldflags "$(LDFLAGS)" -o opentrace ./cmd/opentrace

dev:
	templ generate --watch --proxy="http://localhost:8080" --open-browser=false --cmd="go run ./cmd/opentrace" &
	npx @tailwindcss/cli -i ./assets/css/input.css -o ./assets/css/output.css --watch

test:
	go test -short -race ./...

vet:
	go vet ./...

clean:
	rm -f opentrace
	rm -rf tmp
	rm -f assets/css/output.css
