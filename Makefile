.PHONY: all build clean proto-gen lint test docker-build docker-push helm-deploy

# Variables
BINARY_NAME=long-gw
IMAGE_NAME=long-gw
IMAGE_TAG?=latest
IMAGE_REGISTRY?=ghcr.io/vadam-zhan
DOCKERFILE=Dockerfile
HELMChart=deploy/helm/long-gw

# Go parameters
GOBUILD=go build
GOTEST=go test
GOGET=go get
GOMOD=go mod
GOVET=go vet

# Build flags
LDFLAGS=-ldflags="-w -s"
GOFLAGS=-gcflags="all=-trimpath=/" -asmflags="all=-trimpath=/"

# Build directories
BUILD_DIR=./bin
BUILD_BIN=$(BUILD_DIR)/$(BINARY_NAME)

# OS/Arch for cross-compilation
GOOS=linux
GOARCH=amd64

all: clean build

## build: Build the binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) $(GOBUILD) $(GOFLAGS) $(LDFLAGS) -o $(BUILD_BIN) main.go
	@echo "Binary built: $(BUILD_BIN)"

local-build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(GOFLAGS) $(LDFLAGS) -o $(BUILD_BIN) main.go
	@echo "Binary built: $(BUILD_BIN)"

## clean: Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f $(BINARY_NAME)
	@echo "Clean complete"

## proto-gen: Generate protobuf code
proto-gen:
	@echo "Generating protobuf code..."
	buf generate

## lint: Run linter
lint:
	@echo "Running linter..."
	$(GOVET) ./...

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v -race ./...

## tidy: Tidy go modules
tidy:
	@echo "Tidying modules..."
	$(GOMOD) tidy

## build-all: Build for multiple platforms
build-all:
	@echo "Building for multiple platforms..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 main.go
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 main.go

# ================================================
# Docker targets
# ================================================

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)..."
	docker build -t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) .

## docker-buildx: Build Docker image with buildx for multi-arch
docker-buildx:
	@echo "Building Docker image with buildx..."
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		-t $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) \
		--push .

## docker-push: Push Docker image to registry
docker-push:
	@echo "Pushing Docker image..."
	docker push $(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

## docker-run: Run container locally
docker-run:
	@echo "Running container..."
	docker run -p 8080:8080 -p 8081:8081 \
		-v $(PWD)/etc/config.yaml:/app/config.yaml \
		--name $(BINARY_NAME) \
		$(IMAGE_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

## docker-stop: Stop running container
docker-stop:
	@echo "Stopping container..."
	docker stop $(BINARY_NAME) || true
	docker rm $(BINARY_NAME) || true

# ================================================
# Helm targets
# ================================================

## helm-lint: Lint Helm chart
helm-lint:
	@echo "Linting Helm chart..."
	helm lint $(HELMChart)

## helm-template: Render Helm templates
helm-template:
	@echo "Rendering Helm templates..."
	helm template $(BINARY_NAME) $(HELMChart)

## helm-deploy: Deploy to Kubernetes
helm-deploy:
	@echo "Deploying to Kubernetes..."
	helm upgrade --install $(BINARY_NAME) $(HELMChart) \
		--namespace long-gw \
		--create-namespace \
		--set image.repository=$(IMAGE_REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait --timeout 5m

## helm-uninstall: Uninstall from Kubernetes
helm-uninstall:
	@echo "Uninstalling from Kubernetes..."
	helm uninstall $(BINARY_NAME) -n long-gw || true

## helm-upgrade: Upgrade existing deployment
helm-upgrade:
	@echo "Upgrading Helm release..."
	helm upgrade $(BINARY_NAME) $(HELMChart) \
		--namespace long-gw \
		--set image.repository=$(IMAGE_REGISTRY)/$(IMAGE_NAME) \
		--set image.tag=$(IMAGE_TAG) \
		--wait --timeout 5m

# ================================================
# Development helpers
# ================================================

## run: Run locally with config
run: build
	@echo "Running $(BINARY_NAME)..."
	./$(BUILD_BIN) gateway --config etc/config.yaml

## reload: Build and run with hot reload (requires air)
reload:
	@which air > /dev/null || (echo "Installing air..." && go install github.com/cosmtrek/air@latest)
	air

## fmt: Format code
fmt:
	@echo "Formatting code..."
	gofmt -s -w .
	goimports -w .

# ================================================
# Help
# ================================================

## help: Show this help
help:
	@echo "Available targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
