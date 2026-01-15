## Context

Phase 1 of the PrestaShop Demo Multiplexer is partially complete. The foundation is in place with all interfaces defined and tested.

## What's Done (Session 1)

### Project Structure ✅
- Go module initialized (`github.com/boss/demo-multiplexer`)
- Full directory structure created per plan
- Git repository initialized

### Core Interfaces ✅
- `internal/domain/` - Instance, PoolStats, errors
- `internal/container/runtime.go` - Runtime interface (RestoreFromCheckpoint, Start, Stop)
- `internal/store/repository.go` - Repository interface (pool ops, TTL, rate limiting)
- `internal/proxy/route.go` - RouteManager interface (AddRoute, RemoveRoute)
- `internal/pool/manager.go` - Manager interface (Acquire, Release, Stats)
- `internal/queue/queue.go` - Publisher/Consumer interfaces

### Configuration ✅
- `internal/config/config.go` - Full config with env var loading
- `.env.example` - All environment variables documented

### API Skeleton ✅
- `internal/api/handler.go` - Gin router with all endpoints (returning 501 for now)
- `cmd/server/main.go` - Entry point with graceful shutdown

### Infrastructure ✅
- `deployments/docker-compose.yml` - Caddy, Valkey, NATS, MariaDB
- `deployments/Caddyfile` - Base config with admin API enabled
- `Makefile` - build, test, dev, infra-up/down commands

### Tests ✅ (All Passing)
- `internal/domain/instance_test.go` - Instance methods
- `internal/domain/pool_test.go` - PoolStats methods  
- `internal/config/config_test.go` - Config loading
- `internal/api/handler_test.go` - API routes

## What's Next (Continue Phase 1)

### 1. Implement Valkey Repository
File: `internal/store/valkey.go`
- Connect to Valkey using `github.com/valkey-io/valkey-go`
- Implement all Repository interface methods
- Use LPUSH/LPOP for atomic pool operations
- Enable keyspace notifications for TTL expiry

### 2. Implement Container Runtime (Docker mode)
File: `internal/container/docker.go`
- Implement Runtime interface using Docker SDK
- This is the dev/fallback mode (no CRIU)
- Use `github.com/docker/docker/client`

### 3. Implement Caddy Route Manager
File: `internal/proxy/caddy.go`
- HTTP client for Caddy admin API (localhost:2019)
- POST/DELETE routes dynamically

### 4. Implement Pool Manager
File: `internal/pool/impl.go`
- Implement Manager interface
- Wire up Repository, Runtime, RouteManager
- Background replenishment loop

### 5. Wire Up API Handlers
- Inject dependencies into Handler
- Implement acquireDemo, getDemo, etc.

## How to Continue

```bash
# Start infrastructure
make infra-up

# Run in dev mode
make dev

# Run tests
make test
```

## Reference

- Plan file: `/Users/boss/.claude/plans/purring-tickling-quail.md`
- Research: `research/second-research.md` (architecture decisions)
