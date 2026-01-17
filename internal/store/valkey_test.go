package store

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/internal/domain"
)

// skipIfNoValkey skips the test if Valkey is not available.
func skipIfNoValkey(t *testing.T) *ValkeyRepository {
	t.Helper()
	if os.Getenv("VALKEY_TEST") == "" {
		t.Skip("Skipping Valkey integration test. Set VALKEY_TEST=1 to run.")
	}

	storeCfg := &config.StoreConfig{
		ValkeyAddr: getEnvOrDefault("VALKEY_ADDR", "localhost:6379"),
		Password:   os.Getenv("VALKEY_PASSWORD"),
		DB:         0,
	}
	containerCfg := &config.ContainerConfig{
		PortRangeStart: 32000,
		PortRangeEnd:   32099, // Smaller range for tests
	}

	repo, err := NewValkeyRepository(storeCfg, containerCfg)
	if err != nil {
		t.Skipf("Failed to connect to Valkey: %v", err)
	}

	// Clean up test data before each test
	ctx := context.Background()
	cleanupTestData(ctx, repo)

	return repo
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func cleanupTestData(ctx context.Context, repo *ValkeyRepository) {
	// Delete all test keys - use SCAN in production, but for tests we'll use known patterns
	patterns := []string{
		"instance:*",
		"pool:*",
		"state:*",
		"ports:*",
		"ratelimit:*",
		"counter:*",
	}
	for _, pattern := range patterns {
		keys, _ := repo.client.Do(ctx, repo.client.B().Keys().Pattern(pattern).Build()).AsStrSlice()
		for _, key := range keys {
			repo.client.Do(ctx, repo.client.B().Del().Key(key).Build())
		}
	}
}

func TestValkeyRepository_Ping(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()
	if err := repo.Ping(ctx); err != nil {
		t.Errorf("Ping() error = %v", err)
	}
}

func TestValkeyRepository_SaveAndGetInstance(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	instance := &domain.Instance{
		ID:          "test-instance-1",
		ContainerID: "container-abc123",
		Hostname:    "demo-test1",
		Port:        32000,
		State:       domain.StateReady,
		DBPrefix:    "test1_",
		CreatedAt:   time.Now(),
	}

	// Save
	if err := repo.SaveInstance(ctx, instance); err != nil {
		t.Fatalf("SaveInstance() error = %v", err)
	}

	// Get
	got, err := repo.GetInstance(ctx, instance.ID)
	if err != nil {
		t.Fatalf("GetInstance() error = %v", err)
	}

	if got.ID != instance.ID {
		t.Errorf("GetInstance() ID = %v, want %v", got.ID, instance.ID)
	}
	if got.ContainerID != instance.ContainerID {
		t.Errorf("GetInstance() ContainerID = %v, want %v", got.ContainerID, instance.ContainerID)
	}
	if got.State != instance.State {
		t.Errorf("GetInstance() State = %v, want %v", got.State, instance.State)
	}
}

func TestValkeyRepository_GetInstance_NotFound(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	_, err := repo.GetInstance(ctx, "nonexistent-instance")
	if err != domain.ErrInstanceNotFound {
		t.Errorf("GetInstance() error = %v, want %v", err, domain.ErrInstanceNotFound)
	}
}

func TestValkeyRepository_DeleteInstance(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	instance := &domain.Instance{
		ID:        "test-delete-1",
		Hostname:  "demo-delete",
		Port:      32001,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}

	if err := repo.SaveInstance(ctx, instance); err != nil {
		t.Fatalf("SaveInstance() error = %v", err)
	}

	if err := repo.DeleteInstance(ctx, instance.ID); err != nil {
		t.Fatalf("DeleteInstance() error = %v", err)
	}

	_, err := repo.GetInstance(ctx, instance.ID)
	if err != domain.ErrInstanceNotFound {
		t.Errorf("GetInstance() after delete error = %v, want %v", err, domain.ErrInstanceNotFound)
	}
}

func TestValkeyRepository_UpdateInstanceState(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	instance := &domain.Instance{
		ID:        "test-state-1",
		Hostname:  "demo-state",
		Port:      32002,
		State:     domain.StateWarming,
		CreatedAt: time.Now(),
	}

	if err := repo.SaveInstance(ctx, instance); err != nil {
		t.Fatalf("SaveInstance() error = %v", err)
	}

	// Update to ready
	if err := repo.UpdateInstanceState(ctx, instance.ID, domain.StateReady); err != nil {
		t.Fatalf("UpdateInstanceState() error = %v", err)
	}

	got, err := repo.GetInstance(ctx, instance.ID)
	if err != nil {
		t.Fatalf("GetInstance() error = %v", err)
	}

	if got.State != domain.StateReady {
		t.Errorf("State = %v, want %v", got.State, domain.StateReady)
	}

	// Update to assigned should set AssignedAt
	if err := repo.UpdateInstanceState(ctx, instance.ID, domain.StateAssigned); err != nil {
		t.Fatalf("UpdateInstanceState() error = %v", err)
	}

	got, err = repo.GetInstance(ctx, instance.ID)
	if err != nil {
		t.Fatalf("GetInstance() error = %v", err)
	}

	if got.State != domain.StateAssigned {
		t.Errorf("State = %v, want %v", got.State, domain.StateAssigned)
	}
	if got.AssignedAt == nil {
		t.Error("AssignedAt should be set when state changes to assigned")
	}
}

func TestValkeyRepository_PoolOperations(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	// Add instances to pool
	instance1 := &domain.Instance{
		ID:        "pool-test-1",
		Hostname:  "demo-pool1",
		Port:      32010,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}
	instance2 := &domain.Instance{
		ID:        "pool-test-2",
		Hostname:  "demo-pool2",
		Port:      32011,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}

	if err := repo.AddToPool(ctx, instance1); err != nil {
		t.Fatalf("AddToPool(1) error = %v", err)
	}
	if err := repo.AddToPool(ctx, instance2); err != nil {
		t.Fatalf("AddToPool(2) error = %v", err)
	}

	// Acquire should return in FIFO order
	got1, err := repo.AcquireFromPool(ctx)
	if err != nil {
		t.Fatalf("AcquireFromPool() error = %v", err)
	}
	if got1.ID != instance1.ID {
		t.Errorf("First acquired ID = %v, want %v", got1.ID, instance1.ID)
	}
	if got1.State != domain.StateAssigned {
		t.Errorf("Acquired state = %v, want %v", got1.State, domain.StateAssigned)
	}

	got2, err := repo.AcquireFromPool(ctx)
	if err != nil {
		t.Fatalf("AcquireFromPool() error = %v", err)
	}
	if got2.ID != instance2.ID {
		t.Errorf("Second acquired ID = %v, want %v", got2.ID, instance2.ID)
	}

	// Pool should be exhausted now
	_, err = repo.AcquireFromPool(ctx)
	if err != domain.ErrPoolExhausted {
		t.Errorf("AcquireFromPool() on empty pool error = %v, want %v", err, domain.ErrPoolExhausted)
	}
}

func TestValkeyRepository_ListByState(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	// Create instances in different states
	warming := &domain.Instance{
		ID:        "list-warming-1",
		Hostname:  "demo-warming",
		Port:      32020,
		State:     domain.StateWarming,
		CreatedAt: time.Now(),
	}
	ready := &domain.Instance{
		ID:        "list-ready-1",
		Hostname:  "demo-ready",
		Port:      32021,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}

	if err := repo.SaveInstance(ctx, warming); err != nil {
		t.Fatalf("SaveInstance(warming) error = %v", err)
	}
	if err := repo.SaveInstance(ctx, ready); err != nil {
		t.Fatalf("SaveInstance(ready) error = %v", err)
	}

	// List warming instances
	warmingList, err := repo.ListByState(ctx, domain.StateWarming)
	if err != nil {
		t.Fatalf("ListByState(warming) error = %v", err)
	}
	if len(warmingList) != 1 {
		t.Errorf("ListByState(warming) len = %d, want 1", len(warmingList))
	}
	if len(warmingList) > 0 && warmingList[0].ID != warming.ID {
		t.Errorf("ListByState(warming)[0].ID = %v, want %v", warmingList[0].ID, warming.ID)
	}
}

func TestValkeyRepository_TTLOperations(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	instance := &domain.Instance{
		ID:        "ttl-test-1",
		Hostname:  "demo-ttl",
		Port:      32030,
		State:     domain.StateAssigned,
		CreatedAt: time.Now(),
	}

	if err := repo.SaveInstance(ctx, instance); err != nil {
		t.Fatalf("SaveInstance() error = %v", err)
	}

	// Set TTL
	if err := repo.SetInstanceTTL(ctx, instance.ID, time.Hour); err != nil {
		t.Fatalf("SetInstanceTTL() error = %v", err)
	}

	// Get TTL
	ttl, err := repo.GetInstanceTTL(ctx, instance.ID)
	if err != nil {
		t.Fatalf("GetInstanceTTL() error = %v", err)
	}
	if ttl < 59*time.Minute || ttl > time.Hour {
		t.Errorf("GetInstanceTTL() = %v, want ~1h", ttl)
	}

	// Extend TTL
	if err := repo.ExtendInstanceTTL(ctx, instance.ID, 30*time.Minute); err != nil {
		t.Fatalf("ExtendInstanceTTL() error = %v", err)
	}

	ttl, err = repo.GetInstanceTTL(ctx, instance.ID)
	if err != nil {
		t.Fatalf("GetInstanceTTL() after extend error = %v", err)
	}
	if ttl < 89*time.Minute || ttl > 100*time.Minute {
		t.Errorf("GetInstanceTTL() after extend = %v, want ~1.5h", ttl)
	}
}

func TestValkeyRepository_PortAllocation(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	// Initialize ports
	if err := repo.InitializePorts(ctx); err != nil {
		t.Fatalf("InitializePorts() error = %v", err)
	}

	// Allocate a port
	port1, err := repo.AllocatePort(ctx)
	if err != nil {
		t.Fatalf("AllocatePort() error = %v", err)
	}
	if port1 < 32000 || port1 > 32099 {
		t.Errorf("AllocatePort() = %d, want in range [32000, 32099]", port1)
	}

	// Allocate another
	port2, err := repo.AllocatePort(ctx)
	if err != nil {
		t.Fatalf("AllocatePort() second error = %v", err)
	}
	if port1 == port2 {
		t.Errorf("AllocatePort() returned same port twice: %d", port1)
	}

	// Release first port
	if err := repo.ReleasePort(ctx, port1); err != nil {
		t.Fatalf("ReleasePort() error = %v", err)
	}

	// Should be able to allocate it again (eventually)
	found := false
	for i := 0; i < 100; i++ {
		port, err := repo.AllocatePort(ctx)
		if err != nil {
			break
		}
		if port == port1 {
			found = true
			break
		}
		repo.ReleasePort(ctx, port)
	}
	if !found {
		t.Log("Released port wasn't re-allocated in 100 tries (possible but unlikely)")
	}
}

func TestValkeyRepository_RateLimiting(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	testIP := "192.168.1.100"

	// Check rate limit - should be allowed initially
	allowed, err := repo.CheckRateLimit(ctx, testIP, 2, 5)
	if err != nil {
		t.Fatalf("CheckRateLimit() error = %v", err)
	}
	if !allowed {
		t.Error("CheckRateLimit() should allow initially")
	}

	// Increment twice
	if err := repo.IncrementRateLimit(ctx, testIP); err != nil {
		t.Fatalf("IncrementRateLimit() error = %v", err)
	}
	if err := repo.IncrementRateLimit(ctx, testIP); err != nil {
		t.Fatalf("IncrementRateLimit() second error = %v", err)
	}

	// Should be blocked now (hourly limit = 2)
	allowed, err = repo.CheckRateLimit(ctx, testIP, 2, 5)
	if err != nil {
		t.Fatalf("CheckRateLimit() after increments error = %v", err)
	}
	if allowed {
		t.Error("CheckRateLimit() should block after reaching hourly limit")
	}
}

func TestValkeyRepository_GetPoolStats(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	// Create instances in different states
	warming := &domain.Instance{
		ID:        "stats-warming-1",
		Hostname:  "demo-stats-w",
		Port:      32040,
		State:     domain.StateWarming,
		CreatedAt: time.Now(),
	}
	ready1 := &domain.Instance{
		ID:        "stats-ready-1",
		Hostname:  "demo-stats-r1",
		Port:      32041,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}
	ready2 := &domain.Instance{
		ID:        "stats-ready-2",
		Hostname:  "demo-stats-r2",
		Port:      32042,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}

	if err := repo.SaveInstance(ctx, warming); err != nil {
		t.Fatalf("SaveInstance(warming) error = %v", err)
	}
	if err := repo.AddToPool(ctx, ready1); err != nil {
		t.Fatalf("AddToPool(ready1) error = %v", err)
	}
	if err := repo.AddToPool(ctx, ready2); err != nil {
		t.Fatalf("AddToPool(ready2) error = %v", err)
	}

	// Acquire one
	if _, err := repo.AcquireFromPool(ctx); err != nil {
		t.Fatalf("AcquireFromPool() error = %v", err)
	}

	stats, err := repo.GetPoolStats(ctx)
	if err != nil {
		t.Fatalf("GetPoolStats() error = %v", err)
	}

	if stats.Warming != 1 {
		t.Errorf("Stats.Warming = %d, want 1", stats.Warming)
	}
	if stats.Ready != 1 {
		t.Errorf("Stats.Ready = %d, want 1", stats.Ready)
	}
	if stats.Assigned != 1 {
		t.Errorf("Stats.Assigned = %d, want 1", stats.Assigned)
	}
}

func TestValkeyRepository_IncrementCounter(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	counterName := "test-acquisitions"

	// Increment multiple times
	for i := 0; i < 5; i++ {
		if err := repo.IncrementCounter(ctx, counterName); err != nil {
			t.Fatalf("IncrementCounter() error = %v", err)
		}
	}

	// Verify count
	key := keyCounter + counterName
	count, err := repo.client.Do(ctx, repo.client.B().Get().Key(key).Build()).AsInt64()
	if err != nil {
		t.Fatalf("Get counter error = %v", err)
	}
	if count != 5 {
		t.Errorf("Counter value = %d, want 5", count)
	}
}

func TestValkeyRepository_ListExpired(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	// Create expired instance
	expired := &domain.Instance{
		ID:        "expired-test-1",
		Hostname:  "demo-expired",
		Port:      32050,
		State:     domain.StateAssigned,
		CreatedAt: now,
		ExpiresAt: &past,
	}
	// Create non-expired instance
	active := &domain.Instance{
		ID:        "active-test-1",
		Hostname:  "demo-active",
		Port:      32051,
		State:     domain.StateAssigned,
		CreatedAt: now,
		ExpiresAt: &future,
	}

	if err := repo.SaveInstance(ctx, expired); err != nil {
		t.Fatalf("SaveInstance(expired) error = %v", err)
	}
	if err := repo.SaveInstance(ctx, active); err != nil {
		t.Fatalf("SaveInstance(active) error = %v", err)
	}

	expiredList, err := repo.ListExpired(ctx)
	if err != nil {
		t.Fatalf("ListExpired() error = %v", err)
	}

	if len(expiredList) != 1 {
		t.Errorf("ListExpired() len = %d, want 1", len(expiredList))
	}
	if len(expiredList) > 0 && expiredList[0].ID != expired.ID {
		t.Errorf("ListExpired()[0].ID = %v, want %v", expiredList[0].ID, expired.ID)
	}
}

// TestValkeyRepository_UpdateInstanceState_NotFound verifies the Lua script returns
// ErrInstanceNotFound for non-existent instances.
func TestValkeyRepository_UpdateInstanceState_NotFound(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	err := repo.UpdateInstanceState(ctx, "nonexistent-instance", domain.StateAssigned)
	if err != domain.ErrInstanceNotFound {
		t.Errorf("UpdateInstanceState() error = %v, want %v", err, domain.ErrInstanceNotFound)
	}
}

// TestValkeyRepository_UpdateInstanceState_SameState verifies no-op when state unchanged.
func TestValkeyRepository_UpdateInstanceState_SameState(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	instance := &domain.Instance{
		ID:        "same-state-test-1",
		Hostname:  "demo-same",
		Port:      32060,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}

	if err := repo.SaveInstance(ctx, instance); err != nil {
		t.Fatalf("SaveInstance() error = %v", err)
	}

	// Update to same state should succeed without error
	if err := repo.UpdateInstanceState(ctx, instance.ID, domain.StateReady); err != nil {
		t.Errorf("UpdateInstanceState() to same state error = %v, want nil", err)
	}

	// Verify instance is still in correct state set
	readyList, err := repo.ListByState(ctx, domain.StateReady)
	if err != nil {
		t.Fatalf("ListByState() error = %v", err)
	}

	found := false
	for _, inst := range readyList {
		if inst.ID == instance.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("Instance should still be in ready state set")
	}
}

// TestValkeyRepository_UpdateInstanceState_Concurrent tests that concurrent state updates
// don't corrupt the state sets. This verifies the atomicity fix for try-it-now-8jj.
func TestValkeyRepository_UpdateInstanceState_Concurrent(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	// Create multiple instances
	const numInstances = 10
	instances := make([]*domain.Instance, numInstances)
	for i := 0; i < numInstances; i++ {
		instances[i] = &domain.Instance{
			ID:        "concurrent-state-" + string(rune('a'+i)),
			Hostname:  "demo-concurrent",
			Port:      32070 + i,
			State:     domain.StateWarming,
			CreatedAt: time.Now(),
		}
		if err := repo.SaveInstance(ctx, instances[i]); err != nil {
			t.Fatalf("SaveInstance() error = %v", err)
		}
	}

	// Concurrently update all instances through multiple state transitions
	var wg sync.WaitGroup
	var errors atomic.Int32

	for _, inst := range instances {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			// Transition: warming -> ready -> assigned
			if err := repo.UpdateInstanceState(ctx, id, domain.StateReady); err != nil {
				t.Logf("UpdateInstanceState to ready error: %v", err)
				errors.Add(1)
				return
			}
			if err := repo.UpdateInstanceState(ctx, id, domain.StateAssigned); err != nil {
				t.Logf("UpdateInstanceState to assigned error: %v", err)
				errors.Add(1)
			}
		}(inst.ID)
	}

	wg.Wait()

	if errors.Load() > 0 {
		t.Fatalf("Got %d errors during concurrent updates", errors.Load())
	}

	// Verify all instances ended up in assigned state
	assignedList, err := repo.ListByState(ctx, domain.StateAssigned)
	if err != nil {
		t.Fatalf("ListByState(assigned) error = %v", err)
	}
	if len(assignedList) != numInstances {
		t.Errorf("ListByState(assigned) len = %d, want %d", len(assignedList), numInstances)
	}

	// Verify warming and ready sets are empty
	warmingList, err := repo.ListByState(ctx, domain.StateWarming)
	if err != nil {
		t.Fatalf("ListByState(warming) error = %v", err)
	}
	// Filter out instances from other tests
	warmingCount := 0
	for _, inst := range warmingList {
		for _, testInst := range instances {
			if inst.ID == testInst.ID {
				warmingCount++
			}
		}
	}
	if warmingCount != 0 {
		t.Errorf("Warming state set should have 0 test instances, got %d", warmingCount)
	}

	readyList, err := repo.ListByState(ctx, domain.StateReady)
	if err != nil {
		t.Fatalf("ListByState(ready) error = %v", err)
	}
	readyCount := 0
	for _, inst := range readyList {
		for _, testInst := range instances {
			if inst.ID == testInst.ID {
				readyCount++
			}
		}
	}
	if readyCount != 0 {
		t.Errorf("Ready state set should have 0 test instances, got %d", readyCount)
	}

	// Verify each instance has AssignedAt set
	for _, inst := range instances {
		got, err := repo.GetInstance(ctx, inst.ID)
		if err != nil {
			t.Fatalf("GetInstance(%s) error = %v", inst.ID, err)
		}
		if got.AssignedAt == nil {
			t.Errorf("Instance %s AssignedAt should be set", inst.ID)
		}
	}
}

// TestValkeyRepository_AcquireFromPool_Concurrent tests that concurrent acquires
// don't lose instances. This verifies the atomicity fix for try-it-now-4vf.
func TestValkeyRepository_AcquireFromPool_Concurrent(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	// Create and add instances to pool
	const numInstances = 20
	for i := 0; i < numInstances; i++ {
		instance := &domain.Instance{
			ID:        "pool-concurrent-" + string(rune('a'+i)),
			Hostname:  "demo-pool-concurrent",
			Port:      32100 + i,
			State:     domain.StateReady,
			CreatedAt: time.Now(),
		}
		if err := repo.AddToPool(ctx, instance); err != nil {
			t.Fatalf("AddToPool() error = %v", err)
		}
	}

	// Concurrently acquire all instances
	var wg sync.WaitGroup
	acquired := make(chan string, numInstances)
	var exhaustedCount atomic.Int32

	// Use more goroutines than instances to create contention
	const numGoroutines = 30
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inst, err := repo.AcquireFromPool(ctx)
			if err == domain.ErrPoolExhausted {
				exhaustedCount.Add(1)
				return
			}
			if err != nil {
				t.Logf("AcquireFromPool error: %v", err)
				return
			}
			acquired <- inst.ID
		}()
	}

	wg.Wait()
	close(acquired)

	// Collect all acquired instance IDs
	acquiredIDs := make(map[string]bool)
	for id := range acquired {
		if acquiredIDs[id] {
			t.Errorf("Instance %s was acquired more than once!", id)
		}
		acquiredIDs[id] = true
	}

	// Verify exactly numInstances were acquired
	if len(acquiredIDs) != numInstances {
		t.Errorf("Acquired %d instances, want %d", len(acquiredIDs), numInstances)
	}

	// Verify exhausted count is correct (should be numGoroutines - numInstances)
	expectedExhausted := int32(numGoroutines - numInstances)
	if exhaustedCount.Load() != expectedExhausted {
		t.Errorf("Got %d exhausted errors, want %d", exhaustedCount.Load(), expectedExhausted)
	}

	// Verify all acquired instances are in assigned state
	assignedList, err := repo.ListByState(ctx, domain.StateAssigned)
	if err != nil {
		t.Fatalf("ListByState(assigned) error = %v", err)
	}

	// Count only our test instances
	assignedCount := 0
	for _, inst := range assignedList {
		if acquiredIDs[inst.ID] {
			assignedCount++
			if inst.State != domain.StateAssigned {
				t.Errorf("Instance %s state = %v, want assigned", inst.ID, inst.State)
			}
			if inst.AssignedAt == nil {
				t.Errorf("Instance %s AssignedAt should be set", inst.ID)
			}
		}
	}
	if assignedCount != numInstances {
		t.Errorf("Found %d assigned test instances, want %d", assignedCount, numInstances)
	}

	// Verify pool is empty
	stats, err := repo.GetPoolStats(ctx)
	if err != nil {
		t.Fatalf("GetPoolStats() error = %v", err)
	}
	if stats.Ready != 0 {
		t.Errorf("Pool should be empty, got Ready = %d", stats.Ready)
	}
}

// TestValkeyRepository_AcquireFromPool_MissingInstance tests that the Lua script
// correctly handles the case where an instance ID is in the pool but the instance
// data doesn't exist (e.g., was deleted). The ID should be returned to the pool
// and the acquire returns pool exhausted (since the script returns nil).
func TestValkeyRepository_AcquireFromPool_MissingInstance(t *testing.T) {
	repo := skipIfNoValkey(t)
	defer repo.Close()

	ctx := context.Background()

	// Manually add an ID to the pool without creating the instance
	orphanID := "orphan-instance-id"
	if err := repo.client.Do(ctx, repo.client.B().Rpush().Key(keyPoolReady).Element(orphanID).Build()).Error(); err != nil {
		t.Fatalf("Failed to push orphan ID to pool: %v", err)
	}

	// First acquire should fail because the instance data doesn't exist
	// The Lua script will RPUSH the orphan ID back to the pool and return nil
	_, err := repo.AcquireFromPool(ctx)
	if err != domain.ErrPoolExhausted {
		t.Errorf("AcquireFromPool() for orphan should return ErrPoolExhausted, got %v", err)
	}

	// Verify the orphan ID was pushed back to the pool (still there)
	poolLen, err := repo.client.Do(ctx, repo.client.B().Llen().Key(keyPoolReady).Build()).ToInt64()
	if err != nil {
		t.Fatalf("Failed to get pool length: %v", err)
	}
	if poolLen != 1 {
		t.Errorf("Pool length = %d, want 1 (orphan should be pushed back)", poolLen)
	}

	// Now add a valid instance
	validInstance := &domain.Instance{
		ID:        "valid-after-orphan",
		Hostname:  "demo-valid",
		Port:      32150,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}
	if err := repo.AddToPool(ctx, validInstance); err != nil {
		t.Fatalf("AddToPool() error = %v", err)
	}

	// Next acquire should get the orphan (LPOP) which fails, pushes it back,
	// then we need to try again to get the valid one
	// Actually, each call is atomic - the first call will fail on orphan
	_, err = repo.AcquireFromPool(ctx)
	if err != domain.ErrPoolExhausted {
		t.Errorf("Second AcquireFromPool() should still fail on orphan, got %v", err)
	}

	// Third acquire - now the valid instance should be first (orphan at end)
	got, err := repo.AcquireFromPool(ctx)
	if err != nil {
		t.Fatalf("Third AcquireFromPool() error = %v", err)
	}
	if got.ID != validInstance.ID {
		t.Errorf("AcquireFromPool() ID = %v, want %v", got.ID, validInstance.ID)
	}
}
