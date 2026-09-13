BIN := readback
LDFLAGS := -s -w -X github.com/joshduffy/readback/internal/cli.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test vet check cross attack web-check clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/readback

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

web-check:
	node --test web/worker.test.mjs

# Adversarial fixtures. Every module adds its incident-derived inputs here; the
# target fails if any fixture that must be rejected is accepted.
attack: build
	@command -v jq >/dev/null 2>&1 || { echo "FAIL: make attack requires jq"; exit 1; }; \
	tmp=$$(mktemp -d) || exit 1; \
	trap 'rm -f "$$tmp/result.json" "$$tmp/command.json"; rmdir "$$tmp"' EXIT HUP INT TERM; \
	./$(BIN) verify testdata/verify/fabricated-handoff.md --json >"$$tmp/result.json"; code=$$?; \
	if [ $$code -ne 2 ] || ! jq -e '.exit == 2 and (.error | contains("no_claims_block"))' "$$tmp/result.json" >/dev/null; then \
	  echo "FAIL: fabricated handoff must exit 2 with no_claims_block (got $$code)"; cat "$$tmp/result.json"; exit 1; fi; \
	./$(BIN) verify testdata/verify/contradicted-claims.json --json >"$$tmp/result.json"; code=$$?; \
	if [ $$code -ne 1 ] || ! jq -e '.exit == 1 and .data.summary.contradicted >= 4' "$$tmp/result.json" >/dev/null; then \
	  echo "FAIL: contradicted fixture must exit 1 with at least four contradictions (got $$code)"; cat "$$tmp/result.json"; exit 1; fi; \
	./$(BIN) verify testdata/verify/claims.json --json >"$$tmp/result.json"; code=$$?; \
	if [ $$code -ne 1 ] || ! jq -e '.exit == 1 and .data.summary.contradicted >= 2 and .data.summary.indeterminate >= 1' "$$tmp/result.json" >/dev/null; then \
	  echo "FAIL: mixed fixture must exit 1 with contradicted and indeterminate claims (got $$code)"; cat "$$tmp/result.json"; exit 1; fi; \
	printf '%s\n' '{"version":1,"claims":[{"type":"file_exists","path":"README.md","command":"echo forbidden"}]}' >"$$tmp/command.json"; \
	./$(BIN) verify "$$tmp/command.json" --json >"$$tmp/result.json"; code=$$?; \
	if [ $$code -ne 64 ] || ! jq -e '.exit == 64' "$$tmp/result.json" >/dev/null; then \
	  echo "FAIL: command field must exit 64 (got $$code)"; cat "$$tmp/result.json"; exit 1; fi; \
	echo "PASS: all four attack fixtures rejected with the required exits and evidence"

clean:
	rm -f $(BIN) coverage.out; rm -rf dist
