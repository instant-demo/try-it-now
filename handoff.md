# Production Deployment Handoff

## Current Blocking Issue (2026-01-17)

### Database Isolation Bug - ALL containers share same PS_DOMAIN

**Symptom:** All demo containers redirect to the SAME domain (the last one started), regardless of which subdomain you access.

**Root Cause:** PrestaShop stores its domain in the database (`ps_configuration` or `ps_shop_url` tables). When multiple containers share the same MariaDB database WITHOUT table prefix isolation, the last container to start overwrites the domain config for ALL instances.

**Evidence:**
```bash
# Container A (demo-371eede4-...) has PS_DOMAIN env var set correctly
docker inspect demo-demo-371eede4-... | grep PS_DOMAIN
# Returns: PS_DOMAIN=demo-371eede4-....demo.migration-pro.com

# But when you curl it, it redirects to container B's domain
curl -sI http://localhost:32107/ | grep Location
# Returns: Location: http://demo-ff02474c-....demo.migration-pro.com/

# Container B (demo-ff02474c-...) was the last one started
# ALL containers redirect to its domain
```

**The Fix Required:**
Each container needs its own isolated database tables. Options:
1. **Table prefix isolation** - Use unique DB_PREFIX per container (prestashop-flashlight may support this)
2. **Separate databases** - Create a new database per container (not scalable)
3. **Single-tenant mode** - Only run ONE demo at a time (defeats the purpose)

**Code location:** Check `internal/pool/impl.go` - there IS a `dbPrefix` being generated but it's not being used correctly for PrestaShop table isolation.

**Files to investigate:**
- `internal/container/docker.go:buildEnvVars()` - How env vars are passed
- Check if prestashop-flashlight supports `DB_PREFIX` or similar env var
- The dump restoration in prestashop-flashlight's `/run.sh`

---

## Session Summary (2026-01-17)

MariaDB 11.4 SSL issue RESOLVED. Production deployment WORKING except for the database isolation bug above.

## Server Details

- **Server**: Hetzner CPX42 (8 vCPU, 16GB RAM, 320GB disk)
- **IP**: 46.62.223.166
- **Domain**: demo.migration-pro.com
- **User**: edosh (sudo user created)
- **Location**: Helsinki, Finland (hel1-dc2)

## What's Working

1. **Server Setup** - Docker, Go 1.22 installed
2. **Infrastructure** - All containers running:
   - Caddy (ports 80, 443, 2019)
   - Valkey (port 6379) - healthy
   - NATS (ports 4222, 8222)
   - MariaDB 11.4 (port 3306) - healthy, auto-generated TLS certs
3. **DNS** - Configured in Cloudflare:
   - `demo.migration-pro.com` → 46.62.223.166 (DNS only)
   - `*.demo.migration-pro.com` → 46.62.223.166 (DNS only)
4. **TLS Certificates** - Caddy obtained cert for `demo.migration-pro.com` via ZeroSSL
5. **Go Application** - Builds and runs, connects to Valkey and NATS
6. **PrestaShop Container** - Starts and connects to DB via PDO (PHP)

## Resolved: MySQL SSL Issue

**Problem:** The prestashop-flashlight container's MariaDB 11.x client requires SSL by default, but MariaDB 10.11 server didn't support it.

**Solution:** Upgraded MariaDB from 10.11 to 11.4. MariaDB 11.4+ auto-generates TLS certificates on startup, so both client and server now have matching SSL expectations.

**Changes made:**
1. Updated `deployments/docker-compose.yml` → `mariadb:11.4`
2. Updated `deployments/production/docker-compose.yml` → `mariadb:11.4`
3. Removed SSL config mount hack from `internal/container/docker.go`

## Files Changed This Session

All committed and pushed to `main`:

1. `deployments/production/Caddyfile` - Production Caddy config
2. `deployments/production/docker-compose.yml` - Production docker-compose
3. `deployments/production/.env.production` - Production env template
4. `deployments/production/try-it-now.service` - Systemd service file
5. `internal/container/docker.go` - Updated env vars for prestashop-flashlight (MYSQL_* prefix), added no-redirect health check
6. `internal/container/podman.go` - Same env var updates

## Server State

```bash
# Location
/home/edosh/projects/try-it-now

# Infrastructure running from
/home/edosh/projects/try-it-now/deployments

# Docker network
deployments_demo-net  # All containers connected

# Cleanup: Remove orphaned config file
sudo rm /etc/mysql-client-no-ssl.cnf
```

## Commands to Resume

```bash
# SSH to server
ssh edosh@46.62.223.166

# Check infrastructure
cd ~/projects/try-it-now/deployments
docker compose ps

# View logs
docker logs deployments-mariadb-1
docker logs deployments-caddy-1

# Run app
cd ~/projects/try-it-now
./build/try-it-now

# Clean demo containers
docker rm -f $(docker ps -aq --filter name=demo-demo) 2>/dev/null
```

## Environment Variables Needed

The prestashop-flashlight image requires these env vars (set in buildEnvVars):
- `PS_DOMAIN` - Full domain for the demo
- `MYSQL_HOST` - Database hostname (use `mariadb` for docker network)
- `MYSQL_PORT` - Database port
- `MYSQL_DATABASE` - Database name
- `MYSQL_USER` - Database user
- `MYSQL_PASSWORD` - Database password
- `ADMIN_MAIL_OVERRIDE` - Admin email
- `ADMIN_PASSWORD_OVERRIDE` - Admin password

## Health Check Fix Applied

The health check now uses `CheckRedirect` to not follow HTTP→HTTPS redirects:
```go
CheckRedirect: func(req *http.Request, via []*http.Request) error {
    return http.ErrUseLastResponse
}
```

This is correct - a 302 response means the container is healthy.

## Priority for Next Session

1. ~~**Fix MySQL SSL issue**~~ - ✅ Resolved by upgrading to MariaDB 11.4
2. **Deploy and verify** - Pull changes, recreate MariaDB volume, test container starts
3. **Test full flow** - Acquire demo, verify it's accessible (should see "MySQL dump restored!" in logs)
4. **Set up systemd service** - For production daemon
5. **Configure firewall** - UFW rules for ports 80, 443, 22 only

## Useful Debug Commands

```bash
# Test MariaDB connection from inside network
docker run --rm --network deployments_demo-net mariadb:11.4 \
  mysql -h mariadb -u prestashop -p'PASSWORD' -e "SELECT 1"

# Check what user prestashop-flashlight runs as
docker run --rm prestashop/prestashop-flashlight:9.0.0 whoami

# View demo container logs
docker logs <container-id>
```
