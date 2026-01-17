# Operations Runbook

This document provides operational guidance for running and troubleshooting the Try It Now async provisioning system.

## System Overview

The system uses NATS JetStream for asynchronous task processing:

```
User Request → API → Pool Manager → NATS Queue → Worker → Container Runtime
                        ↓
                    Valkey (state)
```

**Components:**
- **API Server**: Handles HTTP requests, manages pool
- **NATS JetStream**: Message queue for async tasks
- **Valkey**: State storage (Redis-compatible)
- **Caddy**: Dynamic reverse proxy
- **Container Runtime**: Docker or Podman

## Monitoring

### Health Checks

**API Health:**
```bash
curl http://localhost:8080/health
# Expected: {"status":"ok"}
```

**NATS Health:**
```bash
# Check NATS server
curl http://localhost:8222/healthz

# Check JetStream consumers
nats consumer info PROVISIONING provision-workers
nats consumer info PROVISIONING cleanup-workers
```

**Valkey Health:**
```bash
valkey-cli ping
# Expected: PONG
```

### Key Metrics

**Pool Metrics (Prometheus):**
```
# Pool size gauges
try_it_now_pool_ready
try_it_now_pool_assigned
try_it_now_pool_warming
try_it_now_pool_target

# Provisioning counters
try_it_now_provisions_total{method="criu_restore|cold_start", status="success|failure"}
try_it_now_provision_duration_seconds
```

**Queue Metrics:**
```bash
# Check NATS stream info
nats stream info PROVISIONING

# Check pending messages
nats consumer info PROVISIONING provision-workers | grep "Pending"
nats consumer info PROVISIONING cleanup-workers | grep "Pending"
```

### Alerting Thresholds

| Metric | Warning | Critical |
|--------|---------|----------|
| Pool ready count | < 50% of target | < 20% of target |
| Provision failures | > 10% rate | > 25% rate |
| Queue depth | > 50 pending | > 100 pending |
| CRIU restore time | > 5s | > 10s |

## Queue Management

### Viewing Queue State

```bash
# List all streams
nats stream list

# View stream details
nats stream info PROVISIONING

# View consumer details
nats consumer info PROVISIONING provision-workers
nats consumer info PROVISIONING cleanup-workers

# View pending messages
nats consumer next PROVISIONING provision-workers --count 10 --no-ack
```

### Troubleshooting Stuck Messages

**Symptoms:**
- Pool not replenishing
- Queue depth growing
- Worker errors in logs

**Diagnosis:**
```bash
# Check consumer state
nats consumer info PROVISIONING provision-workers

# Look for redeliveries
nats consumer info PROVISIONING provision-workers | grep "Redelivered"

# Check server logs for handler errors
journalctl -u try-it-now | grep "Provision task failed"
```

**Resolution:**

1. **Temporary failures (network, DB):**
   - Messages will auto-retry (up to MaxDeliver times)
   - Fix underlying issue and wait for retry

2. **Persistent failures (bad checkpoint, config):**
   ```bash
   # Terminate stuck messages
   nats consumer msg rm PROVISIONING provision-workers --seq <seq_number>

   # Or purge the stream (DESTRUCTIVE)
   nats stream purge PROVISIONING --force
   ```

3. **Consumer stuck:**
   ```bash
   # Restart the server to recreate consumers
   systemctl restart try-it-now
   ```

### Clearing Queue Backlog

```bash
# View backlog
nats stream info PROVISIONING | grep "Messages"

# Purge all messages (use with caution)
nats stream purge PROVISIONING --force

# Purge only old messages
nats stream purge PROVISIONING --seq <keep_after_seq>
```

## Graceful Shutdown

### Normal Shutdown

The server handles graceful shutdown on SIGTERM:

```bash
# Send graceful shutdown
systemctl stop try-it-now

# Or manually
kill -TERM $(pidof try-it-now)
```

**Shutdown sequence:**
1. Stop accepting new requests
2. Drain NATS connections
3. Wait for in-flight handlers (10s timeout)
4. Stop pool replenisher
5. Close database connections

### Emergency Shutdown

```bash
# Immediate stop (may lose in-flight work)
kill -9 $(pidof try-it-now)
```

**Recovery after emergency shutdown:**
1. Check for orphaned containers: `make clean-demos`
2. Verify pool state: `curl http://localhost:8080/api/v1/pool/stats`
3. Queue will auto-recover on restart

## Common Operations

### Draining the Pool

Before maintenance or updates:

```bash
# Stop new acquisitions (not yet implemented - use nginx/LB)
# Wait for assigned instances to expire or release

# Clear pool data
make clean-data
```

### Recovering from Data Loss

If Valkey data is lost:

1. Stop the server
2. Clean up orphaned containers: `docker ps -a | grep demo- | awk '{print $1}' | xargs docker rm -f`
3. Clear Caddy routes: `curl -X DELETE http://localhost:2019/config/apps/http/servers/demo`
4. Start the server (pool will replenish from scratch)

### Updating Checkpoints

1. Create new checkpoint on staging server
2. Test restore on staging
3. Copy checkpoint to production: `scp checkpoint.tar.gz prod:/var/lib/checkpoints/`
4. Update configuration: `CHECKPOINT_PATH=/var/lib/checkpoints/checkpoint.tar.gz`
5. Restart server: `systemctl restart try-it-now`

### Scaling Workers

Adjust `QUEUE_WORKER_COUNT` in environment:

```bash
# More workers for higher throughput
QUEUE_WORKER_COUNT=8

# Fewer workers to reduce resource usage
QUEUE_WORKER_COUNT=2
```

Restart required for changes to take effect.

## Troubleshooting Guide

### Pool Not Replenishing

**Check:**
1. NATS connection: `journalctl -u try-it-now | grep NATS`
2. Worker errors: `journalctl -u try-it-now | grep "Provision task failed"`
3. Container runtime: `docker ps` or `podman ps`
4. Available ports: check Valkey `available_ports` set

**Common causes:**
- CRIU checkpoint not found
- Database connection failed
- No available ports
- Container image not pulled

### High Acquire Latency

**Check:**
1. Pool ready count: should be > 0
2. Replenisher frequency: default 10s
3. Provision time: check `provision_duration_seconds` metric

**Solutions:**
- Increase `POOL_TARGET_SIZE`
- Lower `POOL_REPLENISH_THRESHOLD`
- Use CRIU if not enabled

### Memory Usage Growing

**Check:**
1. Container count: `docker ps | wc -l`
2. Orphaned containers: containers without matching instances

**Solutions:**
- Verify TTL expiration is working
- Check cleanup handler logs
- Run `make clean-demos` if needed

### NATS Consumer Not Processing

**Check:**
1. Consumer exists: `nats consumer list PROVISIONING`
2. Consumer pending: `nats consumer info PROVISIONING provision-workers`
3. Server logs: `journalctl -u try-it-now | grep "worker"`

**Solutions:**
- Restart server to recreate consumers
- Check NATS server health
- Verify stream subjects match

## Runbook Checklist

### Daily
- [ ] Check pool stats: ready count should match target
- [ ] Review error logs: `journalctl -u try-it-now --since today | grep -i error`
- [ ] Verify metrics are being collected

### Weekly
- [ ] Review provision success rates
- [ ] Check for queue backlog trends
- [ ] Clean up any orphaned containers

### Monthly
- [ ] Review and update checkpoint (if image updated)
- [ ] Check disk usage for checkpoint storage
- [ ] Review and tune pool size based on usage

### On Deployment
- [ ] Verify health endpoint
- [ ] Check pool starts replenishing
- [ ] Confirm NATS consumers created
- [ ] Test acquire/release cycle

## Contact and Escalation

For issues not covered in this runbook:

1. Check server logs: `journalctl -u try-it-now -f`
2. Review recent changes in git
3. Check NATS/Valkey/Container runtime health
4. Escalate to on-call engineer

## References

- [NATS JetStream Documentation](https://docs.nats.io/nats-concepts/jetstream)
- [Valkey Commands](https://valkey.io/commands/)
- [CRIU Checkpoint Guide](./criu-checkpoint.md)
- [Project README](../README.md)
