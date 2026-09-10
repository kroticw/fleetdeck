VERSION  ?= dev
LDFLAGS  := -X github.com/kroticw/fleetdeck/internal/version.value=$(VERSION)

.PHONY: build test lint run

# Build every binary under ./cmd/*. Earlier tasks in this project have not added a
# cmd/ directory yet, so fall back to `go build ./...` to still catch compile errors
# across the module rather than silently doing nothing.
build:
	@dirs="$$(ls -d cmd/*/ 2>/dev/null)"; \
	if [ -z "$$dirs" ]; then \
		go build -ldflags "$(LDFLAGS)" ./... || exit 1; \
	else \
		for dir in $$dirs; do \
			b=$$(basename $$dir); \
			go build -ldflags "$(LDFLAGS)" -o bin/$$b ./cmd/$$b || exit 1; \
		done; \
	fi

test:
	go test ./... -race -count=1

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	golangci-lint run ./...

run: build
	@if [ ! -x ./bin/fleetdeck ]; then \
		echo "run: ./bin/fleetdeck was not built — no cmd/ directory exists yet in this branch" >&2; \
		exit 1; \
	fi
	./bin/fleetdeck
