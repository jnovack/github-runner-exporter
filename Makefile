include scripts/go.mk

.DEFAULT_GOAL := build

.PHONY: all
all: vet test build ## Vet, test, and build

.PHONY: docker
docker: ## Build multi-stage Docker image
	docker build -t $(APPLICATION):$(VERSION) .

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
