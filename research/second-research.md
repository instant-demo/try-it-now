# Instant-provisioning demo environments: a 2026 architecture guide

**You can achieve sub-500ms perceived startup—even 0ms—by combining warm pools, checkpoint/restore, and modern Rust/Go components.** The key insight is that traditional "cold start" optimization matters less than clever pre-warming. This report provides production-ready configurations for replacing your Traefik/Docker/Redis/FastAPI stack with alternatives that are 5-10x faster, specifically deployable on Hetzner VPS.

The critical architecture pattern: maintain a pool of pre-warmed instances, instantly assign one when users click a demo link, and replenish asynchronously. This eliminates cold start entirely from the user's perspective. Combined with ZFS/Btrfs snapshots for instant cloning and CRIU for checkpoint/restore, you can provision new instances in the background in 100-300ms.

---

## Reverse proxy: Caddy beats Traefik for dynamic routing

**Caddy 2.x emerges as the optimal choice** for instant-provisioning scenarios where routes must be added dynamically within milliseconds. Its REST API enables atomic, zero-downtime configuration changes at `localhost:2019/load`, with new routes becoming active instantly.

| Proxy | Dynamic config speed | Request latency (p50) | Memory | Production ready |
|-------|---------------------|----------------------|--------|------------------|
| **Caddy 2.x** | Instant (API call) | ~5.7ms | ~40MB | ✅ Yes |
| Traefik 3.x | Auto-discovery | ~9ms | Higher | ✅ Yes |
| Pingora | Requires code change | ~2-5ms | Minimal | Library only |
| NGINX Unit | API-based, instant | N/A | Low | ⚠️ **Archived Oct 2025** |

**Caddy's key advantages for demo provisioning include** built-in automatic HTTPS without overhead, atomic configuration rollbacks on failure, and granular API updates via path traversal (`/config/apps/http/servers/example/routes/0`). The ~33,500 RPS throughput is more than sufficient for demo environments, while using **70% less CPU** than Traefik under equivalent load.

For maximum raw performance, **Cloudflare's Pingora** (github.com/cloudflare/pingora) achieves 70% less CPU and 67% less memory than NGINX, with 5ms TTFB reduction at p50. However, Pingora is a Rust library requiring custom development—not a ready-to-deploy binary. The **River proxy** (github.com/memorysafety/river), built on Pingora, aimed to provide batteries-included functionality but development is currently paused. **Avoid NGINX Unit entirely**—the project was archived in October 2025 and is unmaintained.

---

## Container runtime: CRIU checkpoint/restore achieves sub-200ms starts

**On Hetzner Cloud VPS, the winning combination is Podman + CRIU** for checkpoint/restore. Firecracker's ~125ms boot time is impressive but requires KVM access, which Hetzner Cloud VPS does not provide (only Hetzner Dedicated/Bare Metal at ~€120/month supports it).

| Technology | Cold start | VPS compatible | Best use case |
|------------|-----------|----------------|---------------|
| **CRIU + Podman** | 50-200ms restore | ✅ Yes | Production demos |
| Firecracker | ~125ms | ❌ Bare metal only | Serverless/FaaS |
| containerd + nerdctl | ~100-120ms | ✅ Yes | Skip Docker overhead |
| Docker | ~150ms | ✅ Yes | Avoid—daemon overhead |
| gVisor | Low | ✅ Yes | **Avoid**—2.2-2.8x syscall overhead |
| Kata Containers | 1-2 seconds | ❌ Bare metal only | Security-first |

**CRIU enables instant container resume** by freezing a running container's complete state—memory, file descriptors, network connections—and serializing it to disk. Restore times of **50-200ms** are achievable depending on container size. Integration with Podman is native:

```bash
# Checkpoint a warmed PrestaShop container
podman container checkpoint prestashop --export=/tmp/checkpoint.tar.gz --tcp-established

# Restore on demand (50-200ms)
podman container restore --import=/tmp/checkpoint.tar.gz --name=demo-123
```

Key limitations include TCP connection re-establishment requirements, hostname conflicts requiring dynamic hostname checking, and cryptographic token refreshing. For demo environments where network connections can be re-established by the application, these are manageable tradeoffs.

**Using containerd directly via nerdctl** (github.com/containerd/nerdctl) eliminates 30-150ms of Docker daemon overhead. Combined with Stargz snapshotter for lazy image pulling—where containers start before full image download—this provides the fastest pure container startup path without checkpoint/restore complexity.

---

## State store: Valkey replaces Redis with 3x throughput

**Valkey 8.1+ is the recommended Redis replacement**—a Linux Foundation fork with true BSD-3-Clause licensing, backed by AWS, Google, Oracle, and Ericsson. It achieves **1.19M RPS** versus Redis's 360K RPS (a 230% improvement) while being 100% Redis 7.2 compatible.

| Store | Peak throughput | P99 latency | Redis compatible | License |
|-------|----------------|-------------|------------------|---------|
| **Valkey 8.1** | 1.19M RPS | 0.8-1ms | ✅ Full (7.2) | BSD-3 |
| DragonflyDB | **6.43M QPS** | 0.7-1ms | 240+ commands | BSL 1.1 |
| KeyDB | 1M+ RPS | <1ms | ✅ Full | BSD-3 |
| Garnet | Multi-million | <0.3ms (p99.9) | ⚠️ Partial | MIT |

**DragonflyDB** (github.com/dragonflydb/dragonfly) offers maximum throughput at **6.43M QPS** on c7gn instances—the "25x faster than Redis" claim compares single-threaded Redis to multi-threaded Dragonfly. It's 30% more memory efficient and eliminates memory spikes during snapshotting. However, its BSL 1.1 license may concern some deployments.

For demo instance tracking with TTL-based auto-cleanup, Valkey's enhanced I/O threading model provides excellent performance with familiar Redis patterns:

```redis
# Store demo instance with 24-hour TTL
SETEX demo:instance:123 86400 '{"user":"alice","module":"my-module","created":"2025-01-15T10:00:00Z"}'

# Track active sessions
HSET demo:sessions user:alice:session1 '{"instance":"123"}'
EXPIRE demo:sessions 3600
```

**Avoid KeyDB for new projects**—development appears stalled since October 2023. Microsoft's Garnet achieves exceptional <300μs p99.9 latency but has partial Redis compatibility and is newer with less ecosystem maturity.

---

## API framework: Go Gin balances speed and productivity

**Go with Gin or Fiber provides the optimal balance** of performance (~34,000-36,000 RPS), fast development, simple deployment, and low cold start for serverless-adjacent patterns. Gin is used by 48% of Go developers according to JetBrains 2025 surveys.

| Framework | Requests/sec | P99 latency | Memory | Learning curve |
|-----------|--------------|-------------|--------|----------------|
| Go Fiber | ~36,000 | ~2.8ms | Lowest | Easy (Express-like) |
| **Go Gin** | ~34,000 | ~3.0ms | Low | Easy, mature ecosystem |
| Rust Axum | ~102,000 | Sub-ms | 5.8MB | Moderate |
| Rust Actix-web | ~94,000 | Sub-ms | 8.1MB | Steeper |
| Bun + Elysia | 100,000-180,000 | Low | Higher | Easy |

**Rust frameworks dominate TechEmpower benchmarks** but require longer development cycles. **Axum** (Tokio team) has surpassed Actix-web in adoption with nearly identical performance and better memory efficiency at 5.8MB idle. For teams with Rust expertise pursuing maximum performance, Axum is the modern choice.

**Bun + Elysia** achieves 3-4x faster throughput than Node.js (100,000-180,000 RPS) with excellent TypeScript DX. If your team has strong JavaScript background, Elysia (github.com/elysiajs/elysia) provides near-Rust performance with familiar syntax. However, Bun's ecosystem is still maturing for production-critical paths.

For a provisioning API that needs to assign pre-warmed instances and manage pool state, the practical recommendation is **Go Gin**—battle-tested, extensive tooling, and sufficient performance for demo orchestration where the bottleneck is container operations, not request handling.

---

## Instant provisioning: warm pools eliminate perceived cold start

**The architecture pattern that achieves sub-500ms perceived startup is warm pools combined with ZFS clones and CRIU restore.** The key insight: users perceive instant startup because they're assigned a pre-existing instance, not waiting for provisioning.

### Warm pool architecture

```
User Request → Get Instance from Pool (0ms) → Instant Assignment
                        ↓
              Background Replenishment → CRIU Restore (100-300ms) → Add to Pool
```

**Sizing strategies for demo environments:**
- Conservative: `pool_size = peak_concurrent_users × 0.2`
- Aggressive: `pool_size = max_capacity - current_running`
- Minimum recommended: `N = max(3, expected_concurrent_demos × 0.5)`

**Replenishment should be async with watermark triggers**—when pool drops below threshold (e.g., 30% of target size), spawn background jobs to restore instances from CRIU checkpoints into ZFS clones.

### ZFS/Btrfs instant cloning

Copy-on-write filesystems enable **microsecond clone operations** for demo workspaces:

```bash
# Create ZFS pool on Hetzner dedicated server
zpool create demopool /dev/sda2

# Create golden template with PrestaShop + modules installed
zfs create demopool/template
# ... install and configure PrestaShop ...

# Snapshot template (instant)
zfs snapshot demopool/template@v1

# Clone for new demo instance (instant - microseconds)
zfs clone demopool/template@v1 demopool/demo-user-123
```

**Storage efficiency is dramatic:** 100 demo clones of a 5GB image with 100MB changes each consumes only 15GB total (vs. 500GB with full copies). Btrfs provides similar capabilities natively on Linux without ZFS kernel module requirements.

### How cloud providers achieve instant start

**Fly.io Machines** achieve ~10ms local start and ~300ms global start by pre-creating stopped machines and starting them on demand—creation (slow, involves image pull and host selection) is separated from start (fast, just resume). Their pattern:

1. Pre-create stopped machines with your demo image
2. Start machines on HTTP request arrival (proxy auto-boot)
3. Process exits when idle → machine stops → scale to zero

**AWS Lambda SnapStart** uses Firecracker microVM snapshots to reduce cold starts from several seconds to sub-second. On version publish, Lambda initializes the function once, takes a memory+disk snapshot, then restores from snapshot on cold starts instead of full initialization.

**Gitpod prebuilds** achieve instant workspaces by running `init` tasks (dependencies, compilation) on commit/push, snapshotting the `/workspace` directory, then deploying the snapshot when users open workspaces—only `command` tasks run at startup.

---

## PrestaShop Addons integration requires cookie modifications

**The PrestaShop Addons marketplace embeds demos via iframe**, requiring specific cookie and session configuration to enable cross-origin authentication.

### Critical cookie configuration

```apache
# .htaccess at PrestaShop root - REQUIRED for iframe display
<IfModule mod_headers.c>
    Header always edit Set-Cookie ^(.*)$ $1;SameSite=None;Secure
</IfModule>
```

Additionally, navigate to **Advanced Parameters → Administration** and enable "Allow cookies from domains other than shop domain." This allows back-office login from the Addons marketplace iframe context.

### Demo account setup pattern

1. Create limited profile in **Advanced Parameters → Team → Profiles**
2. Configure permissions: grant rights only to module configuration
3. Create demo account with limited profile, default page = "Module Manager"
4. Use standard demo credentials: `demo@demo.com` / `demodemo`
5. Reset demo instances nightly to prevent data accumulation

### Instant redirect with session handoff

```javascript
// Landing page handler - skeleton UI shows while pool assignment happens
async function handleDemoRequest(moduleId) {
  showSkeletonUI(); // Immediately show loading state
  
  // Get pre-warmed instance (should return in <100ms from pool)
  const { instanceUrl, sessionToken } = await fetch('/api/demo/assign', {
    method: 'POST',
    body: JSON.stringify({ moduleId })
  }).then(r => r.json());
  
  // Instant redirect with one-time session token
  window.location.href = `${instanceUrl}/admin${hash}/?token=${sessionToken}`;
}
```

**Skeleton screens outperform spinners** for perceived performance—users perceive 20-30% faster load times when shown expected layout structure. Use slow left-to-right wave animation, neutral grays (#E0E0E0), and cross-fade transitions when content loads.

---

## Async patterns: SSE for status, Trigger.dev for jobs, NATS for events

### Server-Sent Events over WebSockets

**SSE is better suited for provisioning status updates** because communication is unidirectional (server→client only), built-in reconnection handles reliability without extra code, and standard HTTP works with existing infrastructure without firewall issues.

```javascript
// SSE client for provisioning status
const eventSource = new EventSource('/api/demo/status?requestId=abc123');

eventSource.onmessage = (event) => {
  const status = JSON.parse(event.data);
  updateSkeletonState(status); // "Creating instance...", "Installing modules..."
  
  if (status.state === 'ready') {
    eventSource.close();
    redirectToDemo(status.instanceUrl);
  }
};
```

### Trigger.dev for background provisioning

**Trigger.dev** (github.com/triggerdotdev/trigger.dev, 13k+ stars) provides TypeScript-native checkpoint-resume workflow execution—simpler than Temporal with better DX. Each `step` is atomic and retried independently.

```typescript
import { task, step } from "@trigger.dev/sdk/v3";

export const provisionDemo = task({
  id: "provision-demo",
  run: async (payload: { moduleId: string; userId: string }) => {
    // Step 1: Assign pre-warmed instance (checkpointed)
    const instance = await step.run("assign-instance", async () => {
      return await instancePool.assign(payload.moduleId);
    });
    
    // Step 2: Configure module (checkpointed)
    await step.run("configure-module", async () => {
      await instance.installModule(payload.moduleId);
      await instance.seedDemoData();
    });
    
    // Step 3: Generate session
    const session = await step.run("create-session", async () => {
      return await instance.createDemoSession(payload.userId);
    });
    
    return { instanceUrl: instance.url, sessionToken: session.token };
  }
});
```

### NATS JetStream for event streaming

**NATS** (nats.io) is the optimal message queue for provisioning events—3MB Docker image, sub-millisecond latency, exactly-once delivery with JetStream persistence. It's a CNCF graduated project designed for cloud-native architectures.

| Queue | Throughput | Latency | Complexity | Best for |
|-------|------------|---------|------------|----------|
| **NATS JetStream** | 8-11M msg/sec | Sub-ms | Minimal | Event streaming |
| RabbitMQ Streams | Lower | Higher | Moderate | Enterprise messaging |
| Redis Streams | High | Sub-ms | Low | Already-in-stack |

---

## Recommended production architecture for Hetzner

This architecture achieves **0ms perceived startup** for users while provisioning new instances in the background in 100-300ms.

```
┌───────────────────────────────────────────────────────────────────┐
│                     USER REQUEST FLOW                              │
├───────────────────────────────────────────────────────────────────┤
│  PrestaShop    →    Landing Page    →    Pre-warmed Instance     │
│  Addons iframe      (Skeleton UI)        (Instant assignment)     │
│                     SSE status                                     │
│                     updates                                        │
└───────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌───────────────────────────────────────────────────────────────────┐
│                     BACKEND STACK                                  │
├───────────────────────────────────────────────────────────────────┤
│                                                                    │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌──────────┐ │
│  │   Caddy     │  │  Go Gin     │  │  Valkey     │  │  NATS    │ │
│  │  (Proxy)    │  │  (API)      │  │  (State)    │  │ JetStream│ │
│  │             │  │             │  │             │  │ (Events) │ │
│  │  Dynamic    │  │  Pool       │  │  Instance   │  │          │ │
│  │  routing    │  │  management │  │  tracking   │  │  Pub/sub │ │
│  │  via API    │  │  34k RPS    │  │  1.19M RPS  │  │  8M/sec  │ │
│  └─────────────┘  └─────────────┘  └─────────────┘  └──────────┘ │
│                                                                    │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │                    Trigger.dev Workers                       │  │
│  │    Checkpoint-resume provisioning • Async pool replenishment │  │
│  └─────────────────────────────────────────────────────────────┘  │
│                                                                    │
└───────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌───────────────────────────────────────────────────────────────────┐
│                     INFRASTRUCTURE                                 │
├───────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────┐  ┌─────────────────────────────┐│
│  │  Podman + CRIU              │  │  ZFS/Btrfs                   ││
│  │  Checkpoint/restore         │  │  Instant cloning             ││
│  │  50-200ms restore           │  │  Microsecond snapshots       ││
│  └─────────────────────────────┘  └─────────────────────────────┘│
└───────────────────────────────────────────────────────────────────┘
```

### Component summary with GitHub repos

| Component | Technology | Performance | Repository |
|-----------|------------|-------------|------------|
| Reverse proxy | Caddy 2.x | 33k RPS, instant dynamic config | caddyserver/caddy |
| Container runtime | Podman + CRIU | 50-200ms restore | containers/podman, checkpoint-restore/criu |
| State store | Valkey 8.1 | 1.19M RPS | valkey-io/valkey |
| API framework | Go Gin | 34k RPS | gin-gonic/gin |
| Background jobs | Trigger.dev | Checkpoint-resume | triggerdotdev/trigger.dev |
| Message queue | NATS JetStream | 8M msg/sec | nats-io/nats-server |
| Filesystem | ZFS/Btrfs | Microsecond clones | openzfs/zfs |

### Migration path from current stack

1. **Phase 1:** Replace Traefik with Caddy (1 day) — API-based dynamic routing
2. **Phase 2:** Replace Redis with Valkey (drop-in, 1 hour) — no code changes needed
3. **Phase 3:** Implement warm pool with existing Docker (1 week) — architecture change
4. **Phase 4:** Replace Docker with Podman + CRIU (2 days) — checkpoint/restore enables faster replenishment
5. **Phase 5:** Add ZFS cloning for storage efficiency (1 day) — requires Hetzner dedicated server
6. **Phase 6:** Replace FastAPI with Go Gin (1-2 weeks) — optional, only if API is bottleneck

The warm pool pattern (Phase 3) delivers the majority of perceived performance improvement. Users experience instant startup regardless of underlying cold-start times because they're assigned pre-existing instances. Phases 4-5 improve background replenishment speed, ensuring pools stay full under load.

---

## Conclusion: the path to instant demos

Achieving sub-1-second perceived startup requires thinking differently about provisioning. **The user should never wait for a container to start**—instead, containers should be waiting for users. Warm pools with async replenishment deliver effectively 0ms perceived startup, while CRIU checkpoint/restore and ZFS clones enable 100-300ms background provisioning.

The recommended stack—Caddy, Go Gin, Valkey, Podman+CRIU, ZFS, Trigger.dev, NATS—replaces each component of your current Traefik/Docker/Redis/FastAPI stack with faster, more modern alternatives while remaining deployable on Hetzner VPS. The total improvement is not merely 5-10x faster cold starts, but elimination of cold starts from the user experience entirely through architectural patterns borrowed from AWS Lambda, Fly.io, and Gitpod.