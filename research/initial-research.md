# PrestaShop 9 Demo Environment Multiplexer Architecture

A module vendor seeking to offer instant trial experiences can achieve **sub-30-second provisioning** with automatic TTL cleanup using PrestaShop Flashlight, Traefik routing, Redis-based TTL tracking, and Docker Compose orchestration on a single Hetzner CPX31 VPS (€14.49/month). This architecture comfortably supports **10-20 concurrent demo instances** with strong isolation, abuse prevention, and minimal operational overhead.

The recommended stack prioritizes simplicity over enterprise scalability: Docker Compose (not Swarm), shared MariaDB with table prefixes (not per-instance databases), and a single VPS (not a cluster). These choices dramatically reduce complexity while perfectly matching the 10s-of-instances scale.

---

## Container strategy centers on PrestaShop Flashlight

The critical discovery for fast provisioning is **PrestaShop Flashlight** (`prestashop/prestashop-flashlight`), an official image that pre-compiles the installation wizard at build time. Unlike standard images requiring 3-5 minute installation, Flashlight starts in **15-30 seconds** by restoring a database dump.

```yaml
# Base demo instance configuration
services:
  prestashop:
    image: prestashop/prestashop-flashlight:9.0.0
    environment:
      PS_DOMAIN: demo-${INSTANCE_ID}.migrationpro.com
      MYSQL_HOST: shared-db
      MYSQL_DATABASE: prestashop_demos
      DB_PREFIX: ${INSTANCE_ID}_
      INSTALL_MODULES_DIR: /ps-modules  # Auto-install your module
    volumes:
      - ./your-module.zip:/ps-modules/your-module.zip:ro
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 512M
```

**Database strategy: shared MariaDB with table prefixes.** For 10-20 instances, per-instance databases consume **3-4x more memory** (150-300MB each) with minimal benefit. A single MariaDB 10.11 container handling all demos via unique table prefixes (e.g., `demo1_`, `demo2_`) uses ~2GB RAM total versus 6-9GB for separate databases.

| Approach | Memory for 20 instances | Startup overhead | Cleanup complexity |
|----------|------------------------|------------------|-------------------|
| Shared DB + prefixes | ~2GB total | None (DB running) | SQL to drop prefixed tables |
| Per-instance DB | ~6-9GB total | 5-10s per instance | Simple container removal |

The table prefix approach requires cleanup queries but saves significant resources. PrestaShop's `DB_PREFIX` environment variable handles configuration automatically.

---

## Traefik provides zero-configuration dynamic routing

Traefik's Docker provider automatically discovers containers via labels, eliminating manual routing configuration. Combined with Let's Encrypt DNS-01 challenge, a **single wildcard certificate** covers all subdomains without rate limit concerns.

```yaml
# traefik/docker-compose.yml
services:
  traefik:
    image: traefik:v3.4
    command:
      - "--providers.docker=true"
      - "--providers.docker.exposedbydefault=false"
      - "--providers.docker.network=demo-net"
      - "--entrypoints.websecure.address=:443"
      - "--certificatesresolvers.le-dns.acme.dnschallenge=true"
      - "--certificatesresolvers.le-dns.acme.dnschallenge.provider=cloudflare"
      - "--certificatesresolvers.le-dns.acme.email=admin@migrationpro.com"
      - "--certificatesresolvers.le-dns.acme.storage=/letsencrypt/acme.json"
      - "--api.dashboard=true"
    environment:
      - CF_DNS_API_TOKEN=${CLOUDFLARE_API_TOKEN}
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./letsencrypt:/letsencrypt
    labels:
      # Wildcard cert for all demo subdomains
      - "traefik.http.routers.traefik.tls.domains[0].main=migrationpro.com"
      - "traefik.http.routers.traefik.tls.domains[0].sans=*.migrationpro.com"
```

Demo instances self-register via Docker labels:

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.demo-${ID}.rule=Host(`demo-${ID}.migrationpro.com`)"
  - "traefik.http.routers.demo-${ID}.entrypoints=websecure"
  - "traefik.http.routers.demo-${ID}.tls.certresolver=le-dns"
  - "traefik.http.services.demo-${ID}.loadbalancer.server.port=80"
```

**DNS setup requires only one A record:** `*.migrationpro.com → your-server-IP`. New subdomains automatically resolve without DNS changes.

---

## Redis TTL tracking enables event-driven cleanup

Redis provides native TTL with **keyspace notifications** that trigger cleanup events when instances expire—no polling required. This hybrid approach (event-driven + periodic backup scan) ensures reliable cleanup.

```python
import redis
import docker
from datetime import datetime, timedelta

class DemoProvisioner:
    def __init__(self):
        self.redis = redis.Redis()
        self.docker = docker.from_env()
        # Enable keyspace notifications
        self.redis.config_set('notify-keyspace-events', 'Ex')
    
    def create_instance(self, user_id: str, ttl: int = 3600) -> dict:
        instance_id = secrets.token_hex(4)  # e.g., "a1b2c3d4"
        expires_at = datetime.utcnow() + timedelta(seconds=ttl)
        
        # Create PrestaShop container with Traefik labels
        container = self.docker.containers.run(
            "prestashop/prestashop-flashlight:9.0.0",
            name=f"demo-{instance_id}",
            detach=True,
            environment={
                "PS_DOMAIN": f"demo-{instance_id}.migrationpro.com",
                "MYSQL_HOST": "shared-db",
                "DB_PREFIX": f"{instance_id}_",
            },
            labels={
                "traefik.enable": "true",
                f"traefik.http.routers.demo-{instance_id}.rule": 
                    f"Host(`demo-{instance_id}.migrationpro.com`)",
                "com.demo.instance_id": instance_id,
                "com.demo.expires_at": expires_at.isoformat(),
            },
            network="demo-net",
        )
        
        # Store in Redis with TTL (auto-expires)
        self.redis.hset(f"instance:{instance_id}", mapping={
            "container_id": container.id,
            "user_id": user_id,
            "db_prefix": f"{instance_id}_",
            "expires_at": expires_at.isoformat(),
        })
        self.redis.expire(f"instance:{instance_id}", ttl)
        
        return {
            "id": instance_id,
            "url": f"https://demo-{instance_id}.migrationpro.com",
            "admin_url": f"https://demo-{instance_id}.migrationpro.com/admin",
            "expires_at": expires_at.isoformat(),
        }
```

**Cleanup service listens for Redis expiry events:**

```python
class CleanupService:
    def cleanup_instance(self, instance_id: str):
        try:
            # Stop and remove container
            container = self.docker.containers.get(f"demo-{instance_id}")
            container.stop(timeout=30)
            container.remove(v=True)
            
            # Drop prefixed tables from shared database
            db_prefix = f"{instance_id}_"
            self.drop_prefixed_tables(db_prefix)
            
        except docker.errors.NotFound:
            pass  # Already removed
    
    def listen_for_expiry(self):
        pubsub = self.redis.pubsub()
        pubsub.psubscribe('__keyevent@0__:expired')
        
        for message in pubsub.listen():
            if message['type'] == 'pmessage':
                key = message['data'].decode()
                if key.startswith('instance:'):
                    instance_id = key.split(':')[1]
                    self.cleanup_instance(instance_id)
    
    def drop_prefixed_tables(self, prefix: str):
        """Remove all tables with the instance's prefix"""
        cursor = self.db.cursor()
        cursor.execute(f"""
            SELECT table_name FROM information_schema.tables 
            WHERE table_schema = 'prestashop_demos' 
            AND table_name LIKE '{prefix}%'
        """)
        for (table,) in cursor.fetchall():
            cursor.execute(f"DROP TABLE IF EXISTS `{table}`")
        self.db.commit()
```

**User expiry warnings** can be pushed via WebSocket or displayed in-page using the remaining TTL from Redis (`redis.ttl(f"instance:{id}")`).

---

## Security hardening prevents abuse on shared infrastructure

Multi-tenant demo environments serving untrusted users require defense-in-depth. The following configuration prevents container escape, crypto mining, spam, and resource exhaustion:

```yaml
services:
  prestashop-demo:
    image: prestashop/prestashop-flashlight:9.0.0
    read_only: true                                    # Prevent filesystem writes
    user: "1000:1000"                                  # Non-root user
    security_opt:
      - no-new-privileges:true                         # Block privilege escalation
      - seccomp:default                                # Syscall filtering
    cap_drop:
      - ALL                                            # Drop all capabilities
    deploy:
      resources:
        limits:
          cpus: '0.5'                                  # Prevents mining profitability
          memory: 512M
          pids: 100                                    # Prevents fork bombs
    tmpfs:
      - /tmp:size=64M,mode=1777,noexec
      - /var/cache:size=128M,noexec
    networks:
      - demo-isolated
```

**Network isolation blocks outbound abuse:**

```bash
# Block egress except DNS and established connections
iptables -I DOCKER-USER -i docker0 -o eth0 -j DROP
iptables -I DOCKER-USER -i docker0 -p udp --dport 53 -j ACCEPT
iptables -I DOCKER-USER -i docker0 -m state --state ESTABLISHED,RELATED -j ACCEPT

# Block mining pool ports
iptables -A DOCKER-USER -p tcp --dport 3333 -j DROP
iptables -A DOCKER-USER -p tcp --dport 4444 -j DROP

# Block SMTP (spam prevention)
iptables -A DOCKER-USER -p tcp --dport 25 -j DROP
iptables -A DOCKER-USER -p tcp --dport 587 -j DROP
```

**Rate limiting demo creation** prevents resource exhaustion:

```python
def check_rate_limit(ip_address: str) -> bool:
    hourly = redis.incr(f"ratelimit:hourly:{ip_address}")
    if hourly == 1:
        redis.expire(f"ratelimit:hourly:{ip_address}", 3600)
    
    daily = redis.incr(f"ratelimit:daily:{ip_address}")
    if daily == 1:
        redis.expire(f"ratelimit:daily:{ip_address}", 86400)
    
    return hourly <= 2 and daily <= 5  # 2/hour, 5/day per IP
```

Add **Cloudflare Turnstile** (free) for invisible CAPTCHA protection on the demo request form.

---

## Hetzner CPX31 provides optimal price-performance

For **10-20 concurrent PrestaShop demos**, Hetzner's **CPX31** (€14.49/month) offers the best value: 4 AMD EPYC vCPUs, 8GB RAM, 160GB NVMe, and 20TB traffic included.

| Resource | Allocation | Supporting instances |
|----------|-----------|---------------------|
| RAM | 8GB total | ~12-15 demos at 512MB each + overhead |
| CPU | 4 vCPUs | ~8-16 demos at 0.5 CPU each |
| Storage | 160GB NVMe | ~100+ demos (images cached, ephemeral data) |

**Critical Hetzner configurations:**

1. **Cloud Firewall (free):** Restrict SSH to your IP, allow only 80/443 inbound
2. **Local NVMe over Volumes:** Use included local storage—faster and free
3. **Docker CE App:** Select during server creation for pre-configured Docker

```json
// /etc/docker/daemon.json for multi-tenant hardening
{
  "userns-remap": "default",
  "log-driver": "json-file",
  "log-opts": {"max-size": "10m", "max-file": "3"},
  "live-restore": true,
  "userland-proxy": false
}
```

**Scaling path:** If demand exceeds CPX31 capacity, horizontal scaling is straightforward—spin up additional Hetzner VPS instances behind a load balancer, using Hetzner's Cloud API for automation.

---

## Complete architecture diagram and recommended tech stack

```
┌─────────────────────────────────────────────────────────────────┐
│                    Hetzner CPX31 VPS (€14.49/mo)                │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │                     Traefik v3.4                           │ │
│  │   Wildcard SSL • Auto-discovery • Dashboard                │ │
│  │   *.migrationpro.com → container routing                   │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              │                                   │
│  ┌──────────────┐  ┌────────┴────────┐  ┌──────────────────┐   │
│  │   Redis 7    │  │   Provisioning  │  │  Cleanup Service │   │
│  │  TTL tracking│◄─┤      API        ├─►│ Event listener   │   │
│  │  Rate limits │  │   (FastAPI)     │  │ Cron backup      │   │
│  └──────────────┘  └─────────────────┘  └──────────────────┘   │
│                              │                                   │
│  ┌───────────────────────────┴───────────────────────────────┐ │
│  │              MariaDB 10.11 (shared, 2GB RAM)              │ │
│  │          Table prefixes: demo1_*, demo2_*, ...            │ │
│  └───────────────────────────────────────────────────────────┘ │
│                              │                                   │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                   Demo Instances                          │  │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐               │  │
│  │  │ Flashlight│  │ Flashlight│  │    ...   │  (10-20)     │  │
│  │  │  demo-a1  │  │  demo-b2  │  │          │              │  │
│  │  │  512MB    │  │  512MB    │  │          │              │  │
│  │  └──────────┘  └──────────┘  └──────────┘               │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

**Final tech stack summary:**

| Component | Choice | Rationale |
|-----------|--------|-----------|
| PrestaShop image | `prestashop/prestashop-flashlight:9.0.0` | 15-30s startup vs 3-5min |
| Database | MariaDB 10.11, shared + prefixes | 3-4x memory savings |
| Orchestration | Docker Compose | Simpler than Swarm for single-node |
| Reverse proxy | Traefik v3.4 | Native Docker discovery + dashboard |
| SSL | Let's Encrypt wildcard via DNS-01 | No rate limit issues |
| TTL tracking | Redis with keyspace notifications | Event-driven cleanup |
| API framework | FastAPI (Python) or Gin (Go) | Docker SDK available |
| Infrastructure | Hetzner CPX31 (8GB/4vCPU) | €14.49/mo, 20TB traffic |

---

## Implementation roadmap

**Phase 1 (Week 1):** Core infrastructure
- Deploy Hetzner CPX31 with Docker CE
- Configure Traefik with wildcard certificate
- Set up shared MariaDB container
- Test manual demo provisioning with Flashlight

**Phase 2 (Week 2):** Automation layer  
- Build provisioning API with rate limiting
- Implement Redis TTL tracking and cleanup service
- Add Cloudflare Turnstile to demo request form
- Deploy monitoring (Traefik dashboard + basic alerts)

**Phase 3 (Week 3):** Hardening and polish
- Apply security configuration (read-only, resource limits, network isolation)
- Add user-facing TTL countdown and expiry warnings
- Test abuse scenarios (high CPU, spam attempts)
- Document runbooks for operations

This architecture delivers instant module trials with minimal operational burden while maintaining strong security boundaries—precisely matching the module vendor use case of converting PrestaShop Addons marketplace visitors into paying customers.