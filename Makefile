.DEFAULT_GOAL := help

# ==== Build metadata ====
GO ?= go
GOFMT ?= gofmt

BINARY_NAME ?= hyperfleet-desire-store
BINARY_PATH ?= bin/$(BINARY_NAME)

BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_SHA ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
APP_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")

GOFLAGS ?= -trimpath
LDFLAGS := -s -w \
	-X main.version=$(APP_VERSION) \
	-X main.commit=$(GIT_SHA) \
	-X main.date=$(BUILD_DATE)

# FIPS-compliant builds are mandatory for shipped HyperFleet services: the
# boringcrypto experiment makes the Red Hat Go toolchain use the system
# OpenSSL, which is why the runtime base image is ubi-minimal and not ubi-micro.
CGO_ENABLED ?= 1
GOEXPERIMENT ?= boringcrypto

# ==== Container image ====
QUAY_ORG ?= openshift-hyperfleet
IMAGE_REGISTRY ?= quay.io/$(QUAY_ORG)
IMAGE_NAME ?= $(BINARY_NAME)
IMAGE_TAG ?= $(APP_VERSION)
IMG ?= $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
PLATFORM ?= linux/amd64
BASE_IMAGE ?= registry.access.redhat.com/ubi9/ubi-minimal:latest
CONTAINER_TOOL ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

# ==== Go tools ====
# Every tool is pinned in tools/go.mod and invoked through the module, so a
# clean checkout gets the same versions the presubmits use.
TOOL_MOD := tools/go.mod
gotool = "$(GO)" tool -modfile="$(TOOL_MOD)" $(1)
GOLANGCI_LINT := $(call gotool,golangci-lint)

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: build
build: ## Build the service binary into bin/
	CGO_ENABLED=$(CGO_ENABLED) GOEXPERIMENT=$(GOEXPERIMENT) \
		$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_PATH) ./cmd/$(BINARY_NAME)

.PHONY: run
run: build ## Run the service locally
	./$(BINARY_PATH) serve --log-format text

.PHONY: test
test: ## Run unit tests with the race detector
	$(GO) test -race -coverprofile=coverage.out ./...

.PHONY: test-all
test-all: fmt-check vet lint test ## Run every check a presubmit runs

.PHONY: cover
cover: test ## Show the coverage report
	$(GO) tool cover -func=coverage.out

.PHONY: fmt
fmt: ## Format Go code
	$(GOFMT) -s -w .

.PHONY: gofmt
gofmt: fmt ## Alias for fmt (hyperfleet-gofmt hook)

.PHONY: fmt-check
fmt-check: ## Fail if Go code is not formatted
	@diff=$$($(GOFMT) -s -d .); \
	if [ -n "$$diff" ]; then \
		echo "Code is not formatted. Run 'make fmt' to fix:"; \
		echo "$$diff"; \
		exit 1; \
	fi

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: go-vet
go-vet: vet ## Alias for vet (hyperfleet-go-vet hook)

.PHONY: lint
lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/ coverage.out

.PHONY: install-hooks
install-hooks: ## Install the pre-commit hooks
	pre-commit install

##@ Dependencies

.PHONY: tidy
tidy: ## Tidy go.mod
	$(GO) mod tidy

.PHONY: tools
tools: ## Re-resolve the pinned tool module
	cd tools && "$(GO)" mod tidy

.PHONY: verify-tools
verify-tools: tools ## Fail if tools/go.mod drifted from the source it pins
	@git diff --exit-code HEAD -- tools/go.mod tools/go.sum || \
		(echo "tool modules out of date; run 'make tools'" && exit 1)

##@ Container Images

.PHONY: check-container-tool
check-container-tool:
ifndef CONTAINER_TOOL
	@echo "Error: no container tool found (podman or docker)"
	@exit 1
endif

.PHONY: image
image: check-container-tool ## Build the container image
	$(CONTAINER_TOOL) build \
		--platform $(PLATFORM) \
		--build-arg GIT_SHA=$(GIT_SHA) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg APP_VERSION=$(APP_VERSION) \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		-t $(IMG) .

.PHONY: image-push
image-push: check-container-tool ## Push the container image
	$(CONTAINER_TOOL) push $(IMG)

##@ Verification

.PHONY: validate-tekton
validate-tekton: ## Parse the Konflux PipelineRuns as YAML
	$(call gotool,yq) eval '.' .tekton/*.yaml > /dev/null
	@echo "OK: .tekton PipelineRuns parse"

.PHONY: ci-validate
ci-validate: fmt-check vet lint test validate-tekton ## Run everything CI runs, except the image build
