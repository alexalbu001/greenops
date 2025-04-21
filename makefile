.PHONY: build cli install clean

# Configuration
BINARY_NAME=greenops
CLI_DIR=./cmd/cli
LAMBDA_DIR=./cmd
PKG_DIR=./pkg
DIST_DIR=./dist

# Default target
all: build

# Build both CLI and Lambda
build: cli lambda

# Build CLI only
cli:
	@echo "Building GreenOps CLI..."
	@mkdir -p $(DIST_DIR)
	go build -o $(DIST_DIR)/$(BINARY_NAME) $(CLI_DIR)

# Build Lambda function
lambda:
	@echo "Building Lambda function..."
	@mkdir -p $(DIST_DIR)
	cd $(LAMBDA_DIR) && GOOS=linux GOARCH=amd64 go build -o ../$(DIST_DIR)/bootstrap main.go
	cd $(DIST_DIR) && zip function.zip bootstrap

# Install CLI to $GOPATH/bin
install: cli
	@echo "Installing GreenOps CLI to GOPATH bin directory..."
	@cp $(DIST_DIR)/$(BINARY_NAME) $(GOPATH)/bin/
	@echo "Installation complete. Run '$(BINARY_NAME)' to start."

# Run tests
test:
	@echo "Running tests..."
	go test ./...

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(DIST_DIR)

# Help information
help:
	@echo "GreenOps Makefile"
	@echo ""
	@echo "Targets:"
	@echo "  all      - Build both CLI and Lambda (default)"
	@echo "  cli      - Build CLI only"
	@echo "  lambda   - Build Lambda function only"
	@echo "  install  - Install CLI to GOPATH bin directory"
	@echo "  test     - Run tests"
	@echo "  clean    - Remove build artifacts"
	@echo "  help     - Show this help message"