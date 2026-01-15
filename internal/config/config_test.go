package config

import (
	"testing"
	"time"
)

func TestLoad_DefaultValues(t *testing.T) {
	cfg := Load()

	// Server defaults
	t.Run("ServerConfig defaults", func(t *testing.T) {
		if cfg.Server.Host != "0.0.0.0" {
			t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "0.0.0.0")
		}
		if cfg.Server.Port != 8080 {
			t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 8080)
		}
		if cfg.Server.ReadTimeout != 30*time.Second {
			t.Errorf("Server.ReadTimeout = %v, want %v", cfg.Server.ReadTimeout, 30*time.Second)
		}
		if cfg.Server.WriteTimeout != 30*time.Second {
			t.Errorf("Server.WriteTimeout = %v, want %v", cfg.Server.WriteTimeout, 30*time.Second)
		}
	})

	// Pool defaults
	t.Run("PoolConfig defaults", func(t *testing.T) {
		if cfg.Pool.TargetSize != 10 {
			t.Errorf("Pool.TargetSize = %d, want %d", cfg.Pool.TargetSize, 10)
		}
		if cfg.Pool.MinSize != 3 {
			t.Errorf("Pool.MinSize = %d, want %d", cfg.Pool.MinSize, 3)
		}
		if cfg.Pool.MaxSize != 20 {
			t.Errorf("Pool.MaxSize = %d, want %d", cfg.Pool.MaxSize, 20)
		}
		if cfg.Pool.ReplenishThreshold != 0.5 {
			t.Errorf("Pool.ReplenishThreshold = %f, want %f", cfg.Pool.ReplenishThreshold, 0.5)
		}
		if cfg.Pool.ReplenishInterval != 10*time.Second {
			t.Errorf("Pool.ReplenishInterval = %v, want %v", cfg.Pool.ReplenishInterval, 10*time.Second)
		}
		if cfg.Pool.DefaultTTL != 1*time.Hour {
			t.Errorf("Pool.DefaultTTL = %v, want %v", cfg.Pool.DefaultTTL, 1*time.Hour)
		}
		if cfg.Pool.MaxTTL != 24*time.Hour {
			t.Errorf("Pool.MaxTTL = %v, want %v", cfg.Pool.MaxTTL, 24*time.Hour)
		}
	})

	// Container defaults
	t.Run("ContainerConfig defaults", func(t *testing.T) {
		if cfg.Container.Mode != "docker" {
			t.Errorf("Container.Mode = %q, want %q", cfg.Container.Mode, "docker")
		}
		if cfg.Container.CheckpointPath != "/var/lib/checkpoints/prestashop.tar.gz" {
			t.Errorf("Container.CheckpointPath = %q, want %q", cfg.Container.CheckpointPath, "/var/lib/checkpoints/prestashop.tar.gz")
		}
		if cfg.Container.Image != "prestashop/prestashop-flashlight:9.0.0" {
			t.Errorf("Container.Image = %q, want %q", cfg.Container.Image, "prestashop/prestashop-flashlight:9.0.0")
		}
		if cfg.Container.Network != "demo-net" {
			t.Errorf("Container.Network = %q, want %q", cfg.Container.Network, "demo-net")
		}
		if cfg.Container.PortRangeStart != 32000 {
			t.Errorf("Container.PortRangeStart = %d, want %d", cfg.Container.PortRangeStart, 32000)
		}
		if cfg.Container.PortRangeEnd != 32999 {
			t.Errorf("Container.PortRangeEnd = %d, want %d", cfg.Container.PortRangeEnd, 32999)
		}
	})

	// Proxy defaults
	t.Run("ProxyConfig defaults", func(t *testing.T) {
		if cfg.Proxy.CaddyAdminURL != "http://localhost:2019" {
			t.Errorf("Proxy.CaddyAdminURL = %q, want %q", cfg.Proxy.CaddyAdminURL, "http://localhost:2019")
		}
		if cfg.Proxy.BaseDomain != "localhost" {
			t.Errorf("Proxy.BaseDomain = %q, want %q", cfg.Proxy.BaseDomain, "localhost")
		}
	})

	// Store defaults
	t.Run("StoreConfig defaults", func(t *testing.T) {
		if cfg.Store.ValkeyAddr != "localhost:6379" {
			t.Errorf("Store.ValkeyAddr = %q, want %q", cfg.Store.ValkeyAddr, "localhost:6379")
		}
		if cfg.Store.Password != "" {
			t.Errorf("Store.Password = %q, want %q", cfg.Store.Password, "")
		}
		if cfg.Store.DB != 0 {
			t.Errorf("Store.DB = %d, want %d", cfg.Store.DB, 0)
		}
	})

	// Queue defaults
	t.Run("QueueConfig defaults", func(t *testing.T) {
		if cfg.Queue.NATSURL != "nats://localhost:4222" {
			t.Errorf("Queue.NATSURL = %q, want %q", cfg.Queue.NATSURL, "nats://localhost:4222")
		}
		if cfg.Queue.StreamName != "PROVISIONING" {
			t.Errorf("Queue.StreamName = %q, want %q", cfg.Queue.StreamName, "PROVISIONING")
		}
		if cfg.Queue.WorkerCount != 3 {
			t.Errorf("Queue.WorkerCount = %d, want %d", cfg.Queue.WorkerCount, 3)
		}
	})

	// PrestaShop defaults
	t.Run("PrestaShopConfig defaults", func(t *testing.T) {
		if cfg.PrestaShop.DBHost != "localhost" {
			t.Errorf("PrestaShop.DBHost = %q, want %q", cfg.PrestaShop.DBHost, "localhost")
		}
		if cfg.PrestaShop.DBPort != 3306 {
			t.Errorf("PrestaShop.DBPort = %d, want %d", cfg.PrestaShop.DBPort, 3306)
		}
		if cfg.PrestaShop.DBName != "prestashop_demos" {
			t.Errorf("PrestaShop.DBName = %q, want %q", cfg.PrestaShop.DBName, "prestashop_demos")
		}
		if cfg.PrestaShop.DBUser != "demo" {
			t.Errorf("PrestaShop.DBUser = %q, want %q", cfg.PrestaShop.DBUser, "demo")
		}
		if cfg.PrestaShop.DBPassword != "devpass" {
			t.Errorf("PrestaShop.DBPassword = %q, want %q", cfg.PrestaShop.DBPassword, "devpass")
		}
		if cfg.PrestaShop.AdminPath != "admin-demo" {
			t.Errorf("PrestaShop.AdminPath = %q, want %q", cfg.PrestaShop.AdminPath, "admin-demo")
		}
		if cfg.PrestaShop.DemoUser != "demo@demo.com" {
			t.Errorf("PrestaShop.DemoUser = %q, want %q", cfg.PrestaShop.DemoUser, "demo@demo.com")
		}
		if cfg.PrestaShop.DemoPass != "demodemo" {
			t.Errorf("PrestaShop.DemoPass = %q, want %q", cfg.PrestaShop.DemoPass, "demodemo")
		}
	})

	// RateLimit defaults
	t.Run("RateLimitConfig defaults", func(t *testing.T) {
		if cfg.RateLimit.RequestsPerHour != 2 {
			t.Errorf("RateLimit.RequestsPerHour = %d, want %d", cfg.RateLimit.RequestsPerHour, 2)
		}
		if cfg.RateLimit.RequestsPerDay != 5 {
			t.Errorf("RateLimit.RequestsPerDay = %d, want %d", cfg.RateLimit.RequestsPerDay, 5)
		}
	})
}

func TestLoad_CustomEnvVars(t *testing.T) {
	// Server custom values
	t.Run("ServerConfig custom values", func(t *testing.T) {
		t.Setenv("SERVER_HOST", "127.0.0.1")
		t.Setenv("SERVER_PORT", "9090")
		t.Setenv("SERVER_READ_TIMEOUT", "45s")
		t.Setenv("SERVER_WRITE_TIMEOUT", "1m")

		cfg := Load()

		if cfg.Server.Host != "127.0.0.1" {
			t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "127.0.0.1")
		}
		if cfg.Server.Port != 9090 {
			t.Errorf("Server.Port = %d, want %d", cfg.Server.Port, 9090)
		}
		if cfg.Server.ReadTimeout != 45*time.Second {
			t.Errorf("Server.ReadTimeout = %v, want %v", cfg.Server.ReadTimeout, 45*time.Second)
		}
		if cfg.Server.WriteTimeout != 1*time.Minute {
			t.Errorf("Server.WriteTimeout = %v, want %v", cfg.Server.WriteTimeout, 1*time.Minute)
		}
	})

	// Pool custom values
	t.Run("PoolConfig custom values", func(t *testing.T) {
		t.Setenv("POOL_TARGET_SIZE", "25")
		t.Setenv("POOL_MIN_SIZE", "5")
		t.Setenv("POOL_MAX_SIZE", "50")
		t.Setenv("POOL_REPLENISH_THRESHOLD", "0.75")
		t.Setenv("POOL_REPLENISH_INTERVAL", "30s")
		t.Setenv("POOL_DEFAULT_TTL", "2h")
		t.Setenv("POOL_MAX_TTL", "48h")

		cfg := Load()

		if cfg.Pool.TargetSize != 25 {
			t.Errorf("Pool.TargetSize = %d, want %d", cfg.Pool.TargetSize, 25)
		}
		if cfg.Pool.MinSize != 5 {
			t.Errorf("Pool.MinSize = %d, want %d", cfg.Pool.MinSize, 5)
		}
		if cfg.Pool.MaxSize != 50 {
			t.Errorf("Pool.MaxSize = %d, want %d", cfg.Pool.MaxSize, 50)
		}
		if cfg.Pool.ReplenishThreshold != 0.75 {
			t.Errorf("Pool.ReplenishThreshold = %f, want %f", cfg.Pool.ReplenishThreshold, 0.75)
		}
		if cfg.Pool.ReplenishInterval != 30*time.Second {
			t.Errorf("Pool.ReplenishInterval = %v, want %v", cfg.Pool.ReplenishInterval, 30*time.Second)
		}
		if cfg.Pool.DefaultTTL != 2*time.Hour {
			t.Errorf("Pool.DefaultTTL = %v, want %v", cfg.Pool.DefaultTTL, 2*time.Hour)
		}
		if cfg.Pool.MaxTTL != 48*time.Hour {
			t.Errorf("Pool.MaxTTL = %v, want %v", cfg.Pool.MaxTTL, 48*time.Hour)
		}
	})

	// Container custom values
	t.Run("ContainerConfig custom values", func(t *testing.T) {
		t.Setenv("CONTAINER_MODE", "podman")
		t.Setenv("CONTAINER_CHECKPOINT_PATH", "/custom/path/checkpoint.tar.gz")
		t.Setenv("CONTAINER_IMAGE", "custom/image:1.0")
		t.Setenv("CONTAINER_NETWORK", "custom-net")
		t.Setenv("CONTAINER_PORT_RANGE_START", "40000")
		t.Setenv("CONTAINER_PORT_RANGE_END", "40999")

		cfg := Load()

		if cfg.Container.Mode != "podman" {
			t.Errorf("Container.Mode = %q, want %q", cfg.Container.Mode, "podman")
		}
		if cfg.Container.CheckpointPath != "/custom/path/checkpoint.tar.gz" {
			t.Errorf("Container.CheckpointPath = %q, want %q", cfg.Container.CheckpointPath, "/custom/path/checkpoint.tar.gz")
		}
		if cfg.Container.Image != "custom/image:1.0" {
			t.Errorf("Container.Image = %q, want %q", cfg.Container.Image, "custom/image:1.0")
		}
		if cfg.Container.Network != "custom-net" {
			t.Errorf("Container.Network = %q, want %q", cfg.Container.Network, "custom-net")
		}
		if cfg.Container.PortRangeStart != 40000 {
			t.Errorf("Container.PortRangeStart = %d, want %d", cfg.Container.PortRangeStart, 40000)
		}
		if cfg.Container.PortRangeEnd != 40999 {
			t.Errorf("Container.PortRangeEnd = %d, want %d", cfg.Container.PortRangeEnd, 40999)
		}
	})

	// Proxy custom values
	t.Run("ProxyConfig custom values", func(t *testing.T) {
		t.Setenv("CADDY_ADMIN_URL", "http://caddy:2019")
		t.Setenv("BASE_DOMAIN", "example.com")

		cfg := Load()

		if cfg.Proxy.CaddyAdminURL != "http://caddy:2019" {
			t.Errorf("Proxy.CaddyAdminURL = %q, want %q", cfg.Proxy.CaddyAdminURL, "http://caddy:2019")
		}
		if cfg.Proxy.BaseDomain != "example.com" {
			t.Errorf("Proxy.BaseDomain = %q, want %q", cfg.Proxy.BaseDomain, "example.com")
		}
	})

	// Store custom values
	t.Run("StoreConfig custom values", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "valkey:6379")
		t.Setenv("VALKEY_PASSWORD", "secret123")
		t.Setenv("VALKEY_DB", "5")

		cfg := Load()

		if cfg.Store.ValkeyAddr != "valkey:6379" {
			t.Errorf("Store.ValkeyAddr = %q, want %q", cfg.Store.ValkeyAddr, "valkey:6379")
		}
		if cfg.Store.Password != "secret123" {
			t.Errorf("Store.Password = %q, want %q", cfg.Store.Password, "secret123")
		}
		if cfg.Store.DB != 5 {
			t.Errorf("Store.DB = %d, want %d", cfg.Store.DB, 5)
		}
	})

	// Queue custom values
	t.Run("QueueConfig custom values", func(t *testing.T) {
		t.Setenv("NATS_URL", "nats://nats:4222")
		t.Setenv("NATS_STREAM_NAME", "CUSTOM_STREAM")
		t.Setenv("NATS_WORKER_COUNT", "10")

		cfg := Load()

		if cfg.Queue.NATSURL != "nats://nats:4222" {
			t.Errorf("Queue.NATSURL = %q, want %q", cfg.Queue.NATSURL, "nats://nats:4222")
		}
		if cfg.Queue.StreamName != "CUSTOM_STREAM" {
			t.Errorf("Queue.StreamName = %q, want %q", cfg.Queue.StreamName, "CUSTOM_STREAM")
		}
		if cfg.Queue.WorkerCount != 10 {
			t.Errorf("Queue.WorkerCount = %d, want %d", cfg.Queue.WorkerCount, 10)
		}
	})

	// PrestaShop custom values
	t.Run("PrestaShopConfig custom values", func(t *testing.T) {
		t.Setenv("PS_DB_HOST", "mysql")
		t.Setenv("PS_DB_PORT", "3307")
		t.Setenv("PS_DB_NAME", "custom_db")
		t.Setenv("PS_DB_USER", "admin")
		t.Setenv("PS_DB_PASSWORD", "strongpass")
		t.Setenv("PS_ADMIN_PATH", "admin-custom")
		t.Setenv("PS_DEMO_USER", "admin@example.com")
		t.Setenv("PS_DEMO_PASS", "adminpass")

		cfg := Load()

		if cfg.PrestaShop.DBHost != "mysql" {
			t.Errorf("PrestaShop.DBHost = %q, want %q", cfg.PrestaShop.DBHost, "mysql")
		}
		if cfg.PrestaShop.DBPort != 3307 {
			t.Errorf("PrestaShop.DBPort = %d, want %d", cfg.PrestaShop.DBPort, 3307)
		}
		if cfg.PrestaShop.DBName != "custom_db" {
			t.Errorf("PrestaShop.DBName = %q, want %q", cfg.PrestaShop.DBName, "custom_db")
		}
		if cfg.PrestaShop.DBUser != "admin" {
			t.Errorf("PrestaShop.DBUser = %q, want %q", cfg.PrestaShop.DBUser, "admin")
		}
		if cfg.PrestaShop.DBPassword != "strongpass" {
			t.Errorf("PrestaShop.DBPassword = %q, want %q", cfg.PrestaShop.DBPassword, "strongpass")
		}
		if cfg.PrestaShop.AdminPath != "admin-custom" {
			t.Errorf("PrestaShop.AdminPath = %q, want %q", cfg.PrestaShop.AdminPath, "admin-custom")
		}
		if cfg.PrestaShop.DemoUser != "admin@example.com" {
			t.Errorf("PrestaShop.DemoUser = %q, want %q", cfg.PrestaShop.DemoUser, "admin@example.com")
		}
		if cfg.PrestaShop.DemoPass != "adminpass" {
			t.Errorf("PrestaShop.DemoPass = %q, want %q", cfg.PrestaShop.DemoPass, "adminpass")
		}
	})

	// RateLimit custom values
	t.Run("RateLimitConfig custom values", func(t *testing.T) {
		t.Setenv("RATE_LIMIT_HOURLY", "10")
		t.Setenv("RATE_LIMIT_DAILY", "50")

		cfg := Load()

		if cfg.RateLimit.RequestsPerHour != 10 {
			t.Errorf("RateLimit.RequestsPerHour = %d, want %d", cfg.RateLimit.RequestsPerHour, 10)
		}
		if cfg.RateLimit.RequestsPerDay != 50 {
			t.Errorf("RateLimit.RequestsPerDay = %d, want %d", cfg.RateLimit.RequestsPerDay, 50)
		}
	})
}

func TestGetEnv(t *testing.T) {
	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("TEST_ENV_VAR", "custom_value")
		result := getEnv("TEST_ENV_VAR", "default")
		if result != "custom_value" {
			t.Errorf("getEnv() = %q, want %q", result, "custom_value")
		}
	})

	t.Run("returns default when env not set", func(t *testing.T) {
		result := getEnv("NONEXISTENT_VAR", "default_value")
		if result != "default_value" {
			t.Errorf("getEnv() = %q, want %q", result, "default_value")
		}
	})

	t.Run("returns default when env is empty string", func(t *testing.T) {
		t.Setenv("EMPTY_VAR", "")
		result := getEnv("EMPTY_VAR", "default")
		if result != "default" {
			t.Errorf("getEnv() = %q, want %q", result, "default")
		}
	})
}

func TestGetEnvInt(t *testing.T) {
	t.Run("returns parsed int when valid", func(t *testing.T) {
		t.Setenv("INT_VAR", "42")
		result := getEnvInt("INT_VAR", 0)
		if result != 42 {
			t.Errorf("getEnvInt() = %d, want %d", result, 42)
		}
	})

	t.Run("returns default when env not set", func(t *testing.T) {
		result := getEnvInt("NONEXISTENT_INT", 99)
		if result != 99 {
			t.Errorf("getEnvInt() = %d, want %d", result, 99)
		}
	})

	t.Run("returns default when env is empty string", func(t *testing.T) {
		t.Setenv("EMPTY_INT", "")
		result := getEnvInt("EMPTY_INT", 100)
		if result != 100 {
			t.Errorf("getEnvInt() = %d, want %d", result, 100)
		}
	})

	t.Run("returns default when env is invalid int", func(t *testing.T) {
		t.Setenv("INVALID_INT", "not_a_number")
		result := getEnvInt("INVALID_INT", 50)
		if result != 50 {
			t.Errorf("getEnvInt() = %d, want %d", result, 50)
		}
	})

	t.Run("returns default when env is float", func(t *testing.T) {
		t.Setenv("FLOAT_AS_INT", "3.14")
		result := getEnvInt("FLOAT_AS_INT", 10)
		if result != 10 {
			t.Errorf("getEnvInt() = %d, want %d", result, 10)
		}
	})

	t.Run("handles negative integers", func(t *testing.T) {
		t.Setenv("NEGATIVE_INT", "-5")
		result := getEnvInt("NEGATIVE_INT", 0)
		if result != -5 {
			t.Errorf("getEnvInt() = %d, want %d", result, -5)
		}
	})

	t.Run("handles zero", func(t *testing.T) {
		t.Setenv("ZERO_INT", "0")
		result := getEnvInt("ZERO_INT", 99)
		if result != 0 {
			t.Errorf("getEnvInt() = %d, want %d", result, 0)
		}
	})
}

func TestGetEnvFloat(t *testing.T) {
	t.Run("returns parsed float when valid", func(t *testing.T) {
		t.Setenv("FLOAT_VAR", "3.14")
		result := getEnvFloat("FLOAT_VAR", 0.0)
		if result != 3.14 {
			t.Errorf("getEnvFloat() = %f, want %f", result, 3.14)
		}
	})

	t.Run("returns default when env not set", func(t *testing.T) {
		result := getEnvFloat("NONEXISTENT_FLOAT", 1.5)
		if result != 1.5 {
			t.Errorf("getEnvFloat() = %f, want %f", result, 1.5)
		}
	})

	t.Run("returns default when env is empty string", func(t *testing.T) {
		t.Setenv("EMPTY_FLOAT", "")
		result := getEnvFloat("EMPTY_FLOAT", 2.5)
		if result != 2.5 {
			t.Errorf("getEnvFloat() = %f, want %f", result, 2.5)
		}
	})

	t.Run("returns default when env is invalid float", func(t *testing.T) {
		t.Setenv("INVALID_FLOAT", "not_a_number")
		result := getEnvFloat("INVALID_FLOAT", 9.9)
		if result != 9.9 {
			t.Errorf("getEnvFloat() = %f, want %f", result, 9.9)
		}
	})

	t.Run("parses integer as float", func(t *testing.T) {
		t.Setenv("INT_AS_FLOAT", "42")
		result := getEnvFloat("INT_AS_FLOAT", 0.0)
		if result != 42.0 {
			t.Errorf("getEnvFloat() = %f, want %f", result, 42.0)
		}
	})

	t.Run("handles negative floats", func(t *testing.T) {
		t.Setenv("NEGATIVE_FLOAT", "-2.5")
		result := getEnvFloat("NEGATIVE_FLOAT", 0.0)
		if result != -2.5 {
			t.Errorf("getEnvFloat() = %f, want %f", result, -2.5)
		}
	})

	t.Run("handles zero", func(t *testing.T) {
		t.Setenv("ZERO_FLOAT", "0.0")
		result := getEnvFloat("ZERO_FLOAT", 99.9)
		if result != 0.0 {
			t.Errorf("getEnvFloat() = %f, want %f", result, 0.0)
		}
	})

	t.Run("handles scientific notation", func(t *testing.T) {
		t.Setenv("SCIENTIFIC_FLOAT", "1.5e-3")
		result := getEnvFloat("SCIENTIFIC_FLOAT", 0.0)
		if result != 0.0015 {
			t.Errorf("getEnvFloat() = %f, want %f", result, 0.0015)
		}
	})
}

func TestGetEnvDuration(t *testing.T) {
	t.Run("parses seconds", func(t *testing.T) {
		t.Setenv("DURATION_SECONDS", "30s")
		result := getEnvDuration("DURATION_SECONDS", 0)
		if result != 30*time.Second {
			t.Errorf("getEnvDuration() = %v, want %v", result, 30*time.Second)
		}
	})

	t.Run("parses minutes", func(t *testing.T) {
		t.Setenv("DURATION_MINUTES", "5m")
		result := getEnvDuration("DURATION_MINUTES", 0)
		if result != 5*time.Minute {
			t.Errorf("getEnvDuration() = %v, want %v", result, 5*time.Minute)
		}
	})

	t.Run("parses hours", func(t *testing.T) {
		t.Setenv("DURATION_HOURS", "2h")
		result := getEnvDuration("DURATION_HOURS", 0)
		if result != 2*time.Hour {
			t.Errorf("getEnvDuration() = %v, want %v", result, 2*time.Hour)
		}
	})

	t.Run("parses complex duration", func(t *testing.T) {
		t.Setenv("DURATION_COMPLEX", "1h30m45s")
		result := getEnvDuration("DURATION_COMPLEX", 0)
		expected := 1*time.Hour + 30*time.Minute + 45*time.Second
		if result != expected {
			t.Errorf("getEnvDuration() = %v, want %v", result, expected)
		}
	})

	t.Run("parses milliseconds", func(t *testing.T) {
		t.Setenv("DURATION_MS", "500ms")
		result := getEnvDuration("DURATION_MS", 0)
		if result != 500*time.Millisecond {
			t.Errorf("getEnvDuration() = %v, want %v", result, 500*time.Millisecond)
		}
	})

	t.Run("returns default when env not set", func(t *testing.T) {
		result := getEnvDuration("NONEXISTENT_DURATION", 10*time.Second)
		if result != 10*time.Second {
			t.Errorf("getEnvDuration() = %v, want %v", result, 10*time.Second)
		}
	})

	t.Run("returns default when env is empty string", func(t *testing.T) {
		t.Setenv("EMPTY_DURATION", "")
		result := getEnvDuration("EMPTY_DURATION", 20*time.Second)
		if result != 20*time.Second {
			t.Errorf("getEnvDuration() = %v, want %v", result, 20*time.Second)
		}
	})

	t.Run("returns default when env is invalid duration", func(t *testing.T) {
		t.Setenv("INVALID_DURATION", "not_a_duration")
		result := getEnvDuration("INVALID_DURATION", 15*time.Second)
		if result != 15*time.Second {
			t.Errorf("getEnvDuration() = %v, want %v", result, 15*time.Second)
		}
	})

	t.Run("returns default when env is just a number", func(t *testing.T) {
		t.Setenv("NUMBER_DURATION", "30")
		result := getEnvDuration("NUMBER_DURATION", 5*time.Second)
		if result != 5*time.Second {
			t.Errorf("getEnvDuration() = %v, want %v", result, 5*time.Second)
		}
	})

	t.Run("returns default when env has invalid unit", func(t *testing.T) {
		t.Setenv("BAD_UNIT_DURATION", "30x")
		result := getEnvDuration("BAD_UNIT_DURATION", 5*time.Second)
		if result != 5*time.Second {
			t.Errorf("getEnvDuration() = %v, want %v", result, 5*time.Second)
		}
	})

	t.Run("handles zero duration", func(t *testing.T) {
		t.Setenv("ZERO_DURATION", "0s")
		result := getEnvDuration("ZERO_DURATION", 10*time.Second)
		if result != 0 {
			t.Errorf("getEnvDuration() = %v, want %v", result, time.Duration(0))
		}
	})

	t.Run("handles negative duration", func(t *testing.T) {
		t.Setenv("NEGATIVE_DURATION", "-5s")
		result := getEnvDuration("NEGATIVE_DURATION", 10*time.Second)
		if result != -5*time.Second {
			t.Errorf("getEnvDuration() = %v, want %v", result, -5*time.Second)
		}
	})
}
