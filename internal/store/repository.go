package store

import (
	"context"
	"time"

	"github.com/boss/demo-multiplexer/internal/domain"
)

// Repository defines the interface for state persistence.
// Implementation: Valkey (Redis-compatible).
type Repository interface {
	// Instance operations
	SaveInstance(ctx context.Context, instance *domain.Instance) error
	GetInstance(ctx context.Context, id string) (*domain.Instance, error)
	DeleteInstance(ctx context.Context, id string) error
	UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error

	// Pool operations (atomic)
	AcquireFromPool(ctx context.Context) (*domain.Instance, error)
	AddToPool(ctx context.Context, instance *domain.Instance) error
	RemoveFromPool(ctx context.Context, id string) error

	// Listing
	ListByState(ctx context.Context, state domain.InstanceState) ([]*domain.Instance, error)
	ListExpired(ctx context.Context) ([]*domain.Instance, error)

	// TTL operations
	SetInstanceTTL(ctx context.Context, id string, ttl time.Duration) error
	GetInstanceTTL(ctx context.Context, id string) (time.Duration, error)
	ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error

	// Port allocation
	AllocatePort(ctx context.Context) (int, error)
	ReleasePort(ctx context.Context, port int) error

	// Rate limiting
	CheckRateLimit(ctx context.Context, ip string, hourlyLimit, dailyLimit int) (bool, error)
	IncrementRateLimit(ctx context.Context, ip string) error

	// Statistics
	GetPoolStats(ctx context.Context) (*domain.PoolStats, error)
	IncrementCounter(ctx context.Context, name string) error

	// Health
	Ping(ctx context.Context) error
}
