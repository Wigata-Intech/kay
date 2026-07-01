# kay — common dev tasks. Run `make` or `make help` for the list.
# Recipe lines are TAB-indented (Makefile requirement).
BINARY := kay
PKG    := ./...

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

.PHONY: fmt
fmt: ## Format all code (gofmt -w)
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofmt-clean (mirrors CI)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## go vet
	go vet $(PKG)

.PHONY: test
test: ## Run tests with the race detector (mirrors CI)
	go test -race $(PKG)

.PHONY: build
build: ## Build the kay binary
	go build -o $(BINARY) ./cmd/kay

.PHONY: lint
lint: ## Run golangci-lint v2 = staticcheck + others (install: https://golangci-lint.run)
	golangci-lint run

.PHONY: gosec
gosec: ## Run the gosec security scanner (mirrors CI)
	go run github.com/securego/gosec/v2/cmd/gosec@latest -exclude=G115 $(PKG)

.PHONY: vuln
vuln: ## Scan for known vulnerabilities (mirrors CI)
	go run golang.org/x/vuln/cmd/govulncheck@latest $(PKG)

.PHONY: tidy
tidy: ## Tidy and verify go.mod/go.sum
	go mod tidy

.PHONY: update-deps
update-deps: ## Bump x/crypto, x/sys, x/term to latest (fixes the govulncheck CVE)
	go get golang.org/x/crypto@latest golang.org/x/sys@latest golang.org/x/term@latest
	go mod tidy

.PHONY: check
check: fmt-check vet test build ## Quick pre-push gate (matches CI's build-test job)

.PHONY: ci
ci: fmt-check vet test build lint gosec vuln ## Run everything CI runs, locally

.PHONY: cover
cover: ## Run tests and print total coverage %% (writes coverage.out)
	go test -covermode=atomic -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

.PHONY: cover-html
cover-html: cover ## Build an HTML coverage report (coverage.html) from coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

.PHONY: release-snapshot
release-snapshot: ## Local GoReleaser dry-run, no publish (needs goreleaser)
	goreleaser release --snapshot --clean

.PHONY: demo
demo: build ## Record the anonymized demo GIF (needs vhs)
	KAY_DEMO=1 vhs docs/demo.tape

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out coverage.html
	rm -rf dist
