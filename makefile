.PHONY: build clean deploy

# Build both Lambda functions
build: build-api build-worker

# Build the API Lambda
build-api:
	@echo "Building API Lambda function..."
	GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -o bootstrap ./cmd/main.go
	zip -j function.zip bootstrap

# Build the worker Lambda
build-worker:
	@echo "Building worker Lambda function..."
	GOOS=linux GOARCH=amd64 go build -tags lambda.norpc -o worker ./cmd/worker/main.go
	zip -j worker.zip worker

# Clean build artifacts
clean:
	rm -f bootstrap worker function.zip worker.zip

# Deploy with Terraform
deploy: build
	terraform apply