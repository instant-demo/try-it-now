# PrestaShop Flashlight Deep Dive: Complete Configuration Guide

**Purpose:** Comprehensive research for instant-provisioning demo environment using `prestashop/prestashop-flashlight:9.0.0` with CRIU checkpoint/restore, warm pools, and shared MariaDB.

---

## 1. PrestaShop Flashlight Overview

### What It Is
PrestaShop Flashlight is a **Docker-based testing utility** that achieves 15-30 second startup (vs 3-5 minutes for standard images) by:
- Pre-compiling the installation wizard at build time
- Restoring from a pre-built database dump
- Skipping the traditional installation flow

### Critical Limitation
**NOT for production use** - designed for development, testing, and demo environments only.

### Image Sources
- **Docker Hub:** `prestashop/prestashop-flashlight`
- **GitHub:** `https://github.com/PrestaShop/prestashop-flashlight`
- **Tags:** `latest`, `nightly`, version-specific (e.g., `9.0.0`, `8.1.7-php8.1`)

### Key Architecture
- Based on Alpine Linux + Nginx (not Apache)
- **Does NOT include MySQL/MariaDB** - must provide external database
- Contains: PhpMyAdmin, Composer, php-cs-fixer, phpstan, phpunit

---

## 2. Environment Variables (Complete List)

### Mandatory Configuration
| Variable | Description | Example |
|----------|-------------|---------|
| `PS_DOMAIN` | Public domain/port for the shop | `demo-abc123.example.com` |
| `MYSQL_HOST` | Database server hostname | `mariadb` |
| `MYSQL_DATABASE` | Database name | `prestashop_demos` |
| `MYSQL_USER` | Database username | `prestashop` |
| `MYSQL_PASSWORD` | Database password | `secret` |

### Optional Configuration
| Variable | Description | Default |
|----------|-------------|---------|
| `DB_PREFIX` | Table prefix for multi-tenancy | `ps_` |
| `INSTALL_MODULES_DIR` | Directory for auto-installing modules | `/ps-modules` |
| `PS_FOLDER_ADMIN` | Admin panel folder name | `admin` |
| `PS_FOLDER_INSTALL` | Install folder name | `install` |
| `PS_ENABLE_SSL` | Enable HTTPS | `0` |
| `PS_DEV_MODE` | Development mode | `1` |
| `NGROK_TUNNEL_AUTO_DETECT` | Auto-detect ngrok tunnel | `false` |

### Demo Credentials (Flashlight Defaults)
- **Admin URL:** `https://{PS_DOMAIN}/admin`
- **Admin User:** `admin@prestashop.com`
- **Admin Password:** `Correct Horse Battery Staple` (or `prestashop` in some versions)

---

## 3. Instant Domain Configuration

### Method 1: Environment Variable at Startup
```yaml
environment:
  PS_DOMAIN: demo-${INSTANCE_ID}.example.com
```
This sets the domain during initial container startup.

### Method 2: Runtime Configuration via CLI
```bash
# Inside the container
php bin/console prestashop:config set PS_SHOP_DOMAIN --value "new-domain.example.com"
php bin/console prestashop:config set PS_SHOP_DOMAIN_SSL --value "new-domain.example.com"
```

### Method 3: Direct Database Update
```sql
-- Update shop URL table
UPDATE ps_shop_url
SET domain = 'new-domain.example.com',
    domain_ssl = 'new-domain.example.com'
WHERE id_shop = 1;

-- Update configuration
UPDATE ps_configuration
SET value = 'new-domain.example.com'
WHERE name = 'PS_SHOP_DOMAIN';

UPDATE ps_configuration
SET value = 'new-domain.example.com'
WHERE name = 'PS_SHOP_DOMAIN_SSL';
```

### Method 4: For CRIU Checkpoint/Restore
After restoring a container, the domain must be updated because the checkpoint was created with the template domain:

```bash
# Post-restore script
mysql -h $MYSQL_HOST -u $MYSQL_USER -p$MYSQL_PASSWORD $MYSQL_DATABASE <<EOF
UPDATE ${DB_PREFIX}shop_url
SET domain = '$NEW_DOMAIN', domain_ssl = '$NEW_DOMAIN'
WHERE id_shop = 1;

UPDATE ${DB_PREFIX}configuration
SET value = '$NEW_DOMAIN'
WHERE name IN ('PS_SHOP_DOMAIN', 'PS_SHOP_DOMAIN_SSL');
EOF
```

---

## 4. Module Installation and Linking

### Auto-Install via INSTALL_MODULES_DIR
Mount a directory containing module ZIP files:

```yaml
services:
  prestashop:
    image: prestashop/prestashop-flashlight:9.0.0
    volumes:
      - ./modules:/ps-modules:ro
    environment:
      INSTALL_MODULES_DIR: /ps-modules
```

The container will automatically extract and install all `.zip` files in this directory during startup.

### Symlink for Development
For development, use symlinks to avoid copying:

```yaml
volumes:
  - ./your-module:/var/www/html/modules/your-module:ro
```

### Programmatic Module Installation
```bash
# Inside container
php bin/console prestashop:module install your_module_name
php bin/console prestashop:module enable your_module_name
```

### Module Installation via Composer
```json
{
  "require": {
    "prestashop/module-lib-mbo-installer": "^1.0"
  }
}
```

---

## 5. CRIU Checkpoint/Restore: Critical Considerations

### When to Create Checkpoint

**Optimal checkpoint timing:**
1. **After container startup completes** - all services running
2. **After database initialization** - schema created, default data loaded
3. **After module installation** - all modules enabled and configured
4. **Before any user-specific data** - clean slate for demos
5. **With warm caches** - OpCache populated, PrestaShop caches warm

**Checkpoint checklist:**
- [ ] PrestaShop fully booted (HTTP 200 on homepage)
- [ ] Admin panel accessible
- [ ] All modules installed and enabled
- [ ] Cache warmed (visit key pages)
- [ ] No active user sessions
- [ ] Template domain configured (will be changed post-restore)

### TCP Connection Handling

**Problem:** CRIU captures TCP socket state. After restore:
- Old TCP connections are invalid
- Database connections will fail
- Any HTTP keep-alive connections are stale

**Solution:** PrestaShop handles this gracefully:
1. PHP reinitializes database connections on each request
2. PDO/MySQLi connections auto-reconnect
3. No persistent connections by default

**Podman CRIU flags:**
```bash
# Checkpoint with TCP handling
podman container checkpoint \
  --export=/checkpoints/prestashop.tar.gz \
  --tcp-established \
  prestashop-template

# Restore with new network identity
podman container restore \
  --import=/checkpoints/prestashop.tar.gz \
  --ignore-static-ip \
  --ignore-static-mac \
  --name=demo-${INSTANCE_ID}
```

### Post-Restore Configuration Script

```bash
#!/bin/bash
# post-restore.sh - Run inside restored container

INSTANCE_ID=$1
NEW_DOMAIN=$2
DB_PREFIX=$3

# Wait for MySQL connectivity
until mysql -h $MYSQL_HOST -u $MYSQL_USER -p$MYSQL_PASSWORD -e "SELECT 1" &>/dev/null; do
  sleep 1
done

# Update domain configuration
mysql -h $MYSQL_HOST -u $MYSQL_USER -p$MYSQL_PASSWORD $MYSQL_DATABASE <<EOF
UPDATE ${DB_PREFIX}shop_url
SET domain = '${NEW_DOMAIN}', domain_ssl = '${NEW_DOMAIN}'
WHERE id_shop = 1;

UPDATE ${DB_PREFIX}configuration
SET value = '${NEW_DOMAIN}'
WHERE name IN ('PS_SHOP_DOMAIN', 'PS_SHOP_DOMAIN_SSL');

-- Clear cached configuration
TRUNCATE TABLE ${DB_PREFIX}smarty_cache;
TRUNCATE TABLE ${DB_PREFIX}smarty_lazy_cache;
EOF

# Clear file caches
rm -rf /var/www/html/var/cache/*
rm -rf /var/www/html/app/cache/*
```

---

## 6. Shared MariaDB: Multi-Tenant Architecture

### Table Prefix Strategy

Each demo instance uses a unique table prefix for isolation:

```yaml
# Instance 1
environment:
  DB_PREFIX: demo_abc123_

# Instance 2
environment:
  DB_PREFIX: demo_xyz789_
```

**Memory efficiency:** Single MariaDB instance (~2GB RAM) vs per-instance databases (~150-300MB each).

### Table Prefix Initialization

PrestaShop creates ~100+ tables with the specified prefix:
- `{prefix}product`
- `{prefix}category`
- `{prefix}customer`
- `{prefix}orders`
- etc.

### Cleanup: Dropping Prefixed Tables

**Generate DROP statements:**
```sql
SELECT CONCAT('DROP TABLE IF EXISTS `', table_name, '`;')
FROM information_schema.tables
WHERE table_schema = 'prestashop_demos'
AND table_name LIKE 'demo_abc123_%';
```

**Execute cleanup in Go:**
```go
func (r *ValkeyRepository) DropPrefixedTables(ctx context.Context, dbPrefix string) error {
    // Get list of tables with prefix
    query := `
        SELECT table_name FROM information_schema.tables
        WHERE table_schema = ? AND table_name LIKE ?
    `
    rows, err := r.db.QueryContext(ctx, query, r.config.Database, dbPrefix+"%")
    if err != nil {
        return fmt.Errorf("failed to list tables: %w", err)
    }
    defer rows.Close()

    // Generate and execute DROP statements
    var tables []string
    for rows.Next() {
        var tableName string
        if err := rows.Scan(&tableName); err != nil {
            continue
        }
        tables = append(tables, tableName)
    }

    if len(tables) == 0 {
        return nil
    }

    // Drop in single transaction
    dropSQL := "DROP TABLE IF EXISTS " + strings.Join(tables, ", ")
    _, err = r.db.ExecContext(ctx, dropSQL)
    return err
}
```

### MariaDB Configuration for Multi-Tenant

```yaml
# docker-compose.yml
services:
  mariadb:
    image: mariadb:10.11
    environment:
      MYSQL_ROOT_PASSWORD: ${DB_ROOT_PASSWORD}
      MYSQL_DATABASE: prestashop_demos
      MYSQL_USER: prestashop
      MYSQL_PASSWORD: ${DB_PASSWORD}
    command:
      - --character-set-server=utf8mb4
      - --collation-server=utf8mb4_unicode_ci
      - --max-connections=200
      - --innodb-buffer-pool-size=1G
    volumes:
      - mariadb-data:/var/lib/mysql
```

---

## 7. Implementation Checklist for Try It Now

### Phase 1: Checkpoint Creation
- [ ] Start prestashop-flashlight:9.0.0 with template configuration
- [ ] Configure with template domain (`template.internal`)
- [ ] Install all demo modules
- [ ] Warm caches (visit homepage, admin, key pages)
- [ ] Create CRIU checkpoint with `--tcp-established`
- [ ] Store checkpoint at configured path

### Phase 2: Instance Provisioning (Warm Pool)
- [ ] Restore from checkpoint (50-200ms)
- [ ] Assign unique instance ID
- [ ] Generate unique hostname (`demo-{id}.example.com`)
- [ ] Generate unique DB prefix (`{id}_`)
- [ ] Update domain in database
- [ ] Add Caddy route
- [ ] Move to ready pool

### Phase 3: Instance Assignment
- [ ] Pop instance from ready pool (O(1))
- [ ] Set TTL
- [ ] Return URL to user
- [ ] Trigger async replenishment

### Phase 4: Instance Cleanup (TTL Expiry)
- [ ] Stop container
- [ ] Remove Caddy route
- [ ] Drop prefixed tables from MariaDB
- [ ] Release port back to pool
- [ ] Remove instance from store

---

## 8. Edge Cases and Solutions

### Edge Case 1: Database Connection After CRIU Restore
**Problem:** Restored container has stale DB connection state.
**Solution:** PrestaShop uses short-lived connections per request. First request after restore establishes new connection automatically.

### Edge Case 2: Session/Token Invalidation
**Problem:** Cached sessions from checkpoint are invalid.
**Solution:**
- Clear session tables in post-restore script
- Use stateless admin tokens for demo access
- Set short session TTL in PrestaShop config

### Edge Case 3: Cache Coherency
**Problem:** File caches may reference old domain.
**Solution:**
```bash
# Post-restore cache clear
rm -rf /var/www/html/var/cache/prod/*
rm -rf /var/www/html/var/cache/dev/*
php bin/console cache:clear --env=prod
```

### Edge Case 4: Module License Validation
**Problem:** Some modules validate domain against license.
**Solution:** Use modules that don't require domain-specific licensing for demos, or configure license to accept wildcard subdomain.

### Edge Case 5: SSL Certificate Mismatch
**Problem:** Checkpoint has cached SSL state for template domain.
**Solution:** Use wildcard certificate at Caddy level; PrestaShop doesn't handle SSL directly in this architecture.

### Edge Case 6: Concurrent Checkpoint Access
**Problem:** Multiple restore operations reading same checkpoint file.
**Solution:** Checkpoint file is read-only; concurrent reads are safe. Store on fast local NVMe.

---

## 9. Performance Benchmarks (Expected)

| Operation | Time | Notes |
|-----------|------|-------|
| Cold start (no checkpoint) | 15-30s | Standard Flashlight startup |
| CRIU restore | 50-200ms | Depends on memory footprint |
| Domain update (SQL) | <10ms | Single UPDATE query |
| Caddy route add | <5ms | REST API call |
| Pool acquire (LPOP) | <1ms | Valkey O(1) operation |
| **Total user-perceived latency** | **<500ms** | From click to usable demo |

---

## 10. Docker Compose Reference Configuration

```yaml
version: '3.8'

services:
  # Shared MariaDB for all demo instances
  mariadb:
    image: mariadb:10.11
    container_name: demo-mariadb
    environment:
      MYSQL_ROOT_PASSWORD: ${DB_ROOT_PASSWORD}
      MYSQL_DATABASE: prestashop_demos
      MYSQL_USER: prestashop
      MYSQL_PASSWORD: ${DB_PASSWORD}
    volumes:
      - mariadb-data:/var/lib/mysql
    networks:
      - demo-net

  # Template instance for checkpoint creation
  prestashop-template:
    image: prestashop/prestashop-flashlight:9.0.0
    container_name: prestashop-template
    environment:
      PS_DOMAIN: template.internal
      MYSQL_HOST: mariadb
      MYSQL_DATABASE: prestashop_demos
      MYSQL_USER: prestashop
      MYSQL_PASSWORD: ${DB_PASSWORD}
      DB_PREFIX: template_
      PS_DEV_MODE: 0
      INSTALL_MODULES_DIR: /ps-modules
    volumes:
      - ./modules:/ps-modules:ro
    depends_on:
      - mariadb
    networks:
      - demo-net

volumes:
  mariadb-data:

networks:
  demo-net:
    driver: bridge
```

---

## 11. Key Takeaways

1. **PS_DOMAIN must be updated after CRIU restore** - both in database and potentially in cached files
2. **DB_PREFIX provides multi-tenant isolation** - each instance gets unique prefix
3. **Module installation happens at checkpoint time** - not at restore time
4. **TCP connections auto-reconnect** - PrestaShop handles this gracefully
5. **Cache clearing is essential post-restore** - prevents domain/config mismatches
6. **Checkpoint should be created after warm state** - OpCache populated, Smarty compiled
7. **Wildcard SSL at proxy level** - Caddy handles SSL, not PrestaShop containers

---

## References

- [PrestaShop Flashlight GitHub](https://github.com/PrestaShop/prestashop-flashlight)
- [PrestaShop Docker Hub](https://hub.docker.com/r/prestashop/prestashop-flashlight)
- [PrestaShop Dev Docs - Docker](https://devdocs.prestashop-project.org/8/basics/installation/environments/docker/)
- [CRIU TCP Connection Handling](https://criu.org/TCP_connection)
- [Podman Checkpoint/Restore](https://docs.podman.io/en/latest/markdown/podman-container-checkpoint.1.html)
