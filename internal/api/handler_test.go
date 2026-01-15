package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/boss/demo-multiplexer/internal/metrics"
	"github.com/boss/demo-multiplexer/internal/pool"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// MockPoolManager implements pool.Manager for testing.
type MockPoolManager struct {
	mu        sync.Mutex
	instances map[string]*domain.Instance
	stats     *domain.PoolStats
}

func NewMockPoolManager() *MockPoolManager {
	return &MockPoolManager{
		instances: make(map[string]*domain.Instance),
		stats: &domain.PoolStats{
			Ready:    5,
			Assigned: 2,
			Warming:  1,
			Target:   10,
			Capacity: 20,
		},
	}
}

func (m *MockPoolManager) Acquire(ctx context.Context) (*domain.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.instances) == 0 {
		return nil, domain.ErrPoolExhausted
	}
	for id, inst := range m.instances {
		delete(m.instances, id)
		inst.State = domain.StateAssigned
		now := time.Now()
		inst.AssignedAt = &now
		exp := time.Now().Add(time.Hour)
		inst.ExpiresAt = &exp
		return inst, nil
	}
	return nil, domain.ErrPoolExhausted
}

func (m *MockPoolManager) Release(ctx context.Context, instanceID string) error {
	return nil
}

func (m *MockPoolManager) Stats(ctx context.Context) (*domain.PoolStats, error) {
	return m.stats, nil
}

func (m *MockPoolManager) StartReplenisher(ctx context.Context) error {
	return nil
}

func (m *MockPoolManager) StopReplenisher() error {
	return nil
}

func (m *MockPoolManager) TriggerReplenish(ctx context.Context) error {
	return nil
}

func (m *MockPoolManager) AddInstance(inst *domain.Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[inst.ID] = inst
}

// MockRepository implements store.Repository for testing.
type MockRepository struct {
	mu        sync.Mutex
	instances map[string]*domain.Instance
}

func NewMockRepository() *MockRepository {
	return &MockRepository{
		instances: make(map[string]*domain.Instance),
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
	return nil
}

func (m *MockRepository) AcquireFromPool(ctx context.Context) (*domain.Instance, error) {
	return nil, domain.ErrPoolExhausted
}

func (m *MockRepository) AddToPool(ctx context.Context, instance *domain.Instance) error {
	return nil
}

func (m *MockRepository) RemoveFromPool(ctx context.Context, id string) error {
	return nil
}

func (m *MockRepository) ListByState(ctx context.Context, state domain.InstanceState) ([]*domain.Instance, error) {
	return nil, nil
}

func (m *MockRepository) ListExpired(ctx context.Context) ([]*domain.Instance, error) {
	return nil, nil
}

func (m *MockRepository) SetInstanceTTL(ctx context.Context, id string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		exp := time.Now().Add(ttl)
		inst.ExpiresAt = &exp
	}
	return nil
}

func (m *MockRepository) GetInstanceTTL(ctx context.Context, id string) (time.Duration, error) {
	return time.Hour, nil
}

func (m *MockRepository) ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		if inst.ExpiresAt != nil {
			newExp := inst.ExpiresAt.Add(extension)
			inst.ExpiresAt = &newExp
		}
	}
	return nil
}

func (m *MockRepository) AllocatePort(ctx context.Context) (int, error) {
	return 32000, nil
}

func (m *MockRepository) ReleasePort(ctx context.Context, port int) error {
	return nil
}

func (m *MockRepository) CheckRateLimit(ctx context.Context, ip string, hourlyLimit, dailyLimit int) (bool, error) {
	return true, nil
}

func (m *MockRepository) IncrementRateLimit(ctx context.Context, ip string) error {
	return nil
}

func (m *MockRepository) GetPoolStats(ctx context.Context) (*domain.PoolStats, error) {
	return &domain.PoolStats{Ready: 5, Assigned: 2}, nil
}

func (m *MockRepository) IncrementCounter(ctx context.Context, name string) error {
	return nil
}

func (m *MockRepository) Ping(ctx context.Context) error {
	return nil
}

func (m *MockRepository) AddInstance(inst *domain.Instance) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[inst.ID] = inst
}

// Helper to create a test handler
func newTestHandler() (*Handler, *MockPoolManager, *MockRepository) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "docker",
		},
		Proxy: config.ProxyConfig{
			BaseDomain: "localhost",
		},
		PrestaShop: config.PrestaShopConfig{
			AdminPath: "admin-demo",
		},
		RateLimit: config.RateLimitConfig{
			RequestsPerHour: 2,
			RequestsPerDay:  5,
		},
		Pool: config.PoolConfig{
			MaxTTL: 24 * time.Hour,
		},
	}
	poolMgr := NewMockPoolManager()
	repo := NewMockRepository()
	h := NewHandler(cfg, poolMgr, repo, nil) // nil metrics for tests
	return h, poolMgr, repo
}

func TestNewHandler(t *testing.T) {
	h, _, _ := newTestHandler()

	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.cfg == nil {
		t.Error("NewHandler did not set config")
	}
	if h.pool == nil {
		t.Error("NewHandler did not set pool manager")
	}
	if h.store == nil {
		t.Error("NewHandler did not set store")
	}
}

func TestRouter(t *testing.T) {
	h, _, _ := newTestHandler()

	router := h.Router()

	if router == nil {
		t.Fatal("Router returned nil")
	}
}

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "podman",
		},
	}
	poolMgr := NewMockPoolManager()
	repo := NewMockRepository()
	h := NewHandler(cfg, poolMgr, repo, nil) // nil metrics for tests
	router := h.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", response["status"])
	}
	if response["mode"] != "podman" {
		t.Errorf("expected mode 'podman', got %q", response["mode"])
	}
}

func TestPoolStatsEndpoint(t *testing.T) {
	h, _, _ := newTestHandler()
	router := h.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/pool/stats", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var stats StatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if stats.Ready != 5 {
		t.Errorf("expected Ready=5, got %d", stats.Ready)
	}
	if stats.Assigned != 2 {
		t.Errorf("expected Assigned=2, got %d", stats.Assigned)
	}
}

func TestAcquireEndpoint_PoolExhausted(t *testing.T) {
	h, _, _ := newTestHandler()
	router := h.Router()

	// Pool is empty, should return 503
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/demo/acquire", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestAcquireEndpoint_Success(t *testing.T) {
	h, poolMgr, _ := newTestHandler()
	router := h.Router()

	// Add an instance to the pool
	poolMgr.AddInstance(&domain.Instance{
		ID:        "test-123",
		Hostname:  "demo-test",
		Port:      32000,
		State:     domain.StateReady,
		CreatedAt: time.Now(),
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/demo/acquire", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp AcquireResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.ID != "test-123" {
		t.Errorf("expected ID=test-123, got %s", resp.ID)
	}
	if resp.URL == "" {
		t.Error("expected URL to be set")
	}
}

func TestGetDemoEndpoint_NotFound(t *testing.T) {
	h, _, _ := newTestHandler()
	router := h.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/demo/nonexistent", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}
}

func TestGetDemoEndpoint_Success(t *testing.T) {
	h, _, repo := newTestHandler()
	router := h.Router()

	// Add an instance to the repository
	repo.AddInstance(&domain.Instance{
		ID:        "existing-123",
		Hostname:  "demo-existing",
		Port:      32001,
		State:     domain.StateAssigned,
		CreatedAt: time.Now(),
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/demo/existing-123", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp InstanceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.ID != "existing-123" {
		t.Errorf("expected ID=existing-123, got %s", resp.ID)
	}
	if resp.State != "assigned" {
		t.Errorf("expected State=assigned, got %s", resp.State)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "docker",
		},
		RateLimit: config.RateLimitConfig{
			RequestsPerHour: 10,
			RequestsPerDay:  50,
		},
		Pool: config.PoolConfig{
			MaxTTL: 24 * time.Hour,
		},
	}
	poolMgr := NewMockPoolManager()
	repo := NewMockRepository()
	metricsCollector := metrics.NewCollector()
	h := NewHandler(cfg, poolMgr, repo, metricsCollector)
	router := h.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/metrics", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("expected metrics content")
	}
	if !containsString(body, "demo_pool_ready") {
		t.Error("expected demo_pool_ready metric")
	}
}

func TestNotFoundRoute(t *testing.T) {
	h, _, _ := newTestHandler()
	router := h.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/nonexistent", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d for nonexistent route, got %d", http.StatusNotFound, w.Code)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStringHelper(s, substr))
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Ensure MockPoolManager implements pool.Manager
var _ pool.Manager = (*MockPoolManager)(nil)
