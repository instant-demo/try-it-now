# Try It Now

**Every app deserves a Try It Now button.**

> Instant application provisioning. Sub-20ms on-demand instances from warm pool.

A warm-pool provisioning system that eliminates container startup latency. Pre-warm any containerized application and serve instances on-demand via O(1) queue operations.

## Benchmarks

| Operation | Latency | Notes |
|-----------|---------|-------|
| Acquire (warm pool) | **~10-20ms** | Valkey LPOP + route lookup |
| Acquire (cold) | ~45-60s | Container start + health check |
| Pool replenish | Background | No user-facing latency |
| TTL extend | **~5ms** | Valkey HSET |
| Release | **~100ms** | Container stop + cleanup |

*Warm pool maintains target size automatically. Users never hit cold path under normal load.*

## Features

- **Instant provisioning** - Sub-500ms instance acquisition from warm pool
- **API key authentication** - Secure access with `X-API-Key` header
- **Request tracing** - `X-Request-ID` header for distributed tracing
- **Rate limiting** - Configurable hourly/daily limits per IP
- **Prometheus metrics** - Full observability at `/metrics`
- **Graceful shutdown** - Proper cleanup of all resources
- **Security hardened** - Localhost-only bindings, trusted proxy config

## Use Cases

- **SaaS trials** - Instant product demos for prospect conversion
- **E-commerce platforms** - PrestaShop, WooCommerce, Magento trials
- **Marketplace demos** - Plugin/theme/module showcases (PrestaShop addons, WooCommerce extensions)
- **Open source projects** - Instant "try it now" demos for any OSS project
- **Development sandboxes** - Isolated test environments
- **Training environments** - Pre-configured learning instances
- **CI/CD preview** - Branch-specific demo deployments

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
- **MariaDB 10.11** (optional, for stateful applications)
- **NATS JetStream** (async message queue)

## Installation

### Production Deployment

```bash
# Build binary
make build

# Copy binary and config
cp build/try-it-now /usr/local/bin/
cp .env.example /etc/try-it-now/.env
chmod 600 /etc/try-it-now/.env

# Edit config (set API_KEY, TRUSTED_PROXIES, database credentials)
vim /etc/try-it-now/.env

# Run (systemd unit in deployments/systemd/)
try-it-now
```

### Infrastructure Requirements

- **Valkey/Redis** - State persistence
- **Caddy 2** - Reverse proxy with admin API enabled
- **Docker or Podman** - Container runtime
- **MariaDB** - Database for stateful apps (optional)
- **NATS** - Message queue (optional)

See `deployments/docker-compose.yml` for reference infrastructure setup.

## Development

```bash
make test              # Run unit tests (79 tests)
make test-coverage     # Generate coverage HTML report
make lint              # Run golangci-lint
make build             # Compile to build/try-it-now
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

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/your-feature`)
3. Run tests (`make test && make lint`)
4. Commit with clear messages
5. Open a PR against `main`

See [CLAUDE.md](./CLAUDE.md) for architecture details and coding conventions.

## Known Limitations

- **Queue handlers** - NATS provision/cleanup handlers are stubs. Provisioning currently uses synchronous pool replenishment. Queue-based async provisioning is planned.

## Roadmap

**Analytics & Insights**
- Demo engagement tracking (session duration, feature usage)
- Conversion funnel metrics
- PostHog/Umami integration

**Management & Operations**
- TUI dashboard for pool monitoring
- Admin API for fleet management
- Multi-tenant support

**Platform Integrations**
- Pocketbase backend provisioning
- Encore.dev distributed system demos
- SST one-click cloud deployment
- Coolify self-hosted PaaS mode

**Infrastructure**
- Kubernetes operator
- Terraform provider
- ARM64 support

## Built With

- **[Valkey](https://valkey.io)** - Redis-compatible state store (pool queue, rate limiting)
- **[Caddy](https://caddyserver.com)** - Automatic HTTPS reverse proxy
- **[NATS JetStream](https://nats.io)** - Async message queue for provisioning
- **[CRIU](https://criu.org)** - Checkpoint/restore for instant container startup (Podman)
- **[Gin](https://gin-gonic.com)** - High-performance HTTP framework

## License

MIT
