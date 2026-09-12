BIN := readback
LDFLAGS := -s -w -X github.com/joshduffy/readback/internal/cli.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet check cross attack clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/readback

test:
	go test ./...

vet:
	go vet ./...

# Cross-compile every package with no output binary; catches GOOS-specific
# breakage (syscall use, build tags) that the host build misses.
cross:
	GOOS=windows GOARCH=amd64 go build -o /dev/null ./...
	GOOS=linux GOARCH=arm64 go build -o /dev/null ./...
	GOOS=darwin GOARCH=amd64 go build -o /dev/null ./...

check: vet test build cross

# Adversarial fixtures. Every module adds its incident-derived inputs here; the
# target fails if any fixture that must be rejected is accepted.
attack: build
	@echo "verify: fabricated handoff must exit 1"; \
	./$(BIN) verify --json testdata/verify/fabricated-handoff.md >/dev/null; \
	code=$$?; if [ $$code -eq 0 ]; then echo "FAIL: accepted fabricated handoff"; exit 1; fi; \
	echo "  exit $$code (stub exits 2 until implemented; 1 is the target)"

clean:
	rm -f $(BIN) coverage.out; rm -rf dist
