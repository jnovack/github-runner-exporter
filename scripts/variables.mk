APPLICATION  ?= $(shell basename $(CURDIR))
PACKAGE      ?= $(shell git remote get-url origin 2>/dev/null | sed 's|.*github.com[:/]\(.*\)\.git|\1|' || echo "github.com/jnovack/$(APPLICATION)")
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
REVISION     ?= $(shell git rev-parse HEAD 2>/dev/null || echo "none")
BRANCH       ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
BUILD_RFC3339 ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

GO_LDFLAGS   := -s -w \
	-X main.version=$(VERSION) \
	-X main.revision=$(REVISION) \
	-X main.buildRFC3339=$(BUILD_RFC3339)
