package pool

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/boss/demo-multiplexer/internal/container"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/boss/demo-multiplexer/internal/proxy"
	"github.com/boss/demo-multiplexer/internal/store"
)

// instanceCounter provides unique suffixes for hostname/DB prefix generation.
// Using atomic operations guarantees uniqueness across concurrent calls.
var instanceCounter uint64

// CRIURuntime is an optional interface for runtimes that support CRIU checkpoint/restore.
type CRIURuntime interface {
	CRIUAvailable() bool
}

// PoolManager implements the Manager interface.
type PoolManager struct {
	cfg      ManagerConfig
	repo     store.Repository
	runtime  container.Runtime
	proxy    proxy.RouteManager

	stopCh   chan struct{}
	doneCh   chan struct{}
	mu       sync.Mutex
	running  bool

	// Configuration from external configs
	containerImage string
	networkID      string
	baseDomain     string
	checkpointPath string // Path to CRIU checkpoint archive
}

// NewPoolManager creates a new pool manager.
func NewPoolManager(
	cfg ManagerConfig,
	repo store.Repository,
	runtime container.Runtime,
	proxyMgr proxy.RouteManager,
	containerImage string,
	networkID string,
	baseDomain string,
	checkpointPath string,
) *PoolManager {
	return &PoolManager{
		cfg:            cfg,
		repo:           repo,
		runtime:        runtime,
		proxy:          proxyMgr,
		containerImage: containerImage,
		networkID:      networkID,
		baseDomain:     baseDomain,
		checkpointPath: checkpointPath,
	}
}

// Acquire gets an instance from the warm pool.
func (m *PoolManager) Acquire(ctx context.Context) (*domain.Instance, error) {
	// Atomically acquire from pool
	instance, err := m.repo.AcquireFromPool(ctx)
	if err != nil {
		return nil, err
	}

	// Set TTL
	if err := m.repo.SetInstanceTTL(ctx, instance.ID, m.cfg.DefaultTTL); err != nil {
		// Log but don't fail - instance is already assigned
		log.Printf("Warning: failed to set TTL for instance %s: %v", instance.ID, err)
	} else {
		// Update in-memory instance to reflect the TTL we just set
		expiresAt := time.Now().Add(m.cfg.DefaultTTL)
		instance.ExpiresAt = &expiresAt
	}

	// Add route to proxy
	route := proxy.Route{
		Hostname:    instance.Hostname,
		UpstreamURL: fmt.Sprintf("http://localhost:%d", instance.Port),
		InstanceID:  instance.ID,
	}
	if err := m.proxy.AddRoute(ctx, route); err != nil {
		// Log but don't fail - instance can still be used directly
		log.Printf("Warning: failed to add route for instance %s: %v", instance.ID, err)
	}

	// Increment acquisition counter
	if err := m.repo.IncrementCounter(ctx, "acquisitions"); err != nil {
		log.Printf("Warning: failed to increment counter: %v", err)
	}

	// Trigger async replenishment check
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.TriggerReplenish(ctx); err != nil {
			log.Printf("Warning: replenishment check failed: %v", err)
		}
	}()

	return instance, nil
}

// Release returns an instance to be cleaned up.
func (m *PoolManager) Release(ctx context.Context, instanceID string) error {
	instance, err := m.repo.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}

	// Remove route from proxy
	if err := m.proxy.RemoveRoute(ctx, instance.Hostname); err != nil {
		log.Printf("Warning: failed to remove route for instance %s: %v", instanceID, err)
	}

	// Stop container
	if err := m.runtime.Stop(ctx, instance.ContainerID); err != nil {
		log.Printf("Warning: failed to stop container for instance %s: %v", instanceID, err)
	}

	// Release port
	if err := m.repo.ReleasePort(ctx, instance.Port); err != nil {
		log.Printf("Warning: failed to release port %d: %v", instance.Port, err)
	}

	// Delete instance from store
	if err := m.repo.DeleteInstance(ctx, instanceID); err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	return nil
}

// Stats returns current pool statistics.
func (m *PoolManager) Stats(ctx context.Context) (*domain.PoolStats, error) {
	stats, err := m.repo.GetPoolStats(ctx)
	if err != nil {
		return nil, err
	}

	// Fill in config values
	stats.Target = m.cfg.TargetPoolSize
	stats.Capacity = m.cfg.MaxPoolSize

	return stats, nil
}

// StartReplenisher starts the background replenishment loop.
func (m *PoolManager) StartReplenisher(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("replenisher already running")
	}
	m.stopCh = make(chan struct{})
	m.doneCh = make(chan struct{})
	m.running = true
	m.mu.Unlock()

	go m.replenishLoop(ctx)

	return nil
}

// StopReplenisher stops the background replenishment loop.
func (m *PoolManager) StopReplenisher() error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	close(m.stopCh)
	m.running = false
	m.mu.Unlock()

	// Wait for loop to finish
	<-m.doneCh
	return nil
}

// TriggerReplenish manually triggers a replenishment check.
func (m *PoolManager) TriggerReplenish(ctx context.Context) error {
	stats, err := m.Stats(ctx)
	if err != nil {
		return err
	}

	needed := stats.ReplenishmentNeeded()
	if needed <= 0 {
		return nil
	}

	log.Printf("Replenishing pool: %d instances needed (ready=%d, target=%d)",
		needed, stats.Ready, stats.Target)

	// Provision instances in parallel (but not more than capacity allows)
	for i := 0; i < needed; i++ {
		if err := m.provisionInstance(ctx); err != nil {
			log.Printf("Failed to provision instance: %v", err)
			// Continue trying to provision others
		}
	}

	return nil
}

// replenishLoop is the background loop that checks pool levels.
func (m *PoolManager) replenishLoop(ctx context.Context) {
	defer close(m.doneCh)

	ticker := time.NewTicker(m.cfg.ReplenishInterval)
	defer ticker.Stop()

	// Initial replenishment
	if err := m.TriggerReplenish(ctx); err != nil {
		log.Printf("Initial replenishment failed: %v", err)
	}

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.TriggerReplenish(ctx); err != nil {
				log.Printf("Replenishment check failed: %v", err)
			}

			// Also clean up expired instances
			if err := m.cleanupExpired(ctx); err != nil {
				log.Printf("Cleanup expired failed: %v", err)
			}
		}
	}
}

// provisionInstance creates a new instance and adds it to the pool.
// It first attempts CRIU restore if available, falling back to cold start.
func (m *PoolManager) provisionInstance(ctx context.Context) error {
	// Allocate port
	port, err := m.repo.AllocatePort(ctx)
	if err != nil {
		return fmt.Errorf("failed to allocate port: %w", err)
	}

	// Generate unique identifiers
	hostname := m.generateHostname()
	dbPrefix := m.generateDBPrefix()

	var instance *domain.Instance

	// Try CRIU restore first if runtime supports it
	if criuRuntime, ok := m.runtime.(CRIURuntime); ok && criuRuntime.CRIUAvailable() {
		restoreOpts := container.RestoreOptions{
			CheckpointPath: m.checkpointPath,
			Name:           "demo-" + hostname,
			Hostname:       hostname,
			Port:           port,
			DBPrefix:       dbPrefix,
			Labels: map[string]string{
				"app":      "demo-multiplexer",
				"hostname": hostname,
			},
		}

		instance, err = m.runtime.RestoreFromCheckpoint(ctx, restoreOpts)
		if err != nil {
			// Log and fall back to cold start
			log.Printf("CRIU restore failed, falling back to Start(): %v", err)
			instance = nil // Ensure we fall through to Start()
		} else {
			log.Printf("Restored instance from checkpoint: %s (port %d)", instance.ID, port)
		}
	}

	// Fallback: Start new container from image
	if instance == nil {
		opts := container.StartOptions{
			Image:     m.containerImage,
			Name:      "demo-" + hostname,
			Hostname:  hostname,
			Port:      port,
			DBPrefix:  dbPrefix,
			NetworkID: m.networkID,
			Labels: map[string]string{
				"app":      "demo-multiplexer",
				"hostname": hostname,
			},
		}

		instance, err = m.runtime.Start(ctx, opts)
		if err != nil {
			// Release the port we allocated
			_ = m.repo.ReleasePort(ctx, port)
			return fmt.Errorf("failed to start container: %w", err)
		}
	}

	instance.Hostname = hostname
	instance.DBPrefix = dbPrefix

	// Wait for container to be ready
	if err := m.waitForReady(ctx, instance); err != nil {
		// Clean up on failure
		_ = m.runtime.Stop(ctx, instance.ContainerID)
		_ = m.repo.ReleasePort(ctx, port)
		return fmt.Errorf("container failed health check: %w", err)
	}

	// Update state to ready
	instance.State = domain.StateReady

	// Add to pool
	if err := m.repo.AddToPool(ctx, instance); err != nil {
		// Clean up on failure
		_ = m.runtime.Stop(ctx, instance.ContainerID)
		_ = m.repo.ReleasePort(ctx, port)
		return fmt.Errorf("failed to add to pool: %w", err)
	}

	log.Printf("Provisioned new instance: %s (port %d)", instance.ID, port)
	return nil
}

// waitForReady waits for a container to pass health checks.
// It includes a brief initial delay to allow the container process to start,
// then polls at 1-second intervals until the container responds.
func (m *PoolManager) waitForReady(ctx context.Context, instance *domain.Instance) error {
	// Give container a moment to start its internal process (industry best practice: "start period")
	time.Sleep(1 * time.Second)

	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(1 * time.Second) // Reduced from 2s for faster detection
	defer ticker.Stop()

	attempt := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for container to be ready after %d attempts", attempt)
		case <-ticker.C:
			attempt++
			healthy, err := m.runtime.HealthCheck(ctx, instance.ContainerID)
			if err != nil {
				log.Printf("Health check error for %s (attempt %d): %v", instance.ID, attempt, err)
				continue
			}
			if healthy {
				log.Printf("Container %s ready after %d attempts", instance.ID, attempt)
				return nil
			}
		}
	}
}

// cleanupExpired removes expired instances.
func (m *PoolManager) cleanupExpired(ctx context.Context) error {
	expired, err := m.repo.ListExpired(ctx)
	if err != nil {
		return err
	}

	for _, instance := range expired {
		log.Printf("Cleaning up expired instance: %s", instance.ID)
		if err := m.Release(ctx, instance.ID); err != nil {
			log.Printf("Failed to release expired instance %s: %v", instance.ID, err)
		}
	}

	return nil
}

// generateHostname creates a unique hostname for an instance.
// Uses atomic counter combined with timestamp for guaranteed uniqueness.
func (m *PoolManager) generateHostname() string {
	n := atomic.AddUint64(&instanceCounter, 1)
	return fmt.Sprintf("demo-%d-%d", time.Now().Unix(), n)
}

// generateDBPrefix creates a unique database prefix.
// Uses atomic counter combined with timestamp for guaranteed uniqueness.
func (m *PoolManager) generateDBPrefix() string {
	n := atomic.AddUint64(&instanceCounter, 1)
	return fmt.Sprintf("d%d%d_", time.Now().Unix(), n)
}

// Compile-time check that PoolManager implements Manager
var _ Manager = (*PoolManager)(nil)
