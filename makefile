.PHONY: build clean deploy

# Build both Lambda functions
build: build-api build-worker build-cli

# Build the API Lambda
build-api:
	@echo "Building API Lambda function..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
	  -tags lambda.norpc \
	  -o bootstrap \
	  ./cmd/main.go
	zip -j function.zip bootstrap

# Build the worker Lambda
build-worker:
	@echo "Building worker Lambda function..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
	  -tags lambda.norpc \
	  -o bootstrap \
	  ./cmd/worker/main.go
	zip -j worker.zip bootstrap

build-cli:
	@echo "Building CLI..."
	go build -o greenops ./cmd/cli/main.go 

# Clean build artifacts
clean:
	rm -f bootstrap function.zip worker.zip

# Deploy with Terraform
deploy:
	terraform apply -auto-approve
