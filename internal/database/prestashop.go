// Package database provides database operations for PrestaShop instances.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/instant-demo/try-it-now/internal/config"
	_ "github.com/go-sql-driver/mysql"
)

// ValidPrefixPattern matches valid database prefixes: 'd' + 8 hex chars + '_'
// Example: d12345678_, dabcdef01_
var ValidPrefixPattern = regexp.MustCompile(`^d[0-9a-f]{8}_$`)

// validateDBPrefix validates that the database prefix matches the expected format.
// This prevents SQL injection via malicious prefix values.
func validateDBPrefix(prefix string) error {
	if !ValidPrefixPattern.MatchString(prefix) {
		return fmt.Errorf("invalid db prefix format: %s", prefix)
	}
	return nil
}

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
		db.Close()
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
	if err := validateDBPrefix(dbPrefix); err != nil {
		return err
	}

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
	if err := validateDBPrefix(dbPrefix); err != nil {
		return err
	}

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
	if err := validateDBPrefix(dbPrefix); err != nil {
		return err
	}

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

	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate table names: %w", err)
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
