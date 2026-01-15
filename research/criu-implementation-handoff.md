# CRIU Implementation Handoff Document

## PrestaShop CRIU Checkpoint Feature for Demo Multiplexer

**Task ID:** `demo-multi-plexer-989`
**Status:** Research Complete - Ready for Implementation
**Prepared for:** New Claude Code Session
**Date:** 2026-01-16

---

## Executive Summary

This document provides complete implementation instructions for adding PrestaShop CRIU checkpoint/restore support to the Demo Multiplexer. The system currently has a working CRIU infrastructure in `internal/container/podman.go`, but lacks the critical post-restore database operations needed to make PrestaShop functional after restoration.

**Key Implementation Gaps:**
1. No database package exists to update PrestaShop domain configuration after CRIU restore
2. No cleanup mechanism to drop prefixed tables when instances expire
3. No checkpoint creation tooling

**Expected Outcome:** Sub-500ms instance provisioning with proper PrestaShop domain configuration.

---

## Architecture Context

```
┌─────────────────────────────────────────────────────────────────┐
│                     Current Flow                                 │
├─────────────────────────────────────────────────────────────────┤
│  CRIU Restore (50-200ms)                                        │
│        │                                                        │
│        ▼                                                        │
│  [GAP] Domain still points to template domain                   │
│        │                                                        │
│        ▼                                                        │
│  Instance added to pool (broken)                                │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     Target Flow                                  │
├─────────────────────────────────────────────────────────────────┤
│  CRIU Restore (50-200ms)                                        │
│        │                                                        │
│        ▼                                                        │
│  UpdateDomain() + ClearCaches() (<10ms)                         │
│        │                                                        │
│        ▼                                                        │
│  Instance added to pool (working!)                              │
│        │                                                        │
│        ▼ (on TTL expiry)                                        │
│  DropPrefixedTables() (cleanup)                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## Quick Start for New Session

```bash
# 1. Review the task
bd show demo-multi-plexer-989

# 2. Start working on the task
bd update demo-multi-plexer-989 --status=in_progress

# 3. Read the research
cat research/prestashop-flashlight-deep-dive.md

# 4. Follow implementation phases below
```

---

## Research Documents

All research is located in `/Users/boss/dev/demo-multi-plexer/research/`:

| Document | Purpose |
|----------|---------|
| `prestashop-flashlight-deep-dive.md` | Complete PrestaShop Flashlight configuration guide |
| `initial-research.md` | Initial architecture research |
| `second-research.md` | Technology stack research (Caddy, CRIU, Valkey) |

---

## Implementation Phases

### Phase 1: Database Package (Day 1)

**Create:** `internal/database/prestashop.go`

```go
package database

import (
    "context"
    "database/sql"
    "fmt"
    "strings"

    "github.com/boss/demo-multiplexer/internal/config"
    _ "github.com/go-sql-driver/mysql"
)

// PrestaShopDB provides database operations for PrestaShop instances.
type PrestaShopDB struct {
    db     *sql.DB
    dbName string
}

// NewPrestaShopDB creates a new PrestaShop database client.
func NewPrestaShopDB(cfg *config.PrestaShopConfig) (*PrestaShopDB, error) {
    dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
        cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName)

    db, err := sql.Open("mysql", dsn)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }

    if err := db.Ping(); err != nil {
        return nil, fmt.Errorf("failed to ping database: %w", err)
    }

    return &PrestaShopDB{db: db, dbName: cfg.DBName}, nil
}

// Close closes the database connection.
func (p *PrestaShopDB) Close() error {
    return p.db.Close()
}

// UpdateDomain updates the shop domain in PrestaShop configuration tables.
// This MUST be called after CRIU restore to point the shop to the correct domain.
func (p *PrestaShopDB) UpdateDomain(ctx context.Context, dbPrefix, newDomain string) error {
    // Update shop_url table
    shopURLQuery := fmt.Sprintf(`
        UPDATE %sshop_url
        SET domain = ?, domain_ssl = ?
        WHERE id_shop = 1
    `, dbPrefix)

    if _, err := p.db.ExecContext(ctx, shopURLQuery, newDomain, newDomain); err != nil {
        return fmt.Errorf("failed to update shop_url: %w", err)
    }

    // Update configuration table
    configQuery := fmt.Sprintf(`
        UPDATE %sconfiguration
        SET value = ?
        WHERE name IN ('PS_SHOP_DOMAIN', 'PS_SHOP_DOMAIN_SSL')
    `, dbPrefix)

    if _, err := p.db.ExecContext(ctx, configQuery, newDomain); err != nil {
        return fmt.Errorf("failed to update configuration: %w", err)
    }

    return nil
}

// ClearCaches truncates Smarty cache tables.
// Call this after UpdateDomain to prevent stale cached domain references.
func (p *PrestaShopDB) ClearCaches(ctx context.Context, dbPrefix string) error {
    tables := []string{
        dbPrefix + "smarty_cache",
        dbPrefix + "smarty_lazy_cache",
    }

    for _, table := range tables {
        query := fmt.Sprintf("TRUNCATE TABLE %s", table)
        if _, err := p.db.ExecContext(ctx, query); err != nil {
            // Tables might not exist in all PrestaShop versions - continue
            continue
        }
    }

    return nil
}

// DropPrefixedTables drops all tables with the given prefix.
// Use this for instance cleanup when TTL expires.
func (p *PrestaShopDB) DropPrefixedTables(ctx context.Context, dbPrefix string) error {
    // Query to get all tables with prefix
    query := `
        SELECT table_name FROM information_schema.tables
        WHERE table_schema = ? AND table_name LIKE ?
    `

    rows, err := p.db.QueryContext(ctx, query, p.dbName, dbPrefix+"%")
    if err != nil {
        return fmt.Errorf("failed to list prefixed tables: %w", err)
    }
    defer rows.Close()

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

    // Build and execute DROP statement
    quotedTables := make([]string, len(tables))
    for i, t := range tables {
        quotedTables[i] = "`" + t + "`"
    }

    dropQuery := "DROP TABLE IF EXISTS " + strings.Join(quotedTables, ", ")
    if _, err := p.db.ExecContext(ctx, dropQuery); err != nil {
        return fmt.Errorf("failed to drop prefixed tables: %w", err)
    }

    return nil
}

// Ping checks database connectivity.
func (p *PrestaShopDB) Ping(ctx context.Context) error {
    return p.db.PingContext(ctx)
}
```

**Add dependency:**
```bash
go get github.com/go-sql-driver/mysql
```

---

### Phase 2: Pool Manager Integration (Day 1-2)

**Modify:** `internal/pool/impl.go`

**Changes Required:**

1. **Add field to `PoolManager` struct** (around line 28):
```go
type PoolManager struct {
    // ... existing fields ...
    psDB *database.PrestaShopDB  // ADD THIS
}
```

2. **Update `NewPoolManager` signature** (around line 49):
```go
func NewPoolManager(
    cfg ManagerConfig,
    repo store.Repository,
    runtime container.Runtime,
    proxyMgr proxy.RouteManager,
    psDB *database.PrestaShopDB,  // ADD THIS PARAMETER
    containerImage string,
    // ... rest of params ...
) *PoolManager {
    return &PoolManager{
        // ... existing fields ...
        psDB: psDB,  // ADD THIS
    }
}
```

3. **Add post-restore database operations in `provisionInstance()`** (after line ~302, after CRIU restore succeeds):
```go
if m.psDB != nil && method == "criu_restore" {
    fullDomain := hostname + "." + m.baseDomain

    if err := m.psDB.UpdateDomain(ctx, dbPrefix, fullDomain); err != nil {
        m.logger.Warn("Failed to update domain after CRIU restore", "error", err)
    }

    if err := m.psDB.ClearCaches(ctx, dbPrefix); err != nil {
        m.logger.Warn("Failed to clear caches after CRIU restore", "error", err)
    }

    m.logger.Info("Updated PrestaShop domain after CRIU restore",
        "hostname", hostname, "domain", fullDomain, "dbPrefix", dbPrefix)
}
```

4. **Add database cleanup in `Release()` method** (before container stop, around line 134):
```go
// Drop prefixed tables from shared database
if m.psDB != nil && instance.DBPrefix != "" {
    if err := m.psDB.DropPrefixedTables(ctx, instance.DBPrefix); err != nil {
        m.logger.Warn("Failed to drop prefixed tables",
            "instanceID", instanceID, "dbPrefix", instance.DBPrefix, "error", err)
    } else {
        m.logger.Info("Dropped prefixed tables",
            "instanceID", instanceID, "dbPrefix", instance.DBPrefix)
    }
}
```

---

### Phase 3: Server Wiring (Day 2)

**Modify:** `cmd/server/main.go`

1. **Add import**:
```go
import (
    "github.com/boss/demo-multiplexer/internal/database"
)
```

2. **Initialize PrestaShopDB** (after config load, around line 28):
```go
cfg := config.Load()

psDB, err := database.NewPrestaShopDB(&cfg.PrestaShop)
if err != nil {
    logger.Fatal("Failed to connect to PrestaShop database", "error", err)
}
defer psDB.Close()
logger.Info("Connected to PrestaShop database")
```

3. **Update `NewPoolManager` call** (around line 122):
```go
poolMgr := pool.NewPoolManager(
    poolCfg,
    repo,
    runtime,
    proxyMgr,
    psDB,  // ADD THIS PARAMETER
    cfg.Container.Image,
    // ... rest of params ...
)
```

---

### Phase 4: Checkpoint Creator Tool (Day 3)

**Create:** `cmd/checkpoint/main.go`

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/exec"
    "time"
)

func main() {
    var (
        image          = flag.String("image", "prestashop/prestashop-flashlight:9.0.0", "PrestaShop image")
        containerName  = flag.String("name", "prestashop-checkpoint-template", "Container name")
        checkpointPath = flag.String("output", "/var/lib/checkpoints/prestashop.tar.gz", "Output path")
        mysqlHost      = flag.String("mysql-host", "host.containers.internal", "MySQL host")
        mysqlUser      = flag.String("mysql-user", "demo", "MySQL user")
        mysqlPass      = flag.String("mysql-pass", "devpass", "MySQL password")
        mysqlDB        = flag.String("mysql-db", "prestashop_demos", "MySQL database")
        dbPrefix       = flag.String("db-prefix", "template_", "Database table prefix")
        port           = flag.Int("port", 8888, "Host port to expose")
    )
    flag.Parse()

    ctx := context.Background()

    log.Println("Step 1: Starting PrestaShop container...")
    startCmd := exec.CommandContext(ctx, "podman", "run", "-d",
        "--name", *containerName,
        "-p", fmt.Sprintf("%d:80", *port),
        "-e", "PS_DOMAIN=template.internal",
        "-e", fmt.Sprintf("MYSQL_HOST=%s", *mysqlHost),
        "-e", fmt.Sprintf("MYSQL_USER=%s", *mysqlUser),
        "-e", fmt.Sprintf("MYSQL_PASSWORD=%s", *mysqlPass),
        "-e", fmt.Sprintf("MYSQL_DATABASE=%s", *mysqlDB),
        "-e", fmt.Sprintf("DB_PREFIX=%s", *dbPrefix),
        "-e", "PS_DEV_MODE=0",
        *image,
    )
    startCmd.Stdout = os.Stdout
    startCmd.Stderr = os.Stderr
    if err := startCmd.Run(); err != nil {
        log.Fatalf("Failed to start container: %v", err)
    }

    log.Println("Step 2: Waiting for PrestaShop to be ready...")
    url := fmt.Sprintf("http://localhost:%d/", *port)
    if err := waitForHealth(ctx, url, 5*time.Minute); err != nil {
        log.Fatalf("Container failed to become healthy: %v", err)
    }

    log.Println("Step 3: Warming caches...")
    warmCache(ctx, *port)

    log.Println("Step 4: Creating CRIU checkpoint...")
    checkpointCmd := exec.CommandContext(ctx, "sudo", "podman", "container", "checkpoint",
        "--export", *checkpointPath,
        "--tcp-established",
        *containerName,
    )
    checkpointCmd.Stdout = os.Stdout
    checkpointCmd.Stderr = os.Stderr
    if err := checkpointCmd.Run(); err != nil {
        log.Fatalf("Failed to create checkpoint: %v", err)
    }

    log.Printf("Checkpoint created at: %s\n", *checkpointPath)

    log.Println("Step 5: Cleaning up...")
    exec.Command("podman", "rm", *containerName).Run()

    log.Println("Done!")
}

func waitForHealth(ctx context.Context, url string, timeout time.Duration) error {
    client := &http.Client{Timeout: 5 * time.Second}
    deadline := time.Now().Add(timeout)

    for time.Now().Before(deadline) {
        resp, err := client.Get(url)
        if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 500 {
            resp.Body.Close()
            return nil
        }
        if resp != nil {
            resp.Body.Close()
        }
        time.Sleep(2 * time.Second)
    }
    return fmt.Errorf("timeout waiting for %s", url)
}

func warmCache(ctx context.Context, port int) {
    client := &http.Client{Timeout: 30 * time.Second}
    pages := []string{"/", "/admin-demo", "/en/2-home"}

    for _, page := range pages {
        url := fmt.Sprintf("http://localhost:%d%s", port, page)
        resp, _ := client.Get(url)
        if resp != nil {
            log.Printf("  Warmed: %s (status: %d)", page, resp.StatusCode)
            resp.Body.Close()
        }
    }
}
```

---

### Phase 5: Update Tests (Day 3-4)

**Modify:** `internal/pool/impl_test.go`

Add mock for tests:
```go
type MockPrestaShopDB struct {
    updateDomainCalled bool
    clearCachesCalled  bool
    dropTablesCalled   bool
}

func (m *MockPrestaShopDB) UpdateDomain(ctx context.Context, dbPrefix, domain string) error {
    m.updateDomainCalled = true
    return nil
}

func (m *MockPrestaShopDB) ClearCaches(ctx context.Context, dbPrefix string) error {
    m.clearCachesCalled = true
    return nil
}

func (m *MockPrestaShopDB) DropPrefixedTables(ctx context.Context, dbPrefix string) error {
    m.dropTablesCalled = true
    return nil
}
```

---

## SQL Operations Reference

### Update Domain After CRIU Restore
```sql
UPDATE {prefix}shop_url
SET domain = '{new_domain}', domain_ssl = '{new_domain}'
WHERE id_shop = 1;

UPDATE {prefix}configuration
SET value = '{new_domain}'
WHERE name IN ('PS_SHOP_DOMAIN', 'PS_SHOP_DOMAIN_SSL');
```

### Clear Caches
```sql
TRUNCATE TABLE {prefix}smarty_cache;
TRUNCATE TABLE {prefix}smarty_lazy_cache;
```

### Drop Prefixed Tables
```sql
SELECT table_name FROM information_schema.tables
WHERE table_schema = '{db_name}' AND table_name LIKE '{prefix}%';

DROP TABLE IF EXISTS {table1}, {table2}, ...;
```

---

## Key Files Summary

| File | Action | Purpose |
|------|--------|---------|
| `internal/database/prestashop.go` | CREATE | PrestaShop DB operations |
| `internal/database/prestashop_test.go` | CREATE | Database tests |
| `internal/pool/impl.go` | MODIFY | Add psDB field, update provisioning |
| `cmd/server/main.go` | MODIFY | Wire up PrestaShopDB |
| `cmd/checkpoint/main.go` | CREATE | Checkpoint creation tool |

---

## Code Patterns to Follow

1. **Error Handling:** `return fmt.Errorf("failed to X: %w", err)`
2. **Logging:** `m.logger.Info("Message", "key1", value1)`
3. **Testing:** Table-driven tests, hand-written mocks
4. **Context:** All methods accept `context.Context` as first param

---

## Testing Checklist

- [ ] `go test ./internal/database/...` passes
- [ ] `go test ./internal/pool/...` passes
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] Manual CRIU restore test works
- [ ] Domain updates correctly in browser
- [ ] Tables dropped on TTL expiry

---

## Definition of Done

1. All new code has corresponding unit tests
2. Integration tests pass with real MariaDB and Podman
3. `make test` passes
4. `make lint` passes
5. Checkpoint creation documented
6. Issue `demo-multi-plexer-989` closed with summary comment

---

## Completion Steps

```bash
# When implementation is complete:
bd close demo-multi-plexer-989 --reason="Implemented CRIU checkpoint with post-restore domain config and table cleanup"
bd sync --flush-only
```
