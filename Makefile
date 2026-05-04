.PHONY: all build build-virtual-kubelet build-gateway-init build-gateway-controller build-container-agent clean docker-build docker-build-kubelet docker-build-gateway docker-push docker-push-kubelet docker-push-gateway docker-all kind-start kind-stop test test-gateway test-one test-integration setup-envtest install-controller-gen

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

# Run gateway controller integration tests
test-gateway: setup-envtest
	@echo "Running gateway controller integration tests..."
	# KUBEBUILDER_ASSETS=$(ENVTEST_ASSETS_DIR) \
	export KUBEBUILDER_ASSETS="/home/skarupa/.local/share/kubebuilder-envtest/k8s/1.35.0-linux-amd64"
	go test -v -timeout 120s ./internal/gateway/... -run "^Test"

# Run a specific test (usage: make test-one TEST=TestVirtualServiceBasicLifecycle)
test-one: setup-envtest
	@echo "Running test $(TEST)..."
	KUBEBUILDER_ASSETS=$(ENVTEST_ASSETS_DIR) \
		go test -v -timeout 120s ./internal/gateway/... -run "^$(TEST)$$"

# Integration test: spins up kind cluster, loads images, installs chart, checks containers.
# Requires: kind, helm, kubectl, docker; plus locally-built virtual-kubelet:latest and gateway:latest.
# Build images with:
#   docker build -f deploy/Dockerfile         -t virtual-kubelet:latest .
#   docker build -f deploy/gateway.Dockerfile -t gateway:latest         .
# Set KEEP_CLUSTER=1 to leave the cluster on failure for debugging.
test-integration:
	go test -tags integration -v -timeout 20m ./test/integration/...

# Envtest assets directory
ENVTEST_ASSETS_DIR ?= $(shell pwd)/.envtest

# Setup envtest binaries
# If automatic download fails, you can manually install:
# 1. Install setup-envtest: go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.18
# 2. Run: $(go env GOPATH)/bin/setup-envtest use 1.30.x --bin-dir .envtest
# Or set KUBEBUILDER_ASSETS environment variable to point to existing binaries
setup-envtest:
	@mkdir -p $(ENVTEST_ASSETS_DIR)
	@if [ ! -f $(ENVTEST_ASSETS_DIR)/kube-apiserver ]; then \
		echo "Setting up envtest binaries..."; \
		if command -v setup-envtest >/dev/null 2>&1; then \
			ENVTEST_PATH=$$(setup-envtest use 1.30.x -p path 2>/dev/null || echo ""); \
			if [ -n "$$ENVTEST_PATH" ] && [ -f "$$ENVTEST_PATH/kube-apiserver" ]; then \
				ln -sf "$$ENVTEST_PATH"/* $(ENVTEST_ASSETS_DIR)/; \
				echo "Envtest binaries linked from $$ENVTEST_PATH"; \
			else \
				echo "WARNING: setup-envtest failed. Trying GOPATH version..."; \
				ENVTEST_PATH=$$($(shell go env GOPATH)/bin/setup-envtest use 1.30.x -p path 2>/dev/null || echo ""); \
				if [ -n "$$ENVTEST_PATH" ] && [ -f "$$ENVTEST_PATH/kube-apiserver" ]; then \
					ln -sf "$$ENVTEST_PATH"/* $(ENVTEST_ASSETS_DIR)/; \
					echo "Envtest binaries linked from $$ENVTEST_PATH"; \
				else \
					echo ""; \
					echo "ERROR: Failed to download envtest binaries automatically."; \
					echo "Please install manually using one of these methods:"; \
					echo ""; \
					echo "  1. Install setup-envtest and run:"; \
					echo "     go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.18"; \
					echo "     \$$(go env GOPATH)/bin/setup-envtest use 1.30.x --bin-dir $(ENVTEST_ASSETS_DIR)"; \
					echo ""; \
					echo "  2. Set KUBEBUILDER_ASSETS to existing kubebuilder binaries:"; \
					echo "     export KUBEBUILDER_ASSETS=/path/to/kubebuilder/bin"; \
					echo ""; \
					exit 1; \
				fi; \
			fi; \
		else \
			echo "setup-envtest not found. Installing..."; \
			go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.18; \
			ENVTEST_PATH=$$($(shell go env GOPATH)/bin/setup-envtest use 1.30.x -p path 2>/dev/null || echo ""); \
			if [ -n "$$ENVTEST_PATH" ] && [ -f "$$ENVTEST_PATH/kube-apiserver" ]; then \
				ln -sf "$$ENVTEST_PATH"/* $(ENVTEST_ASSETS_DIR)/; \
				echo "Envtest binaries linked from $$ENVTEST_PATH"; \
			else \
				echo ""; \
				echo "ERROR: Failed to download envtest binaries automatically."; \
				echo "Please install manually. See 'make help' for instructions."; \
				exit 1; \
			fi; \
		fi; \
	else \
		echo "Envtest binaries already present in $(ENVTEST_ASSETS_DIR)"; \
	fi

# Build local virtual-kubelet Docker image
docker-build-kubelet: build-virtual-kubelet
	@echo "Building virtual-kubelet Docker image..."
	docker build -f deploy/Dockerfile -t virtual-kubelet:latest .

# Build local gateway Docker image
docker-build-gateway: build-gateway-init build-gateway-controller
	@echo "Building gateway Docker image..."
	docker build -f deploy/gateway.Dockerfile -t gateway:latest .

# Build all local Docker images
docker-build: docker-build-kubelet docker-build-gateway

# Tag and push virtual-kubelet image to ECR
docker-push-kubelet: docker-build-kubelet
	@echo "Pushing virtual-kubelet image as $(VERSION)..."
	docker tag virtual-kubelet:latest public.ecr.aws/m4v1f8q5/gpu-provider/virtual-kubelet:$(VERSION)
	docker push public.ecr.aws/m4v1f8q5/gpu-provider/virtual-kubelet:$(VERSION)

# Tag and push gateway image to ECR
docker-push-gateway: docker-build-gateway
	@echo "Pushing gateway image as $(VERSION)..."
	docker tag gateway:latest public.ecr.aws/m4v1f8q5/gpu-provider/gateway:$(VERSION)
	docker push public.ecr.aws/m4v1f8q5/gpu-provider/gateway:$(VERSION)

# Push all Docker images to ECR
docker-push: docker-push-kubelet docker-push-gateway

# Full Docker pipeline: build and push all images
docker-all: docker-push

# Create local kind cluster and load images
kind-start:
	kind create cluster --name local --config test/kind-config.yaml
	kubectl cluster-info --context kind-local
	kind load docker-image virtual-kubelet:latest --name local
	kind load docker-image gateway:latest --name local

# Delete local kind cluster
kind-stop:
	kind delete cluster --name local

helm-release:
	@echo "Packaging and pushing Helm chart..."
	helm package deploy/chart
	helm push helm-* oci://public.ecr.aws/m4v1f8q5/gpu-provider
	rm helm-*

# Install controller-gen if not present
install-controller-gen:
	@if ! [ -x "$$(command -v controller-gen)" ]; then \
    	echo "Installing controller-gen..."; \
        go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5; \
	fi


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
	@echo "  test                   - Run all tests"
	@echo "  test-gateway           - Run gateway controller integration tests"
	@echo "  test-one TEST=<name>   - Run a specific test (e.g., make test-one TEST=TestVirtualServiceBasicLifecycle)"
	@echo "  setup-envtest          - Download envtest binaries (kube-apiserver, etcd)"
	@echo "  docker-build           - Build all local Docker images"
	@echo "  docker-build-kubelet   - Build local virtual-kubelet:latest image"
	@echo "  docker-build-gateway   - Build local gateway:latest image"
	@echo "  docker-push            - Tag and push all images to ECR"
	@echo "  docker-push-kubelet    - Tag and push virtual-kubelet image to ECR"
	@echo "  docker-push-gateway    - Tag and push gateway image to ECR"
	@echo "  docker-all             - Build and push all Docker images"
	@echo "  kind-start             - Start local Kind cluster and load ANEX images"
	@echo "  kind-stop              - Stop and delete local Kind cluster"
	@echo "  helm-release           - Pack Helm and push it to registry"
	@echo "  install-controller-gen - Install controller-gen tool"
	@echo "  help                   - Show this help message"
