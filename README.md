# PrestaShop Demo Multiplexer

Instant-provisioning system for PrestaShop e-commerce trial instances. Achieves sub-500ms perceived startup using warm pool architecture with pre-warmed containerized instances.

**Status:** Phase 1 complete - core infrastructure working, ready for integration testing.

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

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | /api/v1/demo/acquire | Get instance from pool (rate limited) |
| GET | /api/v1/demo/:id | Instance details |
| POST | /api/v1/demo/:id/extend | Extend TTL |
| DELETE | /api/v1/demo/:id | Release instance |
| GET | /api/v1/demo/:id/status | SSE TTL countdown |
| GET | /api/v1/pool/stats | Pool statistics |
| GET | /health | Health check |
| GET | /metrics | Prometheus metrics |

## Tech Stack

- **Go 1.25** with Gin 1.11 (HTTP framework)
- **Valkey 8** (Redis-compatible state store)
- **Docker** (container runtime, Podman+CRIU planned)
- **Caddy 2** (dynamic reverse proxy)
- **MariaDB 10.11** (shared PrestaShop database)
- **NATS JetStream** (async message queue)

## Development

```bash
make test              # Run all tests
make test-coverage     # Generate coverage HTML report
make lint              # Run golangci-lint
make build             # Compile to build/demo-multiplexer
make clean-all         # Full cleanup
```

## Configuration

See `.env.example` for all configuration options. Key settings:

- `POOL_TARGET_SIZE` - Number of warm instances to maintain
- `CONTAINER_MODE` - docker or podman
- `BASE_DOMAIN` - Domain for instance URLs
- `RATE_LIMIT_HOURLY` / `RATE_LIMIT_DAILY` - Rate limiting

## Documentation

See [CLAUDE.md](./CLAUDE.md) for detailed development instructions, package structure, and implementation details.
