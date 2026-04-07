include scripts/variables.mk

GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0

.PHONY: build
build: ## Build binary to bin/$(APPLICATION)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "$(GO_LDFLAGS)" \
		-o bin/$(APPLICATION)-$(GOOS)-$(GOARCH) \
		./cmd/$(APPLICATION)/main.go

.PHONY: test
test: ## Run tests
	go test -count=1 ./...

.PHONY: test-race
test-race: ## Run tests with race detector
	go test -race -count=1 ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage report
	go test -cover -count=1 ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/

.PHONY: debug-variables
debug-variables: ## Print computed build variables
	@echo "APPLICATION  = $(APPLICATION)"
	@echo "PACKAGE      = $(PACKAGE)"
	@echo "VERSION      = $(VERSION)"
	@echo "REVISION     = $(REVISION)"
	@echo "BRANCH       = $(BRANCH)"
	@echo "BUILD_RFC3339= $(BUILD_RFC3339)"
	@echo "GOOS         = $(GOOS)"
	@echo "GOARCH       = $(GOARCH)"
