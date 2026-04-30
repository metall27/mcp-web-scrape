.PHONY: help build run test stop clean docker-build docker-run docker-stop docker-logs docker-clean

# Default target
help:
	@echo "Available targets:"
	@echo "  make build         - Build Go binary"
	@echo "  make run           - Run server locally"
	@echo "  make test          - Run tests"
	@echo "  make clean         - Clean build artifacts"
	@echo "  make docker-build  - Build Docker image"
	@echo "  make docker-run    - Run Docker container"
	@echo "  make docker-stop   - Stop Docker container"
	@echo "  make docker-logs   - Show Docker logs"
	@echo "  make docker-clean  - Remove Docker containers and images"
	@echo "  make docker-push   - Push Docker image to registry"

# Local development
build:
	@echo "Building mcp-web-scrape..."
	@go build -o mcp-web-scrape ./cmd/server
	@echo "Build complete!"

run:
	@echo "Starting mcp-web-scrape..."
	@./mcp-web-scrape

test:
	@echo "Running tests..."
	@go test -v ./...

clean:
	@echo "Cleaning build artifacts..."
	@rm -f mcp-web-scrape
	@echo "Clean complete!"

# Docker targets
docker-build:
	@echo "Building Docker image..."
	@docker build -t mcp-web-scrape:latest .
	@echo "Docker image built successfully!"

docker-run:
	@echo "Running Docker container..."
	@docker run -d -p 8080:8080 --name mcp-server mcp-web-scrape:latest
	@echo "Container started! Access at http://localhost:8080"

docker-stop:
	@echo "Stopping Docker container..."
	@docker stop mcp-server || true
	@docker rm mcp-server || true
	@echo "Container stopped and removed!"

docker-logs:
	@docker logs -f mcp-server

docker-clean:
	@echo "Cleaning up Docker resources..."
	@docker stop mcp-server 2>/dev/null || true
	@docker rm mcp-server 2>/dev/null || true
	@docker rmi mcp-web-scrape:latest 2>/dev/null || true
	@echo "Docker cleanup complete!"

docker-compose-up:
	@echo "Starting services with docker-compose..."
	@docker-compose up -d
	@echo "Services started!"

docker-compose-down:
	@echo "Stopping services..."
	@docker-compose down
	@echo "Services stopped!"

docker-compose-logs:
	@docker-compose logs -f

docker-compose-restart:
	@echo "Restarting services..."
	@docker-compose restart
	@echo "Services restarted!"

# Development targets
dev:
	@echo "Starting development server..."
	@MCP_WEB_SCRAPE_LOG_LEVEL=debug ./mcp-web-scrape

dev-docker:
	@echo "Starting development container..."
	@docker-compose up --build

# CI/CD targets
ci-test:
	@echo "Running CI tests..."
	@go test -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html

ci-lint:
	@echo "Running linters..."
	@golangci-lint run ./...

ci-build:
	@echo "Building for multiple platforms..."
	@GOOS=linux GOARCH=amd64 go build -o mcp-web-scrape-linux-amd64 ./cmd/server
	@GOOS=linux GOARCH=arm64 go build -o mcp-web-scrape-linux-arm64 ./cmd/server
	@GOOS=darwin GOARCH=amd64 go build -o mcp-web-scrape-darwin-amd64 ./cmd/server
	@GOOS=darwin GOARCH=arm64 go build -o mcp-web-scrape-darwin-arm64 ./cmd/server
	@GOOS=windows GOARCH=amd64 go build -o mcp-web-scrape-windows-amd64.exe ./cmd/server

# Utility targets
check-deps:
	@echo "Checking dependencies..."
	@go mod verify
	@go mod tidy

update-deps:
	@echo "Updating dependencies..."
	@go get -u ./...
	@go mod tidy

format:
	@echo "Formatting code..."
	@go fmt ./...
	@gofmt -w .

vet:
	@echo "Running go vet..."
	@go vet ./...

install-tools:
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
