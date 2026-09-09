BINARIES := fleetdeck fleetdeck-status
VERSION  ?= dev
LDFLAGS  := -X github.com/kroticw/fleetdeck/internal/version.value=$(VERSION)

.PHONY: build test lint run

build:
	@for b in $(BINARIES); do go build -ldflags "$(LDFLAGS)" -o bin/$$b ./cmd/$$b; done

test:
	go test ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

run: build
	./bin/fleetdeck
