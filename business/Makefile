.PHONY: proto-gen build clean run test docker-build docker-push

# Build variables
BINARY_NAME=business
BUILD_DIR=bin
DOCKER_IMAGE=business:latest

## build: Build the binary
build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) .

## clean: Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

## run: Run the service
run:
	go run main.go

## test: Run tests
test:
	go test -v ./...

## docker-build: Build Docker image
docker-build:
	docker build -t $(DOCKER_IMAGE) .

## docker-push: Push Docker image
docker-push:
	docker push $(DOCKER_IMAGE)

## build-all: Build for multiple platforms
build-all:
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 .
