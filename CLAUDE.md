# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

PrestaShop Demo Multiplexer - An instant-provisioning system for PrestaShop e-commerce trial instances. Uses warm pool architecture to achieve sub-500ms perceived startup by pre-warming containerized instances and assigning them on-demand.

**Status:** Phase 1 complete - core infrastructure working, ready for integration testing.

## Build & Development Commands

```bash
# Setup
make init              # Copy .env.example → .env, download Go modules

# Infrastructure (required for development)
make infra-up          # Start Docker Compose (Caddy, Valkey, NATS, MariaDB)
make infra-down        # Stop infrastructure
make infra-logs        # Stream infrastructure logs

# Development
make dev               # Run with Docker backend (CONTAINER_MODE=docker)
make build             # Compile to build/demo-multiplexer
make run               # Build + execute

# Testing
make test              # Run all tests: go test -v ./...
make test-coverage     # Generate coverage HTML report

# Code quality
make lint              # Run golangci-lint
make fmt               # Format code
make tidy              # Tidy go.mod
```

**Running a single test:**
```bash
go test -v -run TestAcquireFromPool ./internal/store/...
```

**Cleanup (IMPORTANT):**
```bash
make stop              # Force kill all server processes
make clean-demos       # Remove orphan demo containers
make clean-data        # Flush Valkey data (instances, pool, rate limits)
make clean-all         # Full cleanup: stop + build + demos + data
```

**Note:** Always run `make stop` before `make clean-demos`. When the server is killed, demo containers become orphans (TTL cleanup requires running server). Use `make clean-all` for complete reset between test sessions.

## Architecture

```
HTTP API (Gin)
     │
     ├── Pool Manager ─── Container Runtime (Docker/Podman)
     │         │
     │         └── Repository (Valkey) ─── State persistence
     │
     └── Caddy Route Manager ─── Dynamic reverse proxy
```

**Key flow - Instant Acquire:**
1. `POST /api/v1/demo/acquire` → Pool Manager
2. `Repository.AcquireFromPool()` → Valkey LPOP (O(1))
3. Instance already warm with Caddy route → Return URL immediately
4. Background replenisher maintains pool size

**Instance lifecycle:** `warming → ready → assigned → expired`

## Package Structure

| Package | Purpose |
|---------|---------|
| `cmd/server` | Application entrypoint, wires components |
| `internal/api` | HTTP handlers, request/response types |
| `internal/config` | Environment-based configuration |
| `internal/container` | Docker runtime (Podman+CRIU planned) |
| `internal/domain` | Core types: Instance, Pool, errors |
| `internal/pool` | Pool Manager: acquire, release, replenish |
| `internal/proxy` | Caddy REST API integration |
| `internal/store` | Valkey repository implementation |
| `internal/queue` | Message queue interface (NATS planned) |

## Key Implementation Details

**Valkey (internal/store/valkey.go):**
- Pool queue: LPUSH/LPOP for O(1) acquire
- Port allocation: SPOP/SADD set operations
- Rate limiting: INCR with hourly/daily counters
- Instance storage: JSON serialization with HSET/HGET

**Pool Manager (internal/pool/impl.go):**
- `StartReplenisher()` - background goroutine, polls every 10s
- `Acquire()` - instant return from warm pool
- `Release()` - stops container, removes route, releases port

**Caddy Routes (internal/proxy/caddy.go):**
- REST API to localhost:2019/config
- Routes added dynamically during warm-up
- Uses @id for efficient route targeting

## API Endpoints

- `POST /api/v1/demo/acquire` - Get instance from pool (rate limited)
- `GET /api/v1/demo/:id` - Instance details
- `POST /api/v1/demo/:id/extend` - Extend TTL
- `DELETE /api/v1/demo/:id` - Early release
- `GET /api/v1/demo/:id/status` - SSE TTL countdown
- `GET /api/v1/pool/stats` - Pool statistics
- `GET /health` - Health check
- `GET /metrics` - Prometheus metrics

## Configuration

Environment variables loaded from `.env` (see `.env.example`):

- **Pool:** `POOL_TARGET_SIZE`, `POOL_MIN_SIZE`, `POOL_MAX_SIZE`, `POOL_DEFAULT_TTL`
- **Container:** `CONTAINER_MODE` (docker/podman), `CONTAINER_IMAGE`, `CONTAINER_PORT_RANGE_*`
- **Proxy:** `CADDY_ADMIN_URL`, `BASE_DOMAIN`
- **Store:** `VALKEY_ADDR`, `VALKEY_PASSWORD`
- **Rate limits:** `RATE_LIMIT_HOURLY`, `RATE_LIMIT_DAILY`

## Tech Stack

- **Go 1.25** with Gin 1.11 (HTTP framework)
- **Valkey 8** (Redis-compatible state store)
- **Docker** (container runtime, Podman+CRIU planned)
- **Caddy 2** (dynamic reverse proxy)
- **MariaDB 10.11** (shared PrestaShop database)
- **prestashop/prestashop-flashlight:9.0.0** (fast-boot PrestaShop image)

## Issue Tracking

This project uses **bd** (beads) for git-backed issue tracking:
```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress
bd close <id>         # Complete work
bd sync               # Sync with git
```
