# Antenna -- development tasks.
#
# `make ci` runs exactly what CI runs, so a red build is always reproducible
# locally. If it passes here it passes there.

# Fall back to the userspace Go install if go is not on PATH.
GO       ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo $(HOME)/.local/bin/golangci-lint)
# gofmt ships beside go; resolve it from there so PATH is not required.
GOFMT    ?= $(shell command -v gofmt 2>/dev/null || echo $(dir $(GO))gofmt)

# golangci-lint shells out to `go env`, so an absolute GO path is not enough --
# the toolchain has to be on PATH for every recipe.
GOROOT_BIN := $(patsubst %/,%,$(dir $(GO)))
export PATH := $(GOROOT_BIN):$(HOME)/.local/bin:$(PATH)

APP     := Antenna
BIN     := App/$(APP)/antenna
PKG     := ./...

# Device target: Miyoo Mini Plus, ARMv7, no cgo, static.
ARMENV  := CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7
LDFLAGS := -s -w -buildid=
GOFLAGS := -buildvcs=false -trimpath -mod=readonly

.DEFAULT_GOAL := help
.PHONY: help fmt fmt-check vet lint test build build-arm package ci hooks clean tools

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk -F':.*?## ' '{printf "  \033[1m%-12s\033[0m %s\n", $$1, $$2}'

fmt: ## Format all Go code
	$(GO) fmt $(PKG)

fmt-check: ## Fail if any file is not gofmt-clean
	@unformatted=$$($(GOFMT) -l . 2>/dev/null); \
		if [ -n "$$unformatted" ]; then \
			echo "Not gofmt-clean:"; echo "$$unformatted"; \
			echo "Run 'make fmt'."; exit 1; \
		fi

vet: ## Run go vet
	$(GO) vet $(PKG)

lint: ## Run golangci-lint
	$(GOLANGCI) run

test: ## Run tests
	$(GO) test $(PKG)

build: ## Build for the host (quick compile check)
	$(GO) build $(GOFLAGS) -o /dev/null .

build-arm: ## Cross-compile the device binary
	$(ARMENV) $(GO) build $(GOFLAGS) -ldflags='$(LDFLAGS)' -o $(BIN) .
	@chmod 0755 $(BIN) App/$(APP)/launch.sh
	@file $(BIN)

package: build-arm ## Build and zip the installable App folder
	@cd App && zip -qr ../$(APP).zip $(APP)
	@echo "Wrote $(APP).zip"

ci: fmt-check vet lint test build-arm ## Everything CI runs
	@echo "CI checks passed."

hooks: ## Install the git pre-commit hook
	@mkdir -p .git/hooks
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Installed .git/hooks/pre-commit"

tools: ## Print how to install the dev tools
	@echo "Go:            https://go.dev/dl/  (or ~/.local/go)"
	@echo "golangci-lint: https://golangci-lint.run/welcome/install/"

clean: ## Remove build output
	@rm -f $(BIN) $(APP).zip
	@echo "Cleaned."
