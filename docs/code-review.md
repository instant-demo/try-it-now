# Code Review: Try It Now - Production Readiness

**Date**: 2026-01-17
**Reviewers**: Claude (Opus 4.5), Bot
**Commit**: b260f05
**Scope**: Comprehensive technical review including Phase 3 Async Processing

---

## Summary

Found **3 Critical**, **6 High**, **6 Medium**, and **5 Low** severity issues. The most serious are non-atomic state transitions in Valkey that can cause data corruption under concurrent load, and a SQL injection vulnerability pattern in database operations.

---

## CRITICAL Issues

### 1. Non-Atomic State Transitions Cause Data Corruption

**Location**: `internal/store/valkey.go:154-196`

**Problem**: `UpdateInstanceState()` performs a read-modify-write sequence across 4 separate Redis commands (GET → SREM → SET → SADD). This is not atomic. Under concurrent access, two goroutines can interleave their operations, corrupting state sets.

**Scenario**:
1. Goroutine A reads instance (state=ready), removes from `state:ready`
2. Goroutine B reads same instance (still sees state=ready), removes from `state:ready` (already gone - no error)
3. Goroutine A sets instance to assigned, adds to `state:assigned`
4. Goroutine B sets instance back to ready, adds to `state:ready`
5. Instance now in both `state:assigned` AND `state:ready` sets, or neither

**Impact**:
- Instance appears in wrong state sets (phantom instances in pool)
- Pool exhaustion despite instances existing
- Data corruption accumulates over time
- **Blast radius**: All tenants
- **Likelihood**: High under production load (100s of concurrent acquires)

**Fix**:
```go
// Use Lua script for atomic read-modify-write
func (r *ValkeyRepository) UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error {
    script := `
        local instance = redis.call('GET', KEYS[1])
        if not instance then return nil end
        local data = cjson.decode(instance)
        local oldState = data.state
        if oldState == ARGV[1] then return 'OK' end
        redis.call('SREM', KEYS[2]..oldState, ARGV[2])
        data.state = ARGV[1]
        if ARGV[1] == 'assigned' and not data.assigned_at then
            data.assigned_at = ARGV[3]
        end
        redis.call('SET', KEYS[1], cjson.encode(data))
        redis.call('SADD', KEYS[2]..ARGV[1], ARGV[2])
        return 'OK'
    `
    // Execute as atomic Lua script
    return r.client.Do(ctx, r.client.B().Eval().Script(script)...).Error()
}
```

---

### 2. AcquireFromPool Can Lose Instances on Partial Failure

**Location**: `internal/store/valkey.go:198-221`

**Problem**: `AcquireFromPool()` does LPOP (atomic), then calls `UpdateInstanceState()` (non-atomic). If the process crashes or UpdateInstanceState fails after LPOP but before completing, the instance is removed from the pool but never marked as assigned. The instance is now "lost" - not in pool, not tracked as assigned.

**Scenario**:
1. LPOP succeeds - instance ID removed from `pool:ready`
2. Process crashes / network error / timeout during UpdateInstanceState
3. Instance is gone from ready pool, but state never updated
4. Instance container keeps running, using resources
5. No way to recover without manual intervention

**Impact**:
- Resource leak (containers run forever)
- Port exhaustion (ports never released)
- Data inconsistency
- **Blast radius**: Single instance per occurrence, but accumulates
- **Likelihood**: Medium (happens on any failure during state transition)

**Fix**:
```go
func (r *ValkeyRepository) AcquireFromPool(ctx context.Context) (*domain.Instance, error) {
    // Use Lua script for atomic pop-and-update
    script := `
        local id = redis.call('LPOP', KEYS[1])
        if not id then return nil end
        local key = KEYS[2]..id
        local data = redis.call('GET', key)
        if not data then
            -- Instance record missing, put ID back
            redis.call('RPUSH', KEYS[1], id)
            return nil
        end
        local instance = cjson.decode(data)
        redis.call('SREM', KEYS[3]..instance.state, id)
        instance.state = 'assigned'
        instance.assigned_at = ARGV[1]
        redis.call('SET', key, cjson.encode(instance))
        redis.call('SADD', KEYS[3]..'assigned', id)
        return data
    `
    // Keys: pool:ready, instance:, state:
    // Args: current timestamp
}
```

---

### 3. SQL Injection Vulnerability in Database Prefix Interpolation

**Location**: `internal/database/prestashop.go:47-68, 73-88, 92-133`

**Problem**: The `dbPrefix` parameter is directly interpolated into SQL queries using `fmt.Sprintf` without validation or parameterization. While the prefix is currently generated internally from UUIDs, this pattern is dangerous and one code change away from critical.

**Scenario**:
```go
// If dbPrefix ever comes from external source:
dbPrefix := "d12345678_; DROP TABLE users; --"
// Results in:
// UPDATE d12345678_; DROP TABLE users; --shop_url SET domain = ?
```

**Impact**:
- Complete database compromise if injection occurs
- Data destruction across ALL tenants (shared database)
- **Blast radius**: System-wide
- **Likelihood**: Low currently (internal UUID generation), but architectural vulnerability

**Fix**:
```go
// Validate prefix format strictly
func validateDBPrefix(prefix string) error {
    // Only allow: d[0-9a-f]{8}_
    if !regexp.MustCompile(`^d[0-9a-f]{8}_$`).MatchString(prefix) {
        return fmt.Errorf("invalid db prefix format: %s", prefix)
    }
    return nil
}

func (p *PrestaShopDB) UpdateDomain(ctx context.Context, dbPrefix, newDomain string) error {
    if err := validateDBPrefix(dbPrefix); err != nil {
        return err
    }
    // Proceed with query...
}
```

---

## HIGH Issues

### 4. Rate Limit Check-Then-Increment is Non-Atomic

**Location**: `internal/store/valkey.go:399-447`, `internal/api/handler.go:201-225, 268-271`

**Problem**: Rate limiting does CHECK separately from INCREMENT, and the increment happens AFTER successful acquire. Multiple concurrent requests from the same IP can all pass the check before any increment occurs.

**Scenario**:
1. IP has limit of 10/hour, currently at 9
2. 5 concurrent requests arrive simultaneously
3. All 5 check limit: 9 < 10, all pass
4. All 5 acquire instances successfully
5. All 5 increment counter to 10, 11, 12, 13, 14
6. IP got 5 instances when should only get 1 more

**Impact**:
- Rate limits ineffective under concurrent requests
- Resource abuse by single actor
- **Blast radius**: Per-IP, but affects pool availability for all
- **Likelihood**: High (concurrent requests are common)

**Fix**:
```go
// Atomic check-and-increment using Lua
func (r *ValkeyRepository) CheckAndIncrementRateLimit(ctx context.Context, ip string, hourlyLimit, dailyLimit int) (bool, error) {
    script := `
        local hourKey = KEYS[1]
        local dayKey = KEYS[2]
        local hourLimit = tonumber(ARGV[1])
        local dayLimit = tonumber(ARGV[2])

        local hourCount = tonumber(redis.call('GET', hourKey) or '0')
        local dayCount = tonumber(redis.call('GET', dayKey) or '0')

        if hourCount >= hourLimit or dayCount >= dayLimit then
            return 0  -- Denied
        end

        redis.call('INCR', hourKey)
        redis.call('EXPIRE', hourKey, 3600)
        redis.call('INCR', dayKey)
        redis.call('EXPIRE', dayKey, 86400)
        return 1  -- Allowed
    `
    result, err := r.client.Do(ctx, r.client.B().Eval().Script(script)
        .Keys(keyRateLimitHour+ip, keyRateLimitDay+ip)
        .Args(strconv.Itoa(hourlyLimit), strconv.Itoa(dailyLimit))
        .Build()).ToInt64()
    return result == 1, err
}
```

---

### 5. Over-Provisioning Race in TriggerReplenish

**Location**: `internal/pool/impl.go:221-244`

**Problem**: `TriggerReplenish()` reads pool stats, calculates `needed`, then provisions that many instances in a loop. Between reading stats and completing provisioning, other goroutines (NATS handlers, other replenish triggers) may also provision instances. The stats snapshot becomes stale.

**Scenario**:
1. Pool has 2 ready, target is 5, so needed=3
2. TriggerReplenish starts provisioning 3 instances
3. Meanwhile, NATS handler also starts provisioning (ProvisionOne called)
4. Meanwhile, ticker fires another TriggerReplenish
5. All goroutines provision based on stale "needed=3" calculation
6. Pool ends up with 2 + 3 + 1 + 3 = 9 instances (target was 5)

**Impact**:
- Resource waste (over-provisioned containers)
- Potential port exhaustion faster than expected
- Increased costs
- **Blast radius**: System-wide resource consumption
- **Likelihood**: High under normal operation (replenisher + NATS handlers run concurrently)

**Fix**:
```go
// Re-check pool state before each provision
func (m *PoolManager) TriggerReplenish(ctx context.Context) error {
    for {
        stats, err := m.Stats(ctx)
        if err != nil {
            return err
        }

        if stats.ReplenishmentNeeded() <= 0 {
            return nil  // Pool is full enough
        }

        // Provision ONE instance
        if err := m.provisionInstance(ctx); err != nil {
            m.logger.Warn("Failed to provision instance", "error", err)
            return err  // Stop on first failure
        }
        // Loop will re-check stats before next provision
    }
}
```

---

### 6. TOCTOU in ExtendTTL Allows Exceeding Max TTL

**Location**: `internal/api/handler.go:347-383`

**Problem**: `extendDemo()` gets instance, checks if extension would exceed max TTL, then extends. Concurrent requests can both pass the check and both extend, exceeding the max TTL limit.

**Scenario**:
1. Instance has 45 minutes remaining, max TTL is 60 minutes
2. Two concurrent extend requests for 10 minutes each
3. Both check: 45 + 10 = 55 < 60, both pass
4. Both extend, final TTL = 45 + 10 + 10 = 65 minutes > max

**Impact**:
- Instances can exceed intended max lifetime
- Resource consumption beyond policy
- **Blast radius**: Per-instance
- **Likelihood**: Medium (requires concurrent extend requests)

**Fix**:
```go
// Use atomic check-and-extend in store
func (r *ValkeyRepository) ExtendInstanceTTLWithMax(ctx context.Context, id string, extension, maxTTL time.Duration) error {
    script := `
        local key = KEYS[1]
        local data = redis.call('GET', key)
        if not data then return {err='not found'} end
        local instance = cjson.decode(data)
        local now = tonumber(ARGV[1])
        local ext = tonumber(ARGV[2])
        local maxTTL = tonumber(ARGV[3])
        local expires = instance.expires_at or now
        local remaining = expires - now
        if remaining + ext > maxTTL then
            return {err='exceeds max'}
        end
        instance.expires_at = expires + ext
        redis.call('SET', key, cjson.encode(instance))
        return {ok=instance.expires_at}
    `
}
```

---

### 7. Port Allocation Leak on Provision Failure

**Location**: `internal/pool/impl.go:291-411, 362-364, 380-381, 396-397`

**Problem**: Port is allocated early in provisioning. If failure occurs at various points (Start() fails, health check fails, or between AllocatePort and Start()), port release may be missed or fail silently. The comment says "port will be reclaimed on next pool initialization" but `InitializePorts()` only runs if the ports set is empty.

**Scenario**:
1. AllocatePort succeeds, port removed from available set
2. Some failure occurs before provisionInstance completes
3. Port release is missed or fails silently
4. Repeat many times over days of operation
5. Port pool slowly exhausts

**Impact**:
- Gradual port exhaustion
- System unable to provision new instances
- **Blast radius**: System-wide
- **Likelihood**: Medium (depends on failure frequency)

**Fix**:
```go
func (m *PoolManager) provisionInstance(ctx context.Context) error {
    port, err := m.repo.AllocatePort(ctx)
    if err != nil {
        return fmt.Errorf("failed to allocate port: %w", err)
    }

    // Ensure port is released on any failure
    var succeeded bool
    defer func() {
        if !succeeded {
            if err := m.repo.ReleasePort(ctx, port); err != nil {
                m.logger.Error("Failed to release port on provision failure",
                    "port", port, "error", err)
            }
        }
    }()

    // ... rest of provisioning logic ...

    // Only after AddToPool succeeds
    succeeded = true
    return nil
}
```

---

### 8. Missing Panic Recovery in Background Goroutines

**Location**: `internal/pool/impl.go:200`, `internal/queue/nats.go:237-250`, `cmd/server/main.go:148-165`

**Problem**: Background goroutines (`replenishLoop`, NATS workers, metrics updater) lack `recover()` protection. A panic will crash the entire process.

**Scenario**:
1. Nil pointer dereference in `Stats()` during metrics update
2. Panic propagates, unrecovered
3. Server crashes, all instances become orphaned

**Impact**:
- Complete service outage from edge-case bugs
- **Blast radius**: System-wide
- **Likelihood**: Low (requires panic-inducing bug)

**Fix**:
```go
go func() {
    defer func() {
        if r := recover(); r != nil {
            m.logger.Error("Recovered from panic in replenishLoop",
                "panic", r, "stack", debug.Stack())
        }
    }()
    m.replenishLoop(ctx)
}()
```

---

### 9. Potential Goroutine Leak in replenishLoop

**Location**: `internal/pool/impl.go:248-283`

**Problem**: When `ctx` is cancelled but `stopCh` is not closed, the `replenishCh` select case can still trigger work with a cancelled context. Additionally, if `TriggerReplenish` blocks indefinitely (e.g., on slow Valkey), workers accumulate.

**Impact**:
- Delayed shutdown, wasted resources
- **Likelihood**: Medium

**Fix**:
```go
for {
    select {
    case <-m.stopCh:
        return
    case <-ctx.Done():
        return
    // ... rest of cases
    }
}
```

---

## MEDIUM Issues

### 10. Caddy Server Creation Race Condition

**Location**: `internal/proxy/caddy.go:267-319`

**Problem**: `ensureServerExists()` checks if server exists, then creates if not. Multiple concurrent `AddRoute()` calls can all see server doesn't exist and all try to create it.

**Impact**: First request succeeds, others fail with errors, routes not added, instances unusable.

**Fix**: Use mutex for first-time server creation, or handle "already exists" response gracefully.

---

### 11. SSE Stream Has No Maximum Duration

**Location**: `internal/api/handler.go:441-499`

**Problem**: `demoStatus()` SSE handler runs until instance expires or client disconnects. No maximum stream duration. A malicious client could keep connections open indefinitely.

**Impact**: Connection exhaustion, goroutine accumulation.

**Fix**: Add maximum stream duration timeout (e.g., 30 minutes).

---

### 12. SetInstanceTTL Failure Leaves Instance Without Expiry

**Location**: `internal/pool/impl.go:88-95`

**Problem**: After Acquire(), if `SetInstanceTTL()` fails, the code logs a warning but continues. The instance has no TTL set and will never expire via automatic cleanup.

**Impact**: Zombie instances that run forever until manual release.

**Fix**: Consider making TTL setting mandatory, or add background job to find instances without expiry.

---

### 13. NATS Worker Message Processing Blocks Stop Signal

**Location**: `internal/queue/nats.go:284-286, 338-340`

**Problem**: While iterating `msgs.Messages()` channel, the worker can't check `stopCh`. Long-running handler processing blocks graceful shutdown.

**Impact**: Delayed shutdown, potential message reprocessing after partial completion.

**Fix**: Process messages with context cancellation awareness.

---

### 14. SaveInstance After Acquire Overwrites State

**Location**: `internal/api/handler.go:276-277`

**Problem**: After Acquire() returns an instance, the handler sets `UserIP` and calls `SaveInstance()`. But SaveInstance() writes the entire instance object, potentially overwriting state changes made by concurrent operations. Error is also silently ignored.

**Impact**: State changes lost, incomplete audit trails, analytics gaps.

**Fix**: Use atomic update for just the UserIP field:
```go
if err := h.store.SaveInstance(ctx, instance); err != nil {
    h.logger.Warn("Failed to save user IP on instance", "instanceID", instance.ID, "error", err)
}
```

---

### 15. SetTrustedProxies Error Silently Ignored

**Location**: `internal/api/handler.go:43-46`

**Problem**: If `SetTrustedProxies` fails, the error is silently ignored. This could leave the server vulnerable to IP spoofing.

**Impact**: Potential rate limit bypass via X-Forwarded-For spoofing if config is invalid.

**Fix**:
```go
if err := r.SetTrustedProxies(h.cfg.Server.TrustedProxies); err != nil {
    log.Warn("Failed to set trusted proxies", "error", err)
}
```

---

## LOW Issues

### 16. Type Assertion Without Check

**Location**: `internal/api/middleware.go:74`

**Problem**: Type assertion without ok check could panic if context value is corrupted.

```go
return id.(string)  // Could panic if type is wrong
```

**Fix**:
```go
if id, exists := c.Get(RequestIDKey); exists {
    if s, ok := id.(string); ok {
        return s
    }
}
return ""
```

---

### 17. Missing Context Propagation in NATS Workers

**Location**: `internal/queue/nats.go:304, 358`

**Problem**: Workers create new contexts with `context.Background()` instead of using a parent context. This prevents proper cancellation propagation during shutdown.

**Fix**:
```go
func (c *NATSConsumer) processProvisionMessage(msg jetstream.Msg, workerID int, parentCtx context.Context) {
    ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
    defer cancel()
    // ...
}
```

---

### 18. Inconsistent Error Handling in ListByState

**Location**: `internal/store/valkey.go:264-269`

**Problem**: Skips instances silently on `ErrInstanceNotFound` but returns early on other errors. This asymmetry may hide data corruption issues.

---

### 19. HTTP Client Shared Without Connection Limits

**Location**: `internal/proxy/caddy.go:32-34`

**Problem**: Caddy HTTP client has 10-second timeout but no connection pooling configuration. Under high load, could exhaust ephemeral ports.

---

### 20. PodmanRuntime Connection Context Reuse

**Location**: `internal/container/podman.go:27`

**Problem**: The Podman connection uses a `context.Context` stored as a struct field, created once at startup and reused. This is correct for Podman but unusual - worth documenting.

---

## Positive Patterns Worth Keeping

1. **replenishCh buffer of 1** (`impl.go:37, 75`): Prevents goroutine accumulation from multiple triggers
2. **Constant-time API key comparison** (`middleware.go:39`): Prevents timing attacks
3. **Shared HTTP client for health checks** (`docker.go:33`): Connection reuse, proper timeouts
4. **Idempotent cleanup operations** (`handlers.go:66-129`): Errors logged but don't fail the cleanup
5. **UUID-based identifiers** (`impl.go:483-491`): Prevents enumeration attacks
6. **Compile-time interface checks** (`valkey.go:495-505`): Catches interface drift at compile time

---

## Systemic Patterns Needing Refactoring

1. **Non-atomic Valkey operations**: Multiple read-modify-write patterns should be consolidated into Lua scripts or use proper Redis transactions.

2. **Error handling inconsistency**: Some errors are ignored with `_`, some logged at warn, some returned. Establish a clear policy for when to ignore vs propagate vs log.

3. **Goroutine lifecycle management**: Background workers need consistent recovery and cancellation patterns. Consider a worker abstraction with built-in recovery, metrics, and graceful shutdown.

---

## Summary by Severity

| Severity | Count |
|----------|-------|
| Critical | 3 |
| High | 6 |
| Medium | 6 |
| Low | 5 |
| **Total** | **20** |

---

## Priority Remediation Order

### Immediate (before production)
1. Non-atomic state transitions → Lua scripts (highest blast radius)
2. AcquireFromPool atomicity → Lua script
3. SQL injection protection → add validation

### Week 1
4. Atomic rate limiting
5. Port allocation cleanup guarantees
6. Panic recovery in goroutines

### Week 2
7. Over-provisioning race fix
8. TTL extension atomic check
9. Goroutine lifecycle improvements

### Backlog
10. Medium/Low issues as capacity allows
