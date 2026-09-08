# One command per claim. `make verify` is what CI runs.
GOTOOLCHAIN ?= auto
export GOTOOLCHAIN

.PHONY: verify build test-tlsverify test-assign test-reftest test-sentinel test-scripts web clean

verify: test-tlsverify test-assign test-reftest test-sentinel test-scripts web
	@echo "verify: all checks passed"

test-tlsverify:
	cd fibre-tlsverify && go vet ./... && go test -race -shuffle=on ./...

test-assign:
	cd fibre-assign && go vet ./... && go test -race -shuffle=on ./...

test-reftest:
	cd fibre-assign/reftest && go test -shuffle=on ./...

test-sentinel:
	cd fibre-sentinel && go vet ./... && go build ./... && go test -shuffle=on ./...

test-scripts:
	for f in fibre-devnet/*.sh fibre-sentinel/*.sh; do bash -n "$$f"; done
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build:
	cd fibre-sentinel && go build -o bin/ ./cmd/...
	cd web && npm ci --no-audit --no-fund && npm run build

# the ~17 minute devnet run with fault injection (needs celestia-appd + fibre on PATH)
devtest:
	cd fibre-sentinel && go build -o bin/ ./cmd/... && PATH="$$PWD/bin:$$PWD/../fibre-devnet:$$PATH" ./probe-devtest.sh 4 3

clean:
	rm -rf fibre-sentinel/bin web/out web/.next
