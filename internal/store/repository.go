package store

import (
	"context"
	"time"

	"github.com/instant-demo/try-it-now/internal/domain"
)

// InstanceStore handles instance CRUD operations.
type InstanceStore interface {
	SaveInstance(ctx context.Context, instance *domain.Instance) error
	GetInstance(ctx context.Context, id string) (*domain.Instance, error)
	DeleteInstance(ctx context.Context, id string) error
	UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error
}

// PoolStore handles pool queue operations.
type PoolStore interface {
	AcquireFromPool(ctx context.Context) (*domain.Instance, error)
	AddToPool(ctx context.Context, instance *domain.Instance) error
	RemoveFromPool(ctx context.Context, id string) error
}

// InstanceLister provides instance listing operations.
type InstanceLister interface {
	ListByState(ctx context.Context, state domain.InstanceState) ([]*domain.Instance, error)
	ListExpired(ctx context.Context) ([]*domain.Instance, error)
}

// TTLManager handles instance TTL operations.
type TTLManager interface {
	SetInstanceTTL(ctx context.Context, id string, ttl time.Duration) error
	GetInstanceTTL(ctx context.Context, id string) (time.Duration, error)
	ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error
}

// PortAllocator handles port management.
type PortAllocator interface {
	AllocatePort(ctx context.Context) (int, error)
	ReleasePort(ctx context.Context, port int) error
}

// RateLimiter handles rate limiting.
type RateLimiter interface {
	CheckRateLimit(ctx context.Context, ip string, hourlyLimit, dailyLimit int) (bool, error)
	IncrementRateLimit(ctx context.Context, ip string) error
}

// StatsCollector handles statistics and counters.
type StatsCollector interface {
	GetPoolStats(ctx context.Context) (*domain.PoolStats, error)
	IncrementCounter(ctx context.Context, name string) error
}

// HealthChecker provides health check capability.
type HealthChecker interface {
	Ping(ctx context.Context) error
}

// Repository composes all focused interfaces for backward compatibility.
// Implementation: Valkey (Redis-compatible).
type Repository interface {
	InstanceStore
	PoolStore
	InstanceLister
	TTLManager
	PortAllocator
	RateLimiter
	StatsCollector
	HealthChecker
}
