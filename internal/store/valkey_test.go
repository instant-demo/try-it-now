package store

import (
	"context"
	"os"
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
