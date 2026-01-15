package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration.
type Config struct {
	Log        LogConfig
	Server     ServerConfig
	Pool       PoolConfig
	Container  ContainerConfig
	Proxy      ProxyConfig
	Store      StoreConfig
	Queue      QueueConfig
	PrestaShop PrestaShopConfig
	RateLimit  RateLimitConfig
}

type LogConfig struct {
	Level  string // debug, info, warn, error
	Format string // json, text
}

type ServerConfig struct {
	Host           string
	Port           int
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	APIKey         string
	TrustedProxies []string // IPs allowed to set X-Forwarded-For
}

type PoolConfig struct {
	TargetSize         int
	MinSize            int
	MaxSize            int
	ReplenishThreshold float64
	ReplenishInterval  time.Duration
	DefaultTTL         time.Duration
	MaxTTL             time.Duration
}

type ContainerConfig struct {
	Mode             string // "docker" or "podman"
	CheckpointPath   string
	Image            string
	Network          string
	PortRangeStart   int
	PortRangeEnd     int
	PodmanSocketPath string // Unix socket path for Podman API
	CRIUEnabled      bool   // Whether to attempt CRIU checkpoint/restore
}

type ProxyConfig struct {
	CaddyAdminURL string
	BaseDomain    string
}

type StoreConfig struct {
	ValkeyAddr string
	Password   string
	DB         int
}

type QueueConfig struct {
	NATSURL     string
	StreamName  string
	WorkerCount int
}

type PrestaShopConfig struct {
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
	AdminPath  string
	DemoUser   string
	DemoPass   string
}

type RateLimitConfig struct {
	RequestsPerHour int
	RequestsPerDay  int
}

// Load loads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Log: LogConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
		},
		Server: ServerConfig{
			Host:           getEnv("SERVER_HOST", "0.0.0.0"),
			Port:           getEnvInt("SERVER_PORT", 8080),
			ReadTimeout:    getEnvDuration("SERVER_READ_TIMEOUT", 30*time.Second),
			WriteTimeout:   getEnvDuration("SERVER_WRITE_TIMEOUT", 30*time.Second),
			APIKey:         getEnv("API_KEY", ""),
			TrustedProxies: getEnvStringSlice("TRUSTED_PROXIES", []string{"127.0.0.1", "::1"}),
		},
		Pool: PoolConfig{
			TargetSize:         getEnvInt("POOL_TARGET_SIZE", 10),
			MinSize:            getEnvInt("POOL_MIN_SIZE", 3),
			MaxSize:            getEnvInt("POOL_MAX_SIZE", 20),
			ReplenishThreshold: getEnvFloat("POOL_REPLENISH_THRESHOLD", 0.5),
			ReplenishInterval:  getEnvDuration("POOL_REPLENISH_INTERVAL", 10*time.Second),
			DefaultTTL:         getEnvDuration("POOL_DEFAULT_TTL", 1*time.Hour),
			MaxTTL:             getEnvDuration("POOL_MAX_TTL", 24*time.Hour),
		},
		Container: ContainerConfig{
			Mode:             getEnv("CONTAINER_MODE", "docker"),
			CheckpointPath:   getEnv("CONTAINER_CHECKPOINT_PATH", "/var/lib/checkpoints/prestashop.tar.gz"),
			Image:            getEnv("CONTAINER_IMAGE", "prestashop/prestashop-flashlight:9.0.0"),
			Network:          getEnv("CONTAINER_NETWORK", ""),
			PortRangeStart:   getEnvInt("CONTAINER_PORT_RANGE_START", 32000),
			PortRangeEnd:     getEnvInt("CONTAINER_PORT_RANGE_END", 32999),
			PodmanSocketPath: getEnv("PODMAN_SOCKET_PATH", "unix:///run/podman/podman.sock"),
			CRIUEnabled:      getEnvBool("CRIU_ENABLED", true),
		},
		Proxy: ProxyConfig{
			CaddyAdminURL: getEnv("CADDY_ADMIN_URL", "http://localhost:2019"),
			BaseDomain:    getEnv("BASE_DOMAIN", "localhost"),
		},
		Store: StoreConfig{
			ValkeyAddr: getEnv("VALKEY_ADDR", "localhost:6379"),
			Password:   getEnv("VALKEY_PASSWORD", ""),
			DB:         getEnvInt("VALKEY_DB", 0),
		},
		Queue: QueueConfig{
			NATSURL:     getEnv("NATS_URL", "nats://localhost:4222"),
			StreamName:  getEnv("NATS_STREAM_NAME", "PROVISIONING"),
			WorkerCount: getEnvInt("NATS_WORKER_COUNT", 3),
		},
		PrestaShop: PrestaShopConfig{
			DBHost:     getEnv("PS_DB_HOST", "localhost"),
			DBPort:     getEnvInt("PS_DB_PORT", 3306),
			DBName:     getEnv("PS_DB_NAME", "prestashop_demos"),
			DBUser:     getEnv("PS_DB_USER", "demo"),
			DBPassword: getEnv("PS_DB_PASSWORD", "devpass"),
			AdminPath:  getEnv("PS_ADMIN_PATH", "admin-demo"),
			DemoUser:   getEnv("PS_DEMO_USER", "demo@demo.com"),
			DemoPass:   getEnv("PS_DEMO_PASS", "demodemo"),
		},
		RateLimit: RateLimitConfig{
			RequestsPerHour: getEnvInt("RATE_LIMIT_HOURLY", 2),
			RequestsPerDay:  getEnvInt("RATE_LIMIT_DAILY", 5),
		},
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvStringSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		parts := strings.Split(value, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return defaultValue
}
