package pool

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/boss/demo-multiplexer/internal/container"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/boss/demo-multiplexer/internal/proxy"
	"github.com/boss/demo-multiplexer/pkg/logging"
)

// MockRepository implements store.Repository for testing.
type MockRepository struct {
	mu        sync.Mutex
	instances map[string]*domain.Instance
	pool      []string // IDs in ready pool
	ports     map[int]bool // true = in use
	counters  map[string]int64
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		instances: make(map[string]*domain.Instance),
		pool:      []string{},
		ports:     make(map[int]bool),
		counters:  make(map[string]int64),
	}
}

func (m *MockRepository) SaveInstance(ctx context.Context, instance *domain.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[instance.ID] = instance
	return nil
}

func (m *MockRepository) GetInstance(ctx context.Context, id string) (*domain.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		return inst, nil
	}
	return nil, domain.ErrInstanceNotFound
}

func (m *MockRepository) DeleteInstance(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.instances, id)
	return nil
}

func (m *MockRepository) UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		inst.State = state
		return nil
	}
	return domain.ErrInstanceNotFound
}

func (m *MockRepository) AcquireFromPool(ctx context.Context) (*domain.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pool) == 0 {
		return nil, domain.ErrPoolExhausted
	}
	id := m.pool[0]
	m.pool = m.pool[1:]
	inst := m.instances[id]
	inst.State = domain.StateAssigned
	now := time.Now()
	inst.AssignedAt = &now
	return inst, nil
}

func (m *MockRepository) AddToPool(ctx context.Context, instance *domain.Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[instance.ID] = instance
	m.pool = append(m.pool, instance.ID)
	return nil
}

func (m *MockRepository) RemoveFromPool(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, poolID := range m.pool {
		if poolID == id {
			m.pool = append(m.pool[:i], m.pool[i+1:]...)
			break
		}
	}
	return nil
}

func (m *MockRepository) ListByState(ctx context.Context, state domain.InstanceState) ([]*domain.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []*domain.Instance{}
	for _, inst := range m.instances {
		if inst.State == state {
			result = append(result, inst)
		}
	}
	return result, nil
}

func (m *MockRepository) ListExpired(ctx context.Context) ([]*domain.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []*domain.Instance{}
	for _, inst := range m.instances {
		if inst.IsExpired() {
			result = append(result, inst)
		}
	}
	return result, nil
}

func (m *MockRepository) SetInstanceTTL(ctx context.Context, id string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		exp := time.Now().Add(ttl)
		inst.ExpiresAt = &exp
		return nil
	}
	return domain.ErrInstanceNotFound
}

func (m *MockRepository) GetInstanceTTL(ctx context.Context, id string) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		return inst.TTLRemaining(), nil
	}
	return 0, domain.ErrInstanceNotFound
}

func (m *MockRepository) ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		if inst.ExpiresAt != nil {
			newExp := inst.ExpiresAt.Add(extension)
			inst.ExpiresAt = &newExp
		}
		return nil
	}
	return domain.ErrInstanceNotFound
}

func (m *MockRepository) AllocatePort(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port := 32000; port < 33000; port++ {
		if !m.ports[port] {
			m.ports[port] = true
			return port, nil
		}
	}
	return 0, domain.ErrNoPortsAvailable
}

func (m *MockRepository) ReleasePort(ctx context.Context, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.ports, port)
	return nil
}

func (m *MockRepository) CheckRateLimit(ctx context.Context, ip string, hourlyLimit, dailyLimit int) (bool, error) {
	return true, nil
}

func (m *MockRepository) IncrementRateLimit(ctx context.Context, ip string) error {
	return nil
}

func (m *MockRepository) GetPoolStats(ctx context.Context) (*domain.PoolStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := &domain.PoolStats{
		Ready: len(m.pool),
	}
	for _, inst := range m.instances {
		switch inst.State {
		case domain.StateAssigned:
			stats.Assigned++
		case domain.StateWarming:
			stats.Warming++
		}
	}
	return stats, nil
}

func (m *MockRepository) IncrementCounter(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name]++
	return nil
}

func (m *MockRepository) Ping(ctx context.Context) error {
	return nil
}

// MockRuntime implements container.Runtime for testing.
type MockRuntime struct {
	mu         sync.Mutex
	containers map[string]bool // containerID -> running
	startDelay time.Duration
	failStart  bool
}

func NewMockRuntime() *MockRuntime {
	return &MockRuntime{
		containers: make(map[string]bool),
	}
}

func (m *MockRuntime) RestoreFromCheckpoint(ctx context.Context, opts container.RestoreOptions) (*domain.Instance, error) {
	return nil, errors.New("not supported")
}

func (m *MockRuntime) Start(ctx context.Context, opts container.StartOptions) (*domain.Instance, error) {
	if m.failStart {
		return nil, errors.New("start failed")
	}
	if m.startDelay > 0 {
		time.Sleep(m.startDelay)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	containerID := "container-" + opts.Name
	m.containers[containerID] = true
	return &domain.Instance{
		ID:          opts.Name,
		ContainerID: containerID,
		Hostname:    opts.Hostname,
		Port:        opts.Port,
		State:       domain.StateWarming,
		DBPrefix:    opts.DBPrefix,
		CreatedAt:   time.Now(),
	}, nil
}

func (m *MockRuntime) Stop(ctx context.Context, containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.containers, containerID)
	return nil
}

func (m *MockRuntime) Inspect(ctx context.Context, containerID string) (*container.ContainerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.containers[containerID]; ok {
		return &container.ContainerInfo{
			ID:    containerID,
			State: "running",
		}, nil
	}
	return nil, domain.ErrContainerNotFound
}

func (m *MockRuntime) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.containers[containerID], nil
}

// MockRouteManager implements proxy.RouteManager for testing.
type MockRouteManager struct {
	mu     sync.Mutex
	routes map[string]proxy.Route
}

func NewMockRouteManager() *MockRouteManager {
	return &MockRouteManager{
		routes: make(map[string]proxy.Route),
	}
}

func (m *MockRouteManager) AddRoute(ctx context.Context, route proxy.Route) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[route.Hostname] = route
	return nil
}

func (m *MockRouteManager) RemoveRoute(ctx context.Context, hostname string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.routes, hostname)
	return nil
}

func (m *MockRouteManager) GetRoute(ctx context.Context, hostname string) (*proxy.Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if route, ok := m.routes[hostname]; ok {
		return &route, nil
	}
	return nil, domain.ErrRouteNotFound
}

func (m *MockRouteManager) ListRoutes(ctx context.Context) ([]proxy.Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	routes := make([]proxy.Route, 0, len(m.routes))
	for _, r := range m.routes {
		routes = append(routes, r)
	}
	return routes, nil
}

func (m *MockRouteManager) Health(ctx context.Context) error {
	return nil
}

// Tests

func TestPoolManager_Acquire(t *testing.T) {
	repo := NewMockRepository()
	runtime := NewMockRuntime()
	proxyMgr := NewMockRouteManager()

	cfg := ManagerConfig{
		TargetPoolSize: 5,
		DefaultTTL:     time.Hour,
		MaxTTL:         24 * time.Hour,
	}

	manager := NewPoolManager(cfg, repo, runtime, proxyMgr, "nginx:alpine", "", "localhost", "", logging.Nop())

	// Pre-populate pool with an instance
	instance := &domain.Instance{
		ID:        "test-instance-1",
		Hostname:  "demo-test",
		Port:      32000,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	}
	repo.AddToPool(context.Background(), instance)

	ctx := context.Background()

	// Acquire
	acquired, err := manager.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	if acquired.ID != instance.ID {
		t.Errorf("Acquired ID = %s, want %s", acquired.ID, instance.ID)
	}

	if acquired.State != domain.StateAssigned {
		t.Errorf("Acquired State = %v, want %v", acquired.State, domain.StateAssigned)
	}

	// Verify route was added
	_, err = proxyMgr.GetRoute(ctx, acquired.Hostname)
	if err != nil {
		t.Errorf("Route not added: %v", err)
	}

	// Verify counter incremented
	if repo.counters["acquisitions"] != 1 {
		t.Errorf("Acquisitions counter = %d, want 1", repo.counters["acquisitions"])
	}
}

func TestPoolManager_Acquire_PoolExhausted(t *testing.T) {
	repo := NewMockRepository()
	runtime := NewMockRuntime()
	proxyMgr := NewMockRouteManager()

	cfg := DefaultManagerConfig()
	manager := NewPoolManager(cfg, repo, runtime, proxyMgr, "nginx:alpine", "", "localhost", "", logging.Nop())

	ctx := context.Background()

	_, err := manager.Acquire(ctx)
	if err != domain.ErrPoolExhausted {
		t.Errorf("Acquire() error = %v, want %v", err, domain.ErrPoolExhausted)
	}
}

func TestPoolManager_Release(t *testing.T) {
	repo := NewMockRepository()
	runtime := NewMockRuntime()
	proxyMgr := NewMockRouteManager()

	cfg := DefaultManagerConfig()
	manager := NewPoolManager(cfg, repo, runtime, proxyMgr, "nginx:alpine", "", "localhost", "", logging.Nop())

	// Create an instance
	instance := &domain.Instance{
		ID:          "test-release-1",
		ContainerID: "container-test",
		Hostname:    "demo-release",
		Port:        32001,
		State:       domain.StateAssigned,
		CreatedAt:   time.Now(),
	}
	repo.instances[instance.ID] = instance
	repo.ports[instance.Port] = true
	runtime.containers[instance.ContainerID] = true
	proxyMgr.routes[instance.Hostname] = proxy.Route{Hostname: instance.Hostname}

	ctx := context.Background()

	// Release
	if err := manager.Release(ctx, instance.ID); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	// Verify cleanup
	if _, ok := repo.instances[instance.ID]; ok {
		t.Error("Instance not deleted from repo")
	}

	if runtime.containers[instance.ContainerID] {
		t.Error("Container not stopped")
	}

	if repo.ports[instance.Port] {
		t.Error("Port not released")
	}

	if _, err := proxyMgr.GetRoute(ctx, instance.Hostname); err == nil {
		t.Error("Route not removed")
	}
}

func TestPoolManager_Stats(t *testing.T) {
	repo := NewMockRepository()
	runtime := NewMockRuntime()
	proxyMgr := NewMockRouteManager()

	cfg := ManagerConfig{
		TargetPoolSize: 10,
		MaxPoolSize:    20,
	}
	manager := NewPoolManager(cfg, repo, runtime, proxyMgr, "nginx:alpine", "", "localhost", "", logging.Nop())

	// Add some instances
	repo.AddToPool(context.Background(), &domain.Instance{ID: "r1", State: domain.StateReady})
	repo.AddToPool(context.Background(), &domain.Instance{ID: "r2", State: domain.StateReady})
	repo.instances["a1"] = &domain.Instance{ID: "a1", State: domain.StateAssigned}
	repo.instances["w1"] = &domain.Instance{ID: "w1", State: domain.StateWarming}

	ctx := context.Background()
	stats, err := manager.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.Ready != 2 {
		t.Errorf("Ready = %d, want 2", stats.Ready)
	}
	if stats.Assigned != 1 {
		t.Errorf("Assigned = %d, want 1", stats.Assigned)
	}
	if stats.Warming != 1 {
		t.Errorf("Warming = %d, want 1", stats.Warming)
	}
	if stats.Target != 10 {
		t.Errorf("Target = %d, want 10", stats.Target)
	}
	if stats.Capacity != 20 {
		t.Errorf("Capacity = %d, want 20", stats.Capacity)
	}
}

func TestPoolManager_TriggerReplenish(t *testing.T) {
	repo := NewMockRepository()
	runtime := NewMockRuntime()
	proxyMgr := NewMockRouteManager()

	cfg := ManagerConfig{
		TargetPoolSize:     3,
		MaxPoolSize:        10,
		ReplenishThreshold: 0.5,
	}
	manager := NewPoolManager(cfg, repo, runtime, proxyMgr, "nginx:alpine", "", "localhost", "", logging.Nop())

	ctx := context.Background()

	// Pool is empty, should provision up to target
	if err := manager.TriggerReplenish(ctx); err != nil {
		t.Fatalf("TriggerReplenish() error = %v", err)
	}

	// Check that instances were created
	stats, _ := manager.Stats(ctx)
	if stats.Ready != 3 {
		t.Errorf("Ready after replenish = %d, want 3", stats.Ready)
	}
}

func TestPoolManager_StartStopReplenisher(t *testing.T) {
	repo := NewMockRepository()
	runtime := NewMockRuntime()
	proxyMgr := NewMockRouteManager()

	cfg := ManagerConfig{
		TargetPoolSize:    2,
		MaxPoolSize:       5,
		ReplenishInterval: 100 * time.Millisecond,
	}
	manager := NewPoolManager(cfg, repo, runtime, proxyMgr, "nginx:alpine", "", "localhost", "", logging.Nop())

	ctx := context.Background()

	// Start replenisher
	if err := manager.StartReplenisher(ctx); err != nil {
		t.Fatalf("StartReplenisher() error = %v", err)
	}

	// Wait for initial replenishment
	time.Sleep(300 * time.Millisecond)

	// Stop replenisher
	if err := manager.StopReplenisher(); err != nil {
		t.Fatalf("StopReplenisher() error = %v", err)
	}

	// Verify some instances were created
	stats, _ := manager.Stats(ctx)
	if stats.Ready == 0 {
		t.Error("No instances were provisioned")
	}
}

func TestPoolManager_GenerateHostname(t *testing.T) {
	manager := &PoolManager{}

	hostname1 := manager.generateHostname()
	hostname2 := manager.generateHostname()

	if hostname1 == hostname2 {
		t.Error("generateHostname returned same value twice")
	}

	if len(hostname1) < 5 {
		t.Errorf("Hostname too short: %s", hostname1)
	}
}

func TestPoolManager_GenerateDBPrefix(t *testing.T) {
	manager := &PoolManager{}

	prefix1 := manager.generateDBPrefix()
	prefix2 := manager.generateDBPrefix()

	if prefix1 == prefix2 {
		t.Error("generateDBPrefix returned same value twice")
	}

	if prefix1[0] != 'd' || prefix1[len(prefix1)-1] != '_' {
		t.Errorf("Invalid prefix format: %s", prefix1)
	}
}
