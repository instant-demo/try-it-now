# PrestaShop Demo Multiplexer

Instant-provisioning system for PrestaShop e-commerce trial instances. Achieves sub-500ms perceived startup using warm pool architecture with pre-warmed containerized instances.

**Status:** v1.0.0 - Production ready with authentication, security hardening, and full test coverage.

## Features

- **Instant provisioning** - Sub-500ms instance acquisition from warm pool
- **API key authentication** - Secure access with `X-API-Key` header
- **Request tracing** - `X-Request-ID` header for distributed tracing
- **Rate limiting** - Configurable hourly/daily limits per IP
- **Prometheus metrics** - Full observability at `/metrics`
- **Graceful shutdown** - Proper cleanup of all resources
- **Security hardened** - Localhost-only bindings, trusted proxy config

## Quick Start

```bash
# Setup
make init              # Copy .env.example -> .env, download Go modules

# Start infrastructure
make infra-up          # Start Docker Compose (Caddy, Valkey, NATS, MariaDB)

# Run server
make dev               # Run with Docker backend
```

## Architecture

```
HTTP API (Gin)
     |
     +-- Pool Manager --- Container Runtime (Docker/Podman)
     |         |
     |         +-- Repository (Valkey) --- State persistence
     |
     +-- Caddy Route Manager --- Dynamic reverse proxy
```

**Instant Acquire Flow:**
1. `POST /api/v1/demo/acquire` -> Pool Manager
2. Valkey LPOP from warm pool (O(1))
3. Instance already has Caddy route -> Return URL immediately

**Instance lifecycle:** `warming -> ready -> assigned -> expired`

## API Endpoints

All `/api/v1/*` endpoints require `X-API-Key` header when `API_KEY` is configured.

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | /api/v1/demo/acquire | Get instance from pool (rate limited) |
| GET | /api/v1/demo/:id | Instance details |
| POST | /api/v1/demo/:id/extend | Extend TTL |
| DELETE | /api/v1/demo/:id | Release instance |
| GET | /api/v1/demo/:id/status | SSE TTL countdown |
| GET | /api/v1/pool/stats | Pool statistics |
| GET | /health | Health check (public) |
| GET | /metrics | Prometheus metrics |

## Tech Stack

- **Go 1.25** with Gin 1.11 (HTTP framework)
- **Valkey 8** (Redis-compatible state store)
- **Docker** (container runtime, Podman+CRIU supported)
- **Caddy 2** (dynamic reverse proxy)
- **MariaDB 10.11** (shared PrestaShop database)
- **NATS JetStream** (async message queue)

## Development

```bash
make test              # Run unit tests (79 tests)
make test-coverage     # Generate coverage HTML report
make lint              # Run golangci-lint
make build             # Compile to build/demo-multiplexer
make clean-all         # Full cleanup

# Integration tests (requires infrastructure)
VALKEY_TEST=1 go test ./internal/store/...
go test -tags=e2e ./tests/integration/...
```

## Configuration

See `.env.example` for all configuration options. Key settings:

| Variable | Description | Default |
|----------|-------------|---------|
| `API_KEY` | API authentication key | (disabled) |
| `TRUSTED_PROXIES` | Comma-separated proxy IPs | 127.0.0.1 |
| `POOL_TARGET_SIZE` | Warm instances to maintain | 10 |
| `CONTAINER_MODE` | docker or podman | docker |
| `BASE_DOMAIN` | Domain for instance URLs | localhost |

## Documentation

See [CLAUDE.md](./CLAUDE.md) for detailed development instructions, package structure, and implementation details.

## License

MIT
