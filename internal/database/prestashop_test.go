package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
)

func TestValidateDBPrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		wantErr bool
	}{
		// Valid prefixes
		{name: "valid numeric prefix", prefix: "d12345678_", wantErr: false},
		{name: "valid hex prefix lowercase", prefix: "dabcdef01_", wantErr: false},
		{name: "valid mixed hex prefix", prefix: "d1a2b3c4d_", wantErr: false},
		{name: "valid all zeros", prefix: "d00000000_", wantErr: false},
		{name: "valid all f", prefix: "dffffffff_", wantErr: false},

		// Invalid prefixes - too short
		{name: "too short 7 chars", prefix: "d1234567_", wantErr: true},
		{name: "too short 6 chars", prefix: "d123456_", wantErr: true},
		{name: "empty prefix", prefix: "", wantErr: true},

		// Invalid prefixes - too long
		{name: "too long 9 chars", prefix: "d123456789_", wantErr: true},
		{name: "too long 10 chars", prefix: "d1234567890_", wantErr: true},

		// Invalid prefixes - bad characters
		{name: "uppercase hex", prefix: "dABCDEF01_", wantErr: true},
		{name: "mixed case hex", prefix: "dAbCdEf01_", wantErr: true},
		{name: "invalid char g", prefix: "d1234567g_", wantErr: true},
		{name: "special chars", prefix: "d12345;--_", wantErr: true},

		// Invalid prefixes - missing d prefix
		{name: "missing d prefix", prefix: "12345678_", wantErr: true},
		{name: "wrong prefix letter", prefix: "a12345678_", wantErr: true},
		{name: "uppercase D", prefix: "D12345678_", wantErr: true},

		// Invalid prefixes - missing underscore
		{name: "missing underscore", prefix: "d12345678", wantErr: true},
		{name: "wrong suffix", prefix: "d12345678-", wantErr: true},

		// SQL injection attempts
		{name: "sql injection drop table", prefix: "; DROP TABLE users; --", wantErr: true},
		{name: "sql injection with prefix", prefix: "d12345678_; DROP TABLE--", wantErr: true},
		{name: "sql injection quotes", prefix: "d12345678_' OR '1'='1", wantErr: true},
		{name: "sql injection union", prefix: "d12345678_ UNION SELECT", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDBPrefix(tt.prefix)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDBPrefix(%q) error = %v, wantErr %v", tt.prefix, err, tt.wantErr)
			}
		})
	}
}

func TestValidPrefixPattern(t *testing.T) {
	// Verify the pattern is correctly compiled
	if ValidPrefixPattern == nil {
		t.Fatal("ValidPrefixPattern is nil")
	}

	// Test the regex directly
	if !ValidPrefixPattern.MatchString("d12345678_") {
		t.Error("ValidPrefixPattern should match d12345678_")
	}
	if ValidPrefixPattern.MatchString("invalid") {
		t.Error("ValidPrefixPattern should not match 'invalid'")
	}
}

// skipIfNoMariaDB skips the test if PS_DB_TEST is not set or MariaDB is not available.
func skipIfNoMariaDB(t *testing.T) *PrestaShopDB {
	t.Helper()

	if os.Getenv("PS_DB_TEST") != "1" {
		t.Skip("Skipping database test: set PS_DB_TEST=1 to run")
	}

	cfg := &config.PrestaShopConfig{
		DBHost:     getTestEnv("PS_DB_HOST", "localhost"),
		DBPort:     3306,
		DBName:     getTestEnv("PS_DB_NAME", "prestashop_demos"),
		DBUser:     getTestEnv("PS_DB_USER", "demo"),
		DBPassword: getTestEnv("PS_DB_PASSWORD", "devpass"),
	}

	db, err := NewPrestaShopDB(cfg)
	if err != nil {
		t.Skipf("Skipping database test: could not connect to MariaDB: %v", err)
	}

	return db
}

func getTestEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func TestNewPrestaShopDB(t *testing.T) {
	db := skipIfNoMariaDB(t)
	defer db.Close()

	// Verify connection is alive
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestPrestaShopDB_UpdateDomain(t *testing.T) {
	db := skipIfNoMariaDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create test tables with a valid prefix (d + 8 hex chars + _)
	testPrefix := "d00000001_"
	if err := createTestTables(ctx, db, testPrefix); err != nil {
		t.Fatalf("Failed to create test tables: %v", err)
	}
	defer dropTestTables(ctx, db, testPrefix)

	// Insert test data
	if err := insertTestData(ctx, db, testPrefix); err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Test UpdateDomain
	newDomain := "new-demo.example.com"
	if err := db.UpdateDomain(ctx, testPrefix, newDomain); err != nil {
		t.Errorf("UpdateDomain() error = %v", err)
	}

	// Verify shop_url was updated
	var domain, domainSSL string
	query := "SELECT domain, domain_ssl FROM " + testPrefix + "shop_url WHERE id_shop = 1"
	if err := db.db.QueryRowContext(ctx, query).Scan(&domain, &domainSSL); err != nil {
		t.Fatalf("Failed to verify shop_url: %v", err)
	}
	if domain != newDomain {
		t.Errorf("shop_url.domain = %q, want %q", domain, newDomain)
	}
	if domainSSL != newDomain {
		t.Errorf("shop_url.domain_ssl = %q, want %q", domainSSL, newDomain)
	}

	// Verify configuration was updated
	var configValue string
	query = "SELECT value FROM " + testPrefix + "configuration WHERE name = 'PS_SHOP_DOMAIN'"
	if err := db.db.QueryRowContext(ctx, query).Scan(&configValue); err != nil {
		t.Fatalf("Failed to verify configuration: %v", err)
	}
	if configValue != newDomain {
		t.Errorf("configuration.PS_SHOP_DOMAIN = %q, want %q", configValue, newDomain)
	}
}

func TestPrestaShopDB_ClearCaches(t *testing.T) {
	db := skipIfNoMariaDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create test cache tables with a valid prefix
	testPrefix := "d00000002_"
	if err := createCacheTables(ctx, db, testPrefix); err != nil {
		t.Fatalf("Failed to create cache tables: %v", err)
	}
	defer dropTestTables(ctx, db, testPrefix)

	// Insert some cache data
	insertCacheData(ctx, db, testPrefix)

	// Clear caches
	if err := db.ClearCaches(ctx, testPrefix); err != nil {
		t.Errorf("ClearCaches() error = %v", err)
	}

	// Verify caches are empty
	var count int
	query := "SELECT COUNT(*) FROM " + testPrefix + "smarty_cache"
	if err := db.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		t.Fatalf("Failed to count cache rows: %v", err)
	}
	if count != 0 {
		t.Errorf("smarty_cache has %d rows after ClearCaches, want 0", count)
	}
}

func TestPrestaShopDB_DropPrefixedTables(t *testing.T) {
	db := skipIfNoMariaDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create test tables with valid prefix
	testPrefix := "d00000003_"
	if err := createTestTables(ctx, db, testPrefix); err != nil {
		t.Fatalf("Failed to create test tables: %v", err)
	}

	// Verify tables exist
	tables, err := listPrefixedTables(ctx, db, testPrefix)
	if err != nil {
		t.Fatalf("Failed to list tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("Expected test tables to exist before drop")
	}

	// Drop prefixed tables
	if err := db.DropPrefixedTables(ctx, testPrefix); err != nil {
		t.Errorf("DropPrefixedTables() error = %v", err)
	}

	// Verify tables are gone
	tables, err = listPrefixedTables(ctx, db, testPrefix)
	if err != nil {
		t.Fatalf("Failed to list tables after drop: %v", err)
	}
	if len(tables) != 0 {
		t.Errorf("Found %d tables after DropPrefixedTables, want 0: %v", len(tables), tables)
	}
}

func TestPrestaShopDB_DropPrefixedTables_NoTables(t *testing.T) {
	db := skipIfNoMariaDB(t)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Drop non-existent tables should not error (use valid prefix format)
	if err := db.DropPrefixedTables(ctx, "d00000099_"); err != nil {
		t.Errorf("DropPrefixedTables() with no tables error = %v", err)
	}
}

// Helper functions for test setup/teardown

func createTestTables(ctx context.Context, db *PrestaShopDB, prefix string) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS ` + prefix + `shop_url (
			id_shop INT PRIMARY KEY,
			domain VARCHAR(255),
			domain_ssl VARCHAR(255)
		)`,
		`CREATE TABLE IF NOT EXISTS ` + prefix + `configuration (
			id_configuration INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255),
			value TEXT
		)`,
	}

	for _, q := range queries {
		if _, err := db.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func insertTestData(ctx context.Context, db *PrestaShopDB, prefix string) error {
	queries := []string{
		`INSERT INTO ` + prefix + `shop_url (id_shop, domain, domain_ssl) VALUES (1, 'old.example.com', 'old.example.com')`,
		`INSERT INTO ` + prefix + `configuration (name, value) VALUES ('PS_SHOP_DOMAIN', 'old.example.com')`,
		`INSERT INTO ` + prefix + `configuration (name, value) VALUES ('PS_SHOP_DOMAIN_SSL', 'old.example.com')`,
	}

	for _, q := range queries {
		if _, err := db.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func createCacheTables(ctx context.Context, db *PrestaShopDB, prefix string) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS ` + prefix + `smarty_cache (
			id_smarty_cache INT AUTO_INCREMENT PRIMARY KEY,
			cache_id VARCHAR(255),
			content TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS ` + prefix + `smarty_lazy_cache (
			id_smarty_lazy_cache INT AUTO_INCREMENT PRIMARY KEY,
			cache_id VARCHAR(255),
			content TEXT
		)`,
	}

	for _, q := range queries {
		if _, err := db.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func insertCacheData(ctx context.Context, db *PrestaShopDB, prefix string) {
	queries := []string{
		`INSERT INTO ` + prefix + `smarty_cache (cache_id, content) VALUES ('test1', 'cached data')`,
		`INSERT INTO ` + prefix + `smarty_lazy_cache (cache_id, content) VALUES ('test2', 'lazy cached data')`,
	}
	for _, q := range queries {
		db.db.ExecContext(ctx, q)
	}
}

func dropTestTables(ctx context.Context, db *PrestaShopDB, prefix string) {
	db.DropPrefixedTables(ctx, prefix)
}

func listPrefixedTables(ctx context.Context, db *PrestaShopDB, prefix string) ([]string, error) {
	query := `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = ? AND table_name LIKE ?
	`
	rows, err := db.db.QueryContext(ctx, query, db.dbName, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}
