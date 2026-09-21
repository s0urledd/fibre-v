# One command per claim. `make verify` is what CI runs.
GOTOOLCHAIN ?= auto
export GOTOOLCHAIN

.PHONY: verify build test-tlsverify test-assign test-reftest test-sentinel test-scripts test-deploy web clean

verify: test-tlsverify test-assign test-reftest test-sentinel test-scripts test-deploy shellcheck web
	@echo "verify: all checks passed"

test-tlsverify:
	cd fibre-tlsverify && go vet ./... && go test -race -shuffle=on ./...

test-assign:
	cd fibre-assign && go vet ./... && go test -race -shuffle=on ./...

test-reftest:
	cd fibre-assign/reftest && go test -shuffle=on ./...

test-sentinel:
	cd fibre-sentinel && go vet ./... && go build ./... && go test -shuffle=on ./...

# CI runs shellcheck; run it here too when it is installed, so a local
# "make verify" cannot pass a change that CI then rejects.
shellcheck:
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck --severity=error fibre-devnet/*.sh fibre-sentinel/*.sh deploy/*.sh deploy/test/*.sh; \
	else \
		echo "shellcheck not installed; CI runs it (severity=error) — install it to catch script errors locally"; \
	fi

test-scripts:
	for f in fibre-devnet/*.sh fibre-sentinel/*.sh deploy/test/*.sh; do bash -n "$$f"; done
	@for f in deploy/*.py deploy/test/*.py web/test/*.py; do python3 -c "import ast,sys; ast.parse(open(sys.argv[1]).read())" "$$f" || exit 1; done
	@for f in web/test/*.cjs; do node --check "$$f" || exit 1; done
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }

# the acceptance tests' own regression tests: fake servers on loopback, no root
test-deploy:
	deploy/test/selftest.sh

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
