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

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Build Docker Virtual Kubelet image
docker-build-kubelet: build-virtual-kubelet
	@echo "Building Docker images..."
	docker build -f deploy/Dockerfile -t public.ecr.aws/m4v1f8q5/gpu-provider/virtual-kubelet:$(VERSION) .
	docker push public.ecr.aws/m4v1f8q5/gpu-provider/virtual-kubelet:$(VERSION)


# Build Docker Virtual Kubelet image
docker-build-gateway: build-gateway-init build-gateway-controller
	@echo "Building Docker images..."
	docker build -f deploy/gateway.Dockerfile -t public.ecr.aws/m4v1f8q5/gpu-provider/gateway:$(VERSION) .
	docker push public.ecr.aws/m4v1f8q5/gpu-provider/gateway:$(VERSION)

# Build all Docker images
docker-build: docker-build-kubelet docker-build-gateway

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
	@echo "  test                   - Run tests"
	@echo "  docker-build           - Build Docker images"
	@echo "  help                   - Show this help message"
