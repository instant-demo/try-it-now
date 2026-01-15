package pool

import (
	"context"
	"time"

	"github.com/boss/demo-multiplexer/internal/domain"
)

// Manager defines the interface for warm pool management.
type Manager interface {
	// Acquire gets an instance from the warm pool.
	// This should be instant (LPOP from ready queue).
	// Returns ErrPoolExhausted if no instances available.
	Acquire(ctx context.Context) (*domain.Instance, error)

	// Release returns an instance to be cleaned up.
	Release(ctx context.Context, instanceID string) error

	// Stats returns current pool statistics.
	Stats(ctx context.Context) (*domain.PoolStats, error)

	// StartReplenisher starts the background replenishment loop.
	// This monitors pool levels and triggers provisioning when needed.
	StartReplenisher(ctx context.Context) error

	// StopReplenisher stops the background replenishment loop.
	StopReplenisher() error

	// TriggerReplenish manually triggers a replenishment check.
	TriggerReplenish(ctx context.Context) error
}

// ManagerConfig holds configuration for the pool manager.
type ManagerConfig struct {
	TargetPoolSize     int           // Target number of ready instances
	MinPoolSize        int           // Minimum before urgent replenishment
	MaxPoolSize        int           // Hard cap on total instances
	ReplenishThreshold float64       // Trigger when pool < threshold * target
	ReplenishInterval  time.Duration // How often to check pool levels
	DefaultTTL         time.Duration // Default TTL for assigned instances
	MaxTTL             time.Duration // Maximum allowed TTL
}

// DefaultManagerConfig returns sensible defaults for development.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		TargetPoolSize:     10,
		MinPoolSize:        3,
		MaxPoolSize:        20,
		ReplenishThreshold: 0.5,
		ReplenishInterval:  10 * time.Second,
		DefaultTTL:         1 * time.Hour,
		MaxTTL:             24 * time.Hour,
	}
}
