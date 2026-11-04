GO ?= go
BINARY := booxd
DIST := dist

.PHONY: build vet test lint ci clean

build:
	CGO_ENABLED=0 $(GO) build -o $(DIST)/$(BINARY) .

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found; install it with: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0"; exit 1; }
	golangci-lint run ./...

ci: build vet test lint

clean:
	rm -rf $(DIST)
