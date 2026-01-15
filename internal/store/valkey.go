package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/valkey-io/valkey-go"
)

// Redis key prefixes and names
const (
	keyInstance      = "instance:"       // instance:{id} -> JSON
	keyPoolReady     = "pool:ready"      // list of ready instance IDs
	keyStateSet      = "state:"          // state:{state} -> set of instance IDs
	keyPorts         = "ports:available" // set of available ports
	keyPortsInUse    = "ports:in_use"    // set of ports in use
	keyRateLimitHour = "ratelimit:hour:" // ratelimit:hour:{ip} -> count
	keyRateLimitDay  = "ratelimit:day:"  // ratelimit:day:{ip} -> count
	keyCounter       = "counter:"        // counter:{name} -> int
)

// ValkeyRepository implements Repository using Valkey.
type ValkeyRepository struct {
	client    valkey.Client
	portStart int
	portEnd   int
}

// NewValkeyRepository creates a new Valkey-backed repository.
func NewValkeyRepository(cfg *config.StoreConfig, containerCfg *config.ContainerConfig) (*ValkeyRepository, error) {
	opts := valkey.ClientOption{
		InitAddress: []string{cfg.ValkeyAddr},
		SelectDB:    cfg.DB,
	}
	if cfg.Password != "" {
		opts.Password = cfg.Password
	}

	client, err := valkey.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create valkey client: %w", err)
	}

	repo := &ValkeyRepository{
		client:    client,
		portStart: containerCfg.PortRangeStart,
		portEnd:   containerCfg.PortRangeEnd,
	}

	return repo, nil
}

// Close closes the Valkey connection.
func (r *ValkeyRepository) Close() {
	r.client.Close()
}

// InitializePorts pre-populates the available ports set if empty.
func (r *ValkeyRepository) InitializePorts(ctx context.Context) error {
	// Check if ports are already initialized
	count, err := r.client.Do(ctx, r.client.B().Scard().Key(keyPorts).Build()).ToInt64()
	if err != nil && !valkey.IsValkeyNil(err) {
		return fmt.Errorf("failed to check ports set: %w", err)
	}
	if count > 0 {
		return nil // Already initialized
	}

	// Add all ports in range to available set
	members := make([]string, 0, r.portEnd-r.portStart+1)
	for port := r.portStart; port <= r.portEnd; port++ {
		members = append(members, strconv.Itoa(port))
	}

	if err := r.client.Do(ctx, r.client.B().Sadd().Key(keyPorts).Member(members...).Build()).Error(); err != nil {
		return fmt.Errorf("failed to initialize ports: %w", err)
	}
	return nil
}

// SaveInstance stores an instance in Valkey.
func (r *ValkeyRepository) SaveInstance(ctx context.Context, instance *domain.Instance) error {
	data, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal instance: %w", err)
	}

	key := keyInstance + instance.ID
	if err := r.client.Do(ctx, r.client.B().Set().Key(key).Value(string(data)).Build()).Error(); err != nil {
		return fmt.Errorf("failed to save instance: %w", err)
	}

	// Add to state set
	stateKey := keyStateSet + string(instance.State)
	if err := r.client.Do(ctx, r.client.B().Sadd().Key(stateKey).Member(instance.ID).Build()).Error(); err != nil {
		return fmt.Errorf("failed to add instance to state set: %w", err)
	}

	return nil
}

// GetInstance retrieves an instance by ID.
func (r *ValkeyRepository) GetInstance(ctx context.Context, id string) (*domain.Instance, error) {
	key := keyInstance + id
	data, err := r.client.Do(ctx, r.client.B().Get().Key(key).Build()).ToString()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, domain.ErrInstanceNotFound
		}
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	var instance domain.Instance
	if err := json.Unmarshal([]byte(data), &instance); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instance: %w", err)
	}

	return &instance, nil
}

// DeleteInstance removes an instance from Valkey.
func (r *ValkeyRepository) DeleteInstance(ctx context.Context, id string) error {
	// Get instance first to know its state
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceNotFound) {
			return nil // Already deleted
		}
		return err
	}

	// Remove from state set
	stateKey := keyStateSet + string(instance.State)
	if err := r.client.Do(ctx, r.client.B().Srem().Key(stateKey).Member(id).Build()).Error(); err != nil {
		return fmt.Errorf("failed to remove from state set: %w", err)
	}

	// Delete the instance key
	key := keyInstance + id
	if err := r.client.Do(ctx, r.client.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	return nil
}

// UpdateInstanceState changes an instance's state.
func (r *ValkeyRepository) UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error {
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		return err
	}

	oldState := instance.State
	if oldState == state {
		return nil // No change needed
	}

	// Remove from old state set
	oldStateKey := keyStateSet + string(oldState)
	if err := r.client.Do(ctx, r.client.B().Srem().Key(oldStateKey).Member(id).Build()).Error(); err != nil {
		return fmt.Errorf("failed to remove from old state set: %w", err)
	}

	// Update instance
	instance.State = state
	if state == domain.StateAssigned && instance.AssignedAt == nil {
		now := time.Now()
		instance.AssignedAt = &now
	}

	// Save updated instance
	data, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal instance: %w", err)
	}

	key := keyInstance + id
	if err := r.client.Do(ctx, r.client.B().Set().Key(key).Value(string(data)).Build()).Error(); err != nil {
		return fmt.Errorf("failed to save instance: %w", err)
	}

	// Add to new state set
	newStateKey := keyStateSet + string(state)
	if err := r.client.Do(ctx, r.client.B().Sadd().Key(newStateKey).Member(id).Build()).Error(); err != nil {
		return fmt.Errorf("failed to add to new state set: %w", err)
	}

	return nil
}

// AcquireFromPool atomically removes and returns an instance from the ready pool.
func (r *ValkeyRepository) AcquireFromPool(ctx context.Context) (*domain.Instance, error) {
	// Atomically pop from the ready list
	id, err := r.client.Do(ctx, r.client.B().Lpop().Key(keyPoolReady).Build()).ToString()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, domain.ErrPoolExhausted
		}
		return nil, fmt.Errorf("failed to pop from pool: %w", err)
	}

	// Update state to assigned first
	if err := r.UpdateInstanceState(ctx, id, domain.StateAssigned); err != nil {
		return nil, err
	}

	// Get the updated instance
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

// AddToPool adds an instance to the ready pool.
func (r *ValkeyRepository) AddToPool(ctx context.Context, instance *domain.Instance) error {
	// Ensure instance is in ready state
	if instance.State != domain.StateReady {
		instance.State = domain.StateReady
	}

	// Save instance first
	if err := r.SaveInstance(ctx, instance); err != nil {
		return err
	}

	// Add to ready pool list (at the end for FIFO)
	if err := r.client.Do(ctx, r.client.B().Rpush().Key(keyPoolReady).Element(instance.ID).Build()).Error(); err != nil {
		return fmt.Errorf("failed to add to pool: %w", err)
	}

	return nil
}

// RemoveFromPool removes an instance from the ready pool without acquiring it.
func (r *ValkeyRepository) RemoveFromPool(ctx context.Context, id string) error {
	// Remove from list (removes first occurrence)
	if err := r.client.Do(ctx, r.client.B().Lrem().Key(keyPoolReady).Count(1).Element(id).Build()).Error(); err != nil {
		return fmt.Errorf("failed to remove from pool: %w", err)
	}
	return nil
}

// ListByState returns all instances in a given state.
func (r *ValkeyRepository) ListByState(ctx context.Context, state domain.InstanceState) ([]*domain.Instance, error) {
	stateKey := keyStateSet + string(state)
	ids, err := r.client.Do(ctx, r.client.B().Smembers().Key(stateKey).Build()).AsStrSlice()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return []*domain.Instance{}, nil
		}
		return nil, fmt.Errorf("failed to get state members: %w", err)
	}

	instances := make([]*domain.Instance, 0, len(ids))
	for _, id := range ids {
		instance, err := r.GetInstance(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrInstanceNotFound) {
				continue // Instance was deleted, skip
			}
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, nil
}

// ListExpired returns all instances that have passed their expiry time.
func (r *ValkeyRepository) ListExpired(ctx context.Context) ([]*domain.Instance, error) {
	// Check assigned instances for expiry
	assigned, err := r.ListByState(ctx, domain.StateAssigned)
	if err != nil {
		return nil, err
	}

	expired := make([]*domain.Instance, 0)
	for _, instance := range assigned {
		if instance.IsExpired() {
			expired = append(expired, instance)
		}
	}

	return expired, nil
}

// SetInstanceTTL sets the expiry time for an instance.
func (r *ValkeyRepository) SetInstanceTTL(ctx context.Context, id string, ttl time.Duration) error {
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		return err
	}

	expiresAt := time.Now().Add(ttl)
	instance.ExpiresAt = &expiresAt

	data, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal instance: %w", err)
	}

	key := keyInstance + id
	if err := r.client.Do(ctx, r.client.B().Set().Key(key).Value(string(data)).Build()).Error(); err != nil {
		return fmt.Errorf("failed to save instance: %w", err)
	}

	return nil
}

// GetInstanceTTL returns the remaining TTL for an instance.
func (r *ValkeyRepository) GetInstanceTTL(ctx context.Context, id string) (time.Duration, error) {
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		return 0, err
	}

	return instance.TTLRemaining(), nil
}

// ExtendInstanceTTL extends the expiry time for an instance.
func (r *ValkeyRepository) ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error {
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		return err
	}

	var newExpiry time.Time
	if instance.ExpiresAt != nil {
		newExpiry = instance.ExpiresAt.Add(extension)
	} else {
		newExpiry = time.Now().Add(extension)
	}
	instance.ExpiresAt = &newExpiry

	data, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal instance: %w", err)
	}

	key := keyInstance + id
	if err := r.client.Do(ctx, r.client.B().Set().Key(key).Value(string(data)).Build()).Error(); err != nil {
		return fmt.Errorf("failed to save instance: %w", err)
	}

	return nil
}

// AllocatePort atomically allocates an available port.
func (r *ValkeyRepository) AllocatePort(ctx context.Context) (int, error) {
	// Atomically move a port from available to in-use
	portStr, err := r.client.Do(ctx, r.client.B().Spop().Key(keyPorts).Build()).ToString()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return 0, domain.ErrNoPortsAvailable
		}
		return 0, fmt.Errorf("failed to allocate port: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid port value: %w", err)
	}

	// Track in in-use set
	if err := r.client.Do(ctx, r.client.B().Sadd().Key(keyPortsInUse).Member(portStr).Build()).Error(); err != nil {
		return 0, fmt.Errorf("failed to track port in use: %w", err)
	}

	return port, nil
}

// ReleasePort returns a port to the available pool.
func (r *ValkeyRepository) ReleasePort(ctx context.Context, port int) error {
	portStr := strconv.Itoa(port)

	// Remove from in-use set
	if err := r.client.Do(ctx, r.client.B().Srem().Key(keyPortsInUse).Member(portStr).Build()).Error(); err != nil {
		return fmt.Errorf("failed to remove port from in-use: %w", err)
	}

	// Add back to available set
	if err := r.client.Do(ctx, r.client.B().Sadd().Key(keyPorts).Member(portStr).Build()).Error(); err != nil {
		return fmt.Errorf("failed to release port: %w", err)
	}

	return nil
}

// CheckRateLimit checks if an IP is within rate limits.
func (r *ValkeyRepository) CheckRateLimit(ctx context.Context, ip string, hourlyLimit, dailyLimit int) (bool, error) {
	hourKey := keyRateLimitHour + ip
	dayKey := keyRateLimitDay + ip

	// Check hourly limit
	// Use AsInt64 because GET returns blob string, not RESP3 integer
	hourCount, err := r.client.Do(ctx, r.client.B().Get().Key(hourKey).Build()).AsInt64()
	if err != nil && !valkey.IsValkeyNil(err) {
		return false, fmt.Errorf("failed to get hourly rate: %w", err)
	}
	if hourCount >= int64(hourlyLimit) {
		return false, nil
	}

	// Check daily limit
	dayCount, err := r.client.Do(ctx, r.client.B().Get().Key(dayKey).Build()).AsInt64()
	if err != nil && !valkey.IsValkeyNil(err) {
		return false, fmt.Errorf("failed to get daily rate: %w", err)
	}
	if dayCount >= int64(dailyLimit) {
		return false, nil
	}

	return true, nil
}

// IncrementRateLimit increments the rate limit counters for an IP.
func (r *ValkeyRepository) IncrementRateLimit(ctx context.Context, ip string) error {
	hourKey := keyRateLimitHour + ip
	dayKey := keyRateLimitDay + ip

	// Increment hourly counter with 1 hour expiry
	if err := r.client.Do(ctx, r.client.B().Incr().Key(hourKey).Build()).Error(); err != nil {
		return fmt.Errorf("failed to increment hourly rate: %w", err)
	}
	if err := r.client.Do(ctx, r.client.B().Expire().Key(hourKey).Seconds(3600).Build()).Error(); err != nil {
		return fmt.Errorf("failed to set hourly expiry: %w", err)
	}

	// Increment daily counter with 24 hour expiry
	if err := r.client.Do(ctx, r.client.B().Incr().Key(dayKey).Build()).Error(); err != nil {
		return fmt.Errorf("failed to increment daily rate: %w", err)
	}
	if err := r.client.Do(ctx, r.client.B().Expire().Key(dayKey).Seconds(86400).Build()).Error(); err != nil {
		return fmt.Errorf("failed to set daily expiry: %w", err)
	}

	return nil
}

// GetPoolStats returns current pool statistics.
func (r *ValkeyRepository) GetPoolStats(ctx context.Context) (*domain.PoolStats, error) {
	stats := &domain.PoolStats{}

	// Count ready instances (from list length)
	ready, err := r.client.Do(ctx, r.client.B().Llen().Key(keyPoolReady).Build()).ToInt64()
	if err != nil && !valkey.IsValkeyNil(err) {
		return nil, fmt.Errorf("failed to get ready count: %w", err)
	}
	stats.Ready = int(ready)

	// Count assigned instances
	assigned, err := r.client.Do(ctx, r.client.B().Scard().Key(keyStateSet+string(domain.StateAssigned)).Build()).ToInt64()
	if err != nil && !valkey.IsValkeyNil(err) {
		return nil, fmt.Errorf("failed to get assigned count: %w", err)
	}
	stats.Assigned = int(assigned)

	// Count warming instances
	warming, err := r.client.Do(ctx, r.client.B().Scard().Key(keyStateSet+string(domain.StateWarming)).Build()).ToInt64()
	if err != nil && !valkey.IsValkeyNil(err) {
		return nil, fmt.Errorf("failed to get warming count: %w", err)
	}
	stats.Warming = int(warming)

	return stats, nil
}

// IncrementCounter increments a named counter.
func (r *ValkeyRepository) IncrementCounter(ctx context.Context, name string) error {
	key := keyCounter + name
	if err := r.client.Do(ctx, r.client.B().Incr().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("failed to increment counter: %w", err)
	}
	return nil
}

// Ping checks the Valkey connection.
func (r *ValkeyRepository) Ping(ctx context.Context) error {
	if err := r.client.Do(ctx, r.client.B().Ping().Build()).Error(); err != nil {
		return fmt.Errorf("valkey ping failed: %w", err)
	}
	return nil
}

// Compile-time checks that ValkeyRepository implements all interfaces
var (
	_ InstanceStore   = (*ValkeyRepository)(nil)
	_ PoolStore       = (*ValkeyRepository)(nil)
	_ InstanceLister  = (*ValkeyRepository)(nil)
	_ TTLManager      = (*ValkeyRepository)(nil)
	_ PortAllocator   = (*ValkeyRepository)(nil)
	_ RateLimiter     = (*ValkeyRepository)(nil)
	_ StatsCollector  = (*ValkeyRepository)(nil)
	_ HealthChecker   = (*ValkeyRepository)(nil)
	_ Repository      = (*ValkeyRepository)(nil)
)
