.PHONY: all build build-virtual-kubelet build-gateway-init build-gateway-controller build-container-agent clean docker-build docker-push test

# Version can be overridden
VERSION ?= latest

# Binary output directory
BIN_DIR := bin

# Envs
export GO111MODULE := on
export GOPROXY := https://proxy.golang.org

# Go build flags
GO_BUILD_FLAGS := -v
LDFLAGS := -w -s -extldflags=-static

all: build

# Build all binaries
build: build-virtual-kubelet build-gateway-init build-gateway-controller build-container-agent

# Build virtual-kubelet binary
build-virtual-kubelet:
	@echo "Building virtual-kubelet..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/virtual_kubelet ./cmd/virtual-kubelet

# Build gateway-init binary
build-gateway-init:
	@echo "Building gateway-init..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/gateway_init ./cmd/gateway-init

# Build gateway-controller binary
build-gateway-controller:
	@echo "Building gateway-controller..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/gateway_controller ./cmd/gateway-controller

# Build container-agent binary
build-container-agent:
	@echo "Building container-agent..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GO_BUILD_FLAGS) -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/container_agent ./cmd/container-agent

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BIN_DIR)

# Generate CRD manifests
generate-crds:
	@echo "Generating CRD manifests..."
	@~/go/bin/controller-gen crd:crdVersions=v1 paths="./api/..." output:crd:dir=./deploy/chart/crds

# Generate deepcopy code
generate-deepcopy:
	@echo "Generating deepcopy code..."
	@~/go/bin/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

# Generate all code
generate: generate-deepcopy generate-crds

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Build Docker images
docker-build: build
	@echo "Building Docker images..."
	docker build -f deploy/Dockerfile -t gpu-provider-virtual-kubelet:$(VERSION) .
	docker build -f deploy/gateway.Dockerfile -t gpu-provider-gateway:$(VERSION) .

# Push Docker images (requires registry configuration)
docker-push:
	@echo "Pushing Docker images..."
	docker push gpu-provider-virtual-kubelet:$(VERSION)
	docker push gpu-provider-gateway:$(VERSION)
	docker push gpu-provider-container-agent:$(VERSION)

# Install controller-gen if not present
install-controller-gen:
	@if ! [ -x "$$(command -v controller-gen)" ]; then \
		echo "Installing controller-gen..."; \
		go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5; \
	fi

# Development: build and generate everything
dev: generate build

# Help target
help:
	@echo "Available targets:"
	@echo "  all                    - Build all binaries (default)"
	@echo "  build                  - Build all binaries"
	@echo "  build-virtual-kubelet  - Build virtual-kubelet binary"
	@echo "  build-gateway-init     - Build gateway-init binary"
	@echo "  build-gateway-controller - Build gateway-controller binary"
	@echo "  build-container-agent  - Build container-agent binary"
	@echo "  clean                  - Remove build artifacts"
	@echo "  generate               - Generate CRDs and deepcopy code"
	@echo "  generate-crds          - Generate CRD manifests"
	@echo "  generate-deepcopy      - Generate deepcopy code"
	@echo "  test                   - Run tests"
	@echo "  docker-build           - Build Docker images"
	@echo "  docker-push            - Push Docker images"
	@echo "  install-controller-gen - Install controller-gen tool"
	@echo "  dev                    - Generate and build everything"
	@echo "  help                   - Show this help message"
