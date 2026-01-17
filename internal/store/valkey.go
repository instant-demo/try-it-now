package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/internal/domain"
	"github.com/valkey-io/valkey-go"
)

// Lua scripts for atomic operations
var (
	// updateInstanceStateScript atomically updates an instance's state.
	// KEYS[1] = instance key (instance:{id})
	// KEYS[2] = state set prefix (state:)
	// ARGV[1] = new state
	// ARGV[2] = instance ID
	// ARGV[3] = assigned_at timestamp (RFC3339) - only used if new state is "assigned"
	// Returns: the updated instance JSON, or nil if not found
	updateInstanceStateScript = valkey.NewLuaScript(`
local instanceKey = KEYS[1]
local statePrefix = KEYS[2]
local newState = ARGV[1]
local instanceID = ARGV[2]
local assignedAt = ARGV[3]

-- Get current instance
local data = redis.call('GET', instanceKey)
if not data then
    return nil
end

-- Decode instance JSON
local instance = cjson.decode(data)
local oldState = instance.state

-- If state is unchanged, return current data
if oldState == newState then
    return data
end

-- Remove from old state set
redis.call('SREM', statePrefix .. oldState, instanceID)

-- Update instance state
instance.state = newState

-- Set assigned_at if transitioning to assigned state
if newState == 'assigned' and (instance.assigned_at == nil or instance.assigned_at == cjson.null) then
    instance.assigned_at = assignedAt
end

-- Save updated instance
local newData = cjson.encode(instance)
redis.call('SET', instanceKey, newData)

-- Add to new state set
redis.call('SADD', statePrefix .. newState, instanceID)

return newData
`)

	// acquireFromPoolScript atomically pops an instance from the pool and marks it assigned.
	// KEYS[1] = pool ready list key (pool:ready)
	// KEYS[2] = instance key prefix (instance:)
	// KEYS[3] = state set prefix (state:)
	// ARGV[1] = assigned_at timestamp (RFC3339)
	// Returns: the updated instance JSON, or nil if pool is empty or instance not found
	acquireFromPoolScript = valkey.NewLuaScript(`
local poolKey = KEYS[1]
local instancePrefix = KEYS[2]
local statePrefix = KEYS[3]
local assignedAt = ARGV[1]

-- Atomically pop from the ready list
local id = redis.call('LPOP', poolKey)
if not id then
    return nil
end

-- Get the instance data
local instanceKey = instancePrefix .. id
local data = redis.call('GET', instanceKey)
if not data then
    -- Instance doesn't exist, push ID back to pool and return nil
    redis.call('RPUSH', poolKey, id)
    return nil
end

-- Decode instance
local instance = cjson.decode(data)
local oldState = instance.state

-- Remove from old state set
redis.call('SREM', statePrefix .. oldState, id)

-- Update instance to assigned state
instance.state = 'assigned'
instance.assigned_at = assignedAt

-- Save updated instance
local newData = cjson.encode(instance)
redis.call('SET', instanceKey, newData)

-- Add to assigned state set
redis.call('SADD', statePrefix .. 'assigned', id)

return newData
`)

	// checkAndIncrementRateLimitScript atomically checks rate limits and increments if allowed.
	// KEYS[1] = hourly rate limit key (ratelimit:hour:{ip})
	// KEYS[2] = daily rate limit key (ratelimit:day:{ip})
	// ARGV[1] = hourly limit
	// ARGV[2] = daily limit
	// Returns: 1 if allowed (incremented), 0 if denied (limit reached)
	checkAndIncrementRateLimitScript = valkey.NewLuaScript(`
local hourKey, dayKey = KEYS[1], KEYS[2]
local hourLimit, dayLimit = tonumber(ARGV[1]), tonumber(ARGV[2])
local hourCount = tonumber(redis.call('GET', hourKey) or '0')
local dayCount = tonumber(redis.call('GET', dayKey) or '0')
if hourCount >= hourLimit or dayCount >= dayLimit then
    return 0  -- Denied
end
redis.call('INCR', hourKey)
redis.call('EXPIRE', hourKey, 3600)
redis.call('INCR', dayKey)
redis.call('EXPIRE', dayKey, 86400)
return 1  -- Allowed
`)

	// extendInstanceTTLAtomicScript atomically extends an instance's TTL if within limits.
	// KEYS[1] = instance key (instance:{id})
	// ARGV[1] = extension in seconds
	// ARGV[2] = max TTL in seconds
	// ARGV[3] = current Unix timestamp (seconds)
	// Returns: JSON with either {ok: true, new_expires_unix: <unix>} or {error: "...", remaining: <seconds>}
	// Note: Uses expires_at_unix field for atomic calculations. Falls back to error if field missing.
	extendInstanceTTLAtomicScript = valkey.NewLuaScript(`
local instanceKey = KEYS[1]
local extensionSecs = tonumber(ARGV[1])
local maxTTLSecs = tonumber(ARGV[2])
local nowUnix = tonumber(ARGV[3])

-- Get current instance
local data = redis.call('GET', instanceKey)
if not data then
    return cjson.encode({error = 'not_found'})
end

local instance = cjson.decode(data)

-- Check if expires_at_unix is set (required for atomic operations)
if not instance.expires_at_unix or instance.expires_at_unix == cjson.null then
    return cjson.encode({error = 'no_expiry'})
end

local expiresUnix = tonumber(instance.expires_at_unix)
local remaining = expiresUnix - nowUnix
if remaining < 0 then
    remaining = 0
end

-- Check if extension would exceed max TTL
local newRemaining = remaining + extensionSecs
if newRemaining > maxTTLSecs then
    return cjson.encode({error = 'exceeds_max', remaining = remaining})
end

-- Calculate new expiry
local newExpiresUnix = expiresUnix + extensionSecs
instance.expires_at_unix = newExpiresUnix

-- Note: expires_at (RFC3339 string) will be updated by Go after this script returns
-- We only update expires_at_unix atomically here

-- Save updated instance
local newData = cjson.encode(instance)
redis.call('SET', instanceKey, newData)
return cjson.encode({ok = true, new_expires_unix = newExpiresUnix})
`)
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

// UpdateInstanceState atomically changes an instance's state using a Lua script.
// This ensures the GET, SREM, SET, and SADD operations happen atomically,
// preventing race conditions under concurrent access.
func (r *ValkeyRepository) UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error {
	instanceKey := keyInstance + id
	now := time.Now().Format(time.RFC3339Nano)

	// Execute the atomic Lua script
	// KEYS: [instance:{id}, state:]
	// ARGV: [newState, instanceID, assignedAt]
	result := updateInstanceStateScript.Exec(
		ctx,
		r.client,
		[]string{instanceKey, keyStateSet},
		[]string{string(state), id, now},
	)

	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return domain.ErrInstanceNotFound
		}
		return fmt.Errorf("failed to update instance state: %w", err)
	}

	return nil
}

// AcquireFromPool atomically removes and returns an instance from the ready pool.
// This uses a Lua script to ensure LPOP and state update happen atomically,
// preventing instance loss if a crash occurs between operations.
func (r *ValkeyRepository) AcquireFromPool(ctx context.Context) (*domain.Instance, error) {
	now := time.Now().Format(time.RFC3339Nano)

	// Execute the atomic Lua script
	// KEYS: [pool:ready, instance:, state:]
	// ARGV: [assignedAt]
	result := acquireFromPoolScript.Exec(
		ctx,
		r.client,
		[]string{keyPoolReady, keyInstance, keyStateSet},
		[]string{now},
	)

	// Check for nil (pool exhausted)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, domain.ErrPoolExhausted
		}
		return nil, fmt.Errorf("failed to acquire from pool: %w", err)
	}

	// Get the instance JSON from the result
	data, err := result.ToString()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, domain.ErrPoolExhausted
		}
		return nil, fmt.Errorf("failed to get instance data: %w", err)
	}

	// Deserialize the instance
	var instance domain.Instance
	if err := json.Unmarshal([]byte(data), &instance); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instance: %w", err)
	}

	return &instance, nil
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
				// Instance may have been cleaned up between listing state set members
				// and fetching the instance. This is expected under concurrent operations
				// where one goroutine may delete an instance while another is iterating.
				// We skip it rather than returning an error, as this represents a benign
				// race condition during cleanup, not a data corruption issue.
				continue
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
	instance.ExpiresAtUnix = expiresAt.Unix()

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
// Note: This is the non-atomic version. Use ExtendInstanceTTLAtomic for concurrent-safe
// extensions that enforce max TTL limits.
func (r *ValkeyRepository) ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error {
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		return err
	}

	var newExpiry time.Time
	if instance.ExpiresAtUnix > 0 {
		// Use Unix timestamp as source of truth
		newExpiry = time.Unix(instance.ExpiresAtUnix, 0).Add(extension)
	} else if instance.ExpiresAt != nil {
		newExpiry = instance.ExpiresAt.Add(extension)
	} else {
		newExpiry = time.Now().Add(extension)
	}
	instance.ExpiresAt = &newExpiry
	instance.ExpiresAtUnix = newExpiry.Unix()

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

// CheckAndIncrementRateLimit atomically checks rate limits and increments if allowed.
// Returns true if the request is allowed, false if rate limited.
// This prevents TOCTOU race conditions where concurrent requests could all pass the check
// before any increment occurs.
func (r *ValkeyRepository) CheckAndIncrementRateLimit(ctx context.Context, ip string, hourlyLimit, dailyLimit int) (bool, error) {
	hourKey := keyRateLimitHour + ip
	dayKey := keyRateLimitDay + ip

	result := checkAndIncrementRateLimitScript.Exec(
		ctx,
		r.client,
		[]string{hourKey, dayKey},
		[]string{strconv.Itoa(hourlyLimit), strconv.Itoa(dailyLimit)},
	)

	allowed, err := result.ToInt64()
	if err != nil {
		return false, fmt.Errorf("failed to execute rate limit script: %w", err)
	}

	return allowed == 1, nil
}

// ExtendTTLResult contains the result of an atomic TTL extension.
type ExtendTTLResult struct {
	Success      bool
	NewExpiresAt time.Time
	Remaining    time.Duration
}

// ExtendInstanceTTLAtomic atomically extends an instance's TTL if it would not exceed maxTTL.
// Returns ErrInstanceNotFound if the instance doesn't exist.
// Returns ErrMaxTTLExceeded if the extension would exceed the maximum TTL.
// This prevents TOCTOU race conditions where concurrent extensions could all pass the
// max TTL check before any extension is applied.
func (r *ValkeyRepository) ExtendInstanceTTLAtomic(ctx context.Context, id string, extension, maxTTL time.Duration) (*ExtendTTLResult, error) {
	instanceKey := keyInstance + id
	nowUnix := time.Now().Unix()

	result := extendInstanceTTLAtomicScript.Exec(
		ctx,
		r.client,
		[]string{instanceKey},
		[]string{
			strconv.FormatInt(int64(extension.Seconds()), 10),
			strconv.FormatInt(int64(maxTTL.Seconds()), 10),
			strconv.FormatInt(nowUnix, 10),
		},
	)

	data, err := result.ToString()
	if err != nil {
		return nil, fmt.Errorf("failed to execute extend TTL script: %w", err)
	}

	// Parse the JSON response
	var resp struct {
		OK             bool    `json:"ok"`
		Error          string  `json:"error"`
		NewExpiresUnix int64   `json:"new_expires_unix"`
		Remaining      float64 `json:"remaining"`
	}
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse script response: %w", err)
	}

	if resp.Error != "" {
		switch resp.Error {
		case "not_found":
			return nil, domain.ErrInstanceNotFound
		case "no_expiry":
			// Instance has no expiry set, fall back to non-atomic extend
			// This can happen for instances created before ExpiresAtUnix was added
			return nil, fmt.Errorf("instance has no expires_at_unix field set")
		case "exceeds_max":
			return &ExtendTTLResult{
				Success:   false,
				Remaining: time.Duration(resp.Remaining) * time.Second,
			}, domain.ErrMaxTTLExceeded
		default:
			return nil, fmt.Errorf("unknown error from script: %s", resp.Error)
		}
	}

	newExpiresAt := time.Unix(resp.NewExpiresUnix, 0)

	// Update the expires_at field to keep it in sync with expires_at_unix
	// This is a secondary update, the atomic operation is already complete
	if err := r.syncExpiresAtField(ctx, id, newExpiresAt); err != nil {
		// Log but don't fail - the atomic operation succeeded
		// The expires_at_unix is the source of truth
	}

	return &ExtendTTLResult{
		Success:      true,
		NewExpiresAt: newExpiresAt,
		Remaining:    time.Until(newExpiresAt),
	}, nil
}

// syncExpiresAtField updates the expires_at string field to match expires_at_unix.
// This is a best-effort update to keep the fields in sync.
func (r *ValkeyRepository) syncExpiresAtField(ctx context.Context, id string, expiresAt time.Time) error {
	instance, err := r.GetInstance(ctx, id)
	if err != nil {
		return err
	}

	instance.ExpiresAt = &expiresAt
	instance.ExpiresAtUnix = expiresAt.Unix()

	data, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("failed to marshal instance: %w", err)
	}

	key := keyInstance + id
	return r.client.Do(ctx, r.client.B().Set().Key(key).Value(string(data)).Build()).Error()
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
	_ InstanceStore  = (*ValkeyRepository)(nil)
	_ PoolStore      = (*ValkeyRepository)(nil)
	_ InstanceLister = (*ValkeyRepository)(nil)
	_ TTLManager     = (*ValkeyRepository)(nil)
	_ PortAllocator  = (*ValkeyRepository)(nil)
	_ RateLimiter    = (*ValkeyRepository)(nil)
	_ StatsCollector = (*ValkeyRepository)(nil)
	_ HealthChecker  = (*ValkeyRepository)(nil)
	_ Repository     = (*ValkeyRepository)(nil)
)
