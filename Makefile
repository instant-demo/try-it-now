.PHONY: all build run test test-e2e clean dev infra-up infra-down lint clean-demos clean-data clean-all stop

# Build variables
BINARY_NAME=try-it-now
BUILD_DIR=build
GO=go

# Default target
all: build

# Build the binary
build:
	$(GO) build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/server

# Build for Linux (production)
build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build -o $(BUILD_DIR)/$(BINARY_NAME)-linux ./cmd/server

# Run the application
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

# Run in development mode (Go loads .env via godotenv)
dev:
	$(GO) run ./cmd/server

# Run tests
test:
	$(GO) test -v ./...

# Run E2E tests (requires: make infra-up && make dev in another terminal)
test-e2e:
	$(GO) test -v -tags=e2e -timeout 10m ./tests/integration/...

# Run tests with coverage
test-coverage:
	$(GO) test -v -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

# Run linter
lint:
	golangci-lint run ./...

# Start infrastructure (Caddy, Valkey, NATS, MariaDB)
infra-up:
	docker compose -f deployments/docker-compose.yml up -d

# Stop infrastructure
infra-down:
	docker compose -f deployments/docker-compose.yml down

# View infrastructure logs
infra-logs:
	docker compose -f deployments/docker-compose.yml logs -f

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Initialize development environment
init:
	cp .env.example .env
	$(GO) mod download
	@echo "Development environment initialized. Run 'make infra-up' to start infrastructure."

# Format code
fmt:
	$(GO) fmt ./...

# Tidy modules
tidy:
	$(GO) mod tidy

# Stop all running server processes
stop:
	@pkill -9 -f $(BINARY_NAME) 2>/dev/null || true
	@echo "Server processes stopped"

# Clean up demo containers (orphans from killed server)
clean-demos:
	@docker rm -f $$(docker ps -aq --filter name=demo-demo) 2>/dev/null || true
	@echo "Demo containers cleaned"

# Clean Valkey data (instances, pool, rate limits, counters)
clean-data:
	@docker exec deployments-valkey-1 valkey-cli FLUSHDB 2>/dev/null || true
	@echo "Valkey data cleaned"

# Clean everything (stop server + build artifacts + demos + data)
clean-all: stop clean clean-demos clean-data
	@echo "Full cleanup complete"
