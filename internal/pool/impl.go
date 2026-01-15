package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/boss/demo-multiplexer/internal/container"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/boss/demo-multiplexer/internal/metrics"
	"github.com/boss/demo-multiplexer/internal/proxy"
	"github.com/boss/demo-multiplexer/internal/store"
	"github.com/boss/demo-multiplexer/pkg/logging"
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
	cfg     ManagerConfig
	repo    store.Repository
	runtime container.Runtime
	proxy   proxy.RouteManager
	logger  *logging.Logger
	metrics *metrics.Collector

	stopCh  chan struct{}
	doneCh  chan struct{}
	mu      sync.Mutex
	running bool

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
	logger *logging.Logger,
	m *metrics.Collector,
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
		logger:         logger.With("component", "pool"),
		metrics:        m,
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
		m.logger.Warn("Failed to set TTL for instance", "instanceID", instance.ID, "error", err)
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
		m.logger.Warn("Failed to add route for instance", "instanceID", instance.ID, "error", err)
	}

	// Increment acquisition counter
	if err := m.repo.IncrementCounter(ctx, "acquisitions"); err != nil {
		m.logger.Warn("Failed to increment counter", "error", err)
	}

	// Trigger async replenishment check
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.TriggerReplenish(ctx); err != nil {
			m.logger.Warn("Replenishment check failed", "error", err)
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
		m.logger.Warn("Failed to remove route for instance", "instanceID", instanceID, "error", err)
	}

	// Stop container
	if err := m.runtime.Stop(ctx, instance.ContainerID); err != nil {
		m.logger.Warn("Failed to stop container for instance", "instanceID", instanceID, "error", err)
	}

	// Release port
	if err := m.repo.ReleasePort(ctx, instance.Port); err != nil {
		m.logger.Warn("Failed to release port", "port", instance.Port, "error", err)
	}

	// Delete instance from store
	if err := m.repo.DeleteInstance(ctx, instanceID); err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	// Record release metric
	if m.metrics != nil {
		m.metrics.ReleasesTotal.WithLabelValues("manual").Inc()
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

	m.logger.Info("Replenishing pool", "needed", needed, "ready", stats.Ready, "target", stats.Target)

	// Provision instances in parallel (but not more than capacity allows)
	for i := 0; i < needed; i++ {
		if err := m.provisionInstance(ctx); err != nil {
			m.logger.Warn("Failed to provision instance", "error", err)
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
		m.logger.Warn("Initial replenishment failed", "error", err)
	}

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.TriggerReplenish(ctx); err != nil {
				m.logger.Warn("Replenishment check failed", "error", err)
			}

			// Also clean up expired instances
			if err := m.cleanupExpired(ctx); err != nil {
				m.logger.Warn("Cleanup expired failed", "error", err)
			}
		}
	}
}

// provisionInstance creates a new instance and adds it to the pool.
// It first attempts CRIU restore if available, falling back to cold start.
func (m *PoolManager) provisionInstance(ctx context.Context) error {
	start := time.Now()
	method := "cold_start"

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
		criuStart := time.Now()
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
			m.logger.Warn("CRIU restore failed, falling back to Start()", "error", err)
			instance = nil // Ensure we fall through to Start()
		} else {
			method = "criu_restore"
			if m.metrics != nil {
				m.metrics.CRIURestoreDuration.Observe(time.Since(criuStart).Seconds())
			}
			m.logger.Info("Restored instance from checkpoint", "instanceID", instance.ID, "port", port)
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
			if m.metrics != nil {
				m.metrics.ProvisionsTotal.WithLabelValues(method, "failure").Inc()
			}
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
		if m.metrics != nil {
			m.metrics.ProvisionsTotal.WithLabelValues(method, "failure").Inc()
		}
		return fmt.Errorf("container failed health check: %w", err)
	}

	// Update state to ready
	instance.State = domain.StateReady

	// Add to pool
	if err := m.repo.AddToPool(ctx, instance); err != nil {
		// Clean up on failure
		_ = m.runtime.Stop(ctx, instance.ContainerID)
		_ = m.repo.ReleasePort(ctx, port)
		if m.metrics != nil {
			m.metrics.ProvisionsTotal.WithLabelValues(method, "failure").Inc()
		}
		return fmt.Errorf("failed to add to pool: %w", err)
	}

	// Record success metrics
	if m.metrics != nil {
		m.metrics.ProvisionsTotal.WithLabelValues(method, "success").Inc()
		m.metrics.ProvisionDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
	}

	m.logger.Info("Provisioned new instance", "instanceID", instance.ID, "port", port)
	return nil
}

// waitForReady waits for a container to pass health checks.
// It includes a brief initial delay to allow the container process to start,
// then polls at 1-second intervals until the container responds.
func (m *PoolManager) waitForReady(ctx context.Context, instance *domain.Instance) error {
	start := time.Now()

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
			checkStart := time.Now()
			healthy, err := m.runtime.HealthCheck(ctx, instance.ContainerID)
			if m.metrics != nil {
				m.metrics.HealthCheckDuration.Observe(time.Since(checkStart).Seconds())
			}
			if err != nil {
				if m.metrics != nil {
					m.metrics.HealthChecksTotal.WithLabelValues("error").Inc()
				}
				m.logger.Warn("Health check error", "instanceID", instance.ID, "attempt", attempt, "error", err)
				continue
			}
			if healthy {
				if m.metrics != nil {
					m.metrics.HealthChecksTotal.WithLabelValues("healthy").Inc()
					m.metrics.WaitForReadyDuration.Observe(time.Since(start).Seconds())
				}
				m.logger.Info("Container ready", "instanceID", instance.ID, "attempts", attempt)
				return nil
			}
			if m.metrics != nil {
				m.metrics.HealthChecksTotal.WithLabelValues("unhealthy").Inc()
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
		m.logger.Info("Cleaning up expired instance", "instanceID", instance.ID)
		if err := m.Release(ctx, instance.ID); err != nil {
			m.logger.Warn("Failed to release expired instance", "instanceID", instance.ID, "error", err)
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
