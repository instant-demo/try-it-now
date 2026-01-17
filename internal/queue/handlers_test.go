package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/instant-demo/try-it-now/internal/container"
	"github.com/instant-demo/try-it-now/internal/domain"
	"github.com/instant-demo/try-it-now/internal/proxy"
	"github.com/instant-demo/try-it-now/pkg/logging"
)

// mockProvisioner implements Provisioner for testing.
type mockProvisioner struct {
	provisionErr   error
	provisionCalls int
}

func (m *mockProvisioner) ProvisionOne(ctx context.Context) error {
	m.provisionCalls++
	return m.provisionErr
}

// mockRuntime implements container.Runtime for testing.
type mockRuntime struct {
	stopErr   error
	stopCalls []string
}

var _ container.Runtime = (*mockRuntime)(nil)

func (m *mockRuntime) Stop(ctx context.Context, containerID string) error {
	m.stopCalls = append(m.stopCalls, containerID)
	return m.stopErr
}

func (m *mockRuntime) Start(ctx context.Context, opts container.StartOptions) (*domain.Instance, error) {
	return nil, nil
}

func (m *mockRuntime) RestoreFromCheckpoint(ctx context.Context, opts container.RestoreOptions) (*domain.Instance, error) {
	return nil, nil
}

func (m *mockRuntime) Inspect(ctx context.Context, containerID string) (*container.ContainerInfo, error) {
	return nil, nil
}

func (m *mockRuntime) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	return true, nil
}

// mockProxy implements proxy.RouteManager for testing.
type mockProxy struct {
	removeErr   error
	removeCalls []string
}

var _ proxy.RouteManager = (*mockProxy)(nil)

func (m *mockProxy) RemoveRoute(ctx context.Context, hostname string) error {
	m.removeCalls = append(m.removeCalls, hostname)
	return m.removeErr
}

func (m *mockProxy) AddRoute(ctx context.Context, route proxy.Route) error {
	return nil
}

func (m *mockProxy) GetRoute(ctx context.Context, hostname string) (*proxy.Route, error) {
	return nil, nil
}

func (m *mockProxy) ListRoutes(ctx context.Context) ([]proxy.Route, error) {
	return nil, nil
}

func (m *mockProxy) Health(ctx context.Context) error {
	return nil
}

// mockRepository implements the subset of store.Repository needed for testing.
type mockRepository struct {
	releasePortErr      error
	deleteInstanceErr   error
	releasePortCalls    []int
	deleteInstanceCalls []string
}

func (m *mockRepository) ReleasePort(ctx context.Context, port int) error {
	m.releasePortCalls = append(m.releasePortCalls, port)
	return m.releasePortErr
}

func (m *mockRepository) DeleteInstance(ctx context.Context, id string) error {
	m.deleteInstanceCalls = append(m.deleteInstanceCalls, id)
	return m.deleteInstanceErr
}

// Implement other Repository methods as no-ops for interface satisfaction
func (m *mockRepository) SaveInstance(ctx context.Context, instance *domain.Instance) error {
	return nil
}
func (m *mockRepository) GetInstance(ctx context.Context, id string) (*domain.Instance, error) {
	return nil, nil
}
func (m *mockRepository) UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error {
	return nil
}
func (m *mockRepository) AcquireFromPool(ctx context.Context) (*domain.Instance, error) {
	return nil, nil
}
func (m *mockRepository) AddToPool(ctx context.Context, instance *domain.Instance) error {
	return nil
}
func (m *mockRepository) RemoveFromPool(ctx context.Context, id string) error { return nil }
func (m *mockRepository) ListByState(ctx context.Context, state domain.InstanceState) ([]*domain.Instance, error) {
	return nil, nil
}
func (m *mockRepository) ListExpired(ctx context.Context) ([]*domain.Instance, error) {
	return nil, nil
}
func (m *mockRepository) SetInstanceTTL(ctx context.Context, id string, ttl time.Duration) error {
	return nil
}
func (m *mockRepository) GetInstanceTTL(ctx context.Context, id string) (time.Duration, error) {
	return 0, nil
}
func (m *mockRepository) ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error {
	return nil
}
func (m *mockRepository) AllocatePort(ctx context.Context) (int, error)           { return 0, nil }
func (m *mockRepository) CheckRateLimit(ctx context.Context, ip string, hourly, daily int) (bool, error) {
	return true, nil
}
func (m *mockRepository) IncrementRateLimit(ctx context.Context, ip string) error { return nil }
func (m *mockRepository) GetPoolStats(ctx context.Context) (*domain.PoolStats, error) {
	return nil, nil
}
func (m *mockRepository) IncrementCounter(ctx context.Context, name string) error { return nil }
func (m *mockRepository) Ping(ctx context.Context) error                          { return nil }

func TestProvisionHandler_Success(t *testing.T) {
	provisioner := &mockProvisioner{}
	logger := logging.New("debug", "text")

	handlers := NewHandlers(provisioner, &mockRuntime{}, nil, &mockProxy{}, &mockRepository{}, logger)

	task := ProvisionTask{
		TaskID:    "test-task-1",
		Priority:  1,
		CreatedAt: time.Now(),
	}

	err := handlers.ProvisionHandler(context.Background(), task)
	if err != nil {
		t.Errorf("ProvisionHandler returned error: %v", err)
	}

	if provisioner.provisionCalls != 1 {
		t.Errorf("Expected 1 provision call, got %d", provisioner.provisionCalls)
	}
}

func TestProvisionHandler_Error(t *testing.T) {
	provisioner := &mockProvisioner{
		provisionErr: errors.New("provision failed"),
	}
	logger := logging.New("debug", "text")

	handlers := NewHandlers(provisioner, &mockRuntime{}, nil, &mockProxy{}, &mockRepository{}, logger)

	task := ProvisionTask{
		TaskID:    "test-task-1",
		Priority:  1,
		CreatedAt: time.Now(),
	}

	err := handlers.ProvisionHandler(context.Background(), task)
	if err == nil {
		t.Error("Expected error from ProvisionHandler")
	}

	if provisioner.provisionCalls != 1 {
		t.Errorf("Expected 1 provision call, got %d", provisioner.provisionCalls)
	}
}

func TestCleanupHandler_Success(t *testing.T) {
	runtime := &mockRuntime{}
	proxyMgr := &mockProxy{}
	repo := &mockRepository{}
	logger := logging.New("debug", "text")

	// psDB is nil - testing without database
	handlers := NewHandlers(&mockProvisioner{}, runtime, nil, proxyMgr, repo, logger)

	task := CleanupTask{
		TaskID:      "test-cleanup-1",
		InstanceID:  "inst-123",
		ContainerID: "container-abc",
		DBPrefix:    "d12345678_",
		Hostname:    "demo-test",
		Port:        8001,
		CreatedAt:   time.Now(),
	}

	err := handlers.CleanupHandler(context.Background(), task)
	if err != nil {
		t.Errorf("CleanupHandler returned error: %v", err)
	}

	// Verify cleanup operations were called (except DB since psDB is nil)
	if len(runtime.stopCalls) != 1 || runtime.stopCalls[0] != "container-abc" {
		t.Errorf("Expected Stop('container-abc'), got %v", runtime.stopCalls)
	}

	if len(proxyMgr.removeCalls) != 1 || proxyMgr.removeCalls[0] != "demo-test" {
		t.Errorf("Expected RemoveRoute('demo-test'), got %v", proxyMgr.removeCalls)
	}

	if len(repo.releasePortCalls) != 1 || repo.releasePortCalls[0] != 8001 {
		t.Errorf("Expected ReleasePort(8001), got %v", repo.releasePortCalls)
	}

	if len(repo.deleteInstanceCalls) != 1 || repo.deleteInstanceCalls[0] != "inst-123" {
		t.Errorf("Expected DeleteInstance('inst-123'), got %v", repo.deleteInstanceCalls)
	}
}

func TestCleanupHandler_Idempotent(t *testing.T) {
	// All operations fail - cleanup should still succeed (idempotent)
	runtime := &mockRuntime{stopErr: errors.New("container not found")}
	proxyMgr := &mockProxy{removeErr: errors.New("route not found")}
	repo := &mockRepository{
		releasePortErr:    errors.New("port already available"),
		deleteInstanceErr: errors.New("instance not found"),
	}
	logger := logging.New("debug", "text")

	handlers := NewHandlers(&mockProvisioner{}, runtime, nil, proxyMgr, repo, logger)

	task := CleanupTask{
		TaskID:      "test-cleanup-2",
		InstanceID:  "inst-456",
		ContainerID: "container-xyz",
		DBPrefix:    "d87654321_",
		Hostname:    "demo-test2",
		Port:        8002,
		CreatedAt:   time.Now(),
	}

	// Should succeed despite all operations failing
	err := handlers.CleanupHandler(context.Background(), task)
	if err != nil {
		t.Errorf("CleanupHandler should be idempotent, got error: %v", err)
	}

	// All operations should still be attempted
	if len(runtime.stopCalls) != 1 {
		t.Errorf("Expected 1 Stop call, got %d", len(runtime.stopCalls))
	}
	if len(proxyMgr.removeCalls) != 1 {
		t.Errorf("Expected 1 RemoveRoute call, got %d", len(proxyMgr.removeCalls))
	}
	if len(repo.releasePortCalls) != 1 {
		t.Errorf("Expected 1 ReleasePort call, got %d", len(repo.releasePortCalls))
	}
	if len(repo.deleteInstanceCalls) != 1 {
		t.Errorf("Expected 1 DeleteInstance call, got %d", len(repo.deleteInstanceCalls))
	}
}

func TestCleanupHandler_PartialTask(t *testing.T) {
	// Test cleanup with only some fields populated
	runtime := &mockRuntime{}
	proxyMgr := &mockProxy{}
	repo := &mockRepository{}
	logger := logging.New("debug", "text")

	handlers := NewHandlers(&mockProvisioner{}, runtime, nil, proxyMgr, repo, logger)

	// Only containerID and instanceID are set
	task := CleanupTask{
		TaskID:      "test-cleanup-3",
		InstanceID:  "inst-789",
		ContainerID: "container-only",
		CreatedAt:   time.Now(),
	}

	err := handlers.CleanupHandler(context.Background(), task)
	if err != nil {
		t.Errorf("CleanupHandler returned error: %v", err)
	}

	// Only container stop and instance delete should be called
	if len(runtime.stopCalls) != 1 {
		t.Errorf("Expected 1 Stop call, got %d", len(runtime.stopCalls))
	}
	if len(proxyMgr.removeCalls) != 0 {
		t.Errorf("Expected 0 RemoveRoute calls (no hostname), got %d", len(proxyMgr.removeCalls))
	}
	if len(repo.releasePortCalls) != 0 {
		t.Errorf("Expected 0 ReleasePort calls (no port), got %d", len(repo.releasePortCalls))
	}
	if len(repo.deleteInstanceCalls) != 1 {
		t.Errorf("Expected 1 DeleteInstance call, got %d", len(repo.deleteInstanceCalls))
	}
}

func TestCleanupHandler_EmptyTask(t *testing.T) {
	// Test cleanup with empty task - should succeed without doing anything
	runtime := &mockRuntime{}
	proxyMgr := &mockProxy{}
	repo := &mockRepository{}
	logger := logging.New("debug", "text")

	handlers := NewHandlers(&mockProvisioner{}, runtime, nil, proxyMgr, repo, logger)

	task := CleanupTask{
		TaskID:    "test-cleanup-4",
		CreatedAt: time.Now(),
	}

	err := handlers.CleanupHandler(context.Background(), task)
	if err != nil {
		t.Errorf("CleanupHandler returned error: %v", err)
	}

	// No operations should be called
	if len(runtime.stopCalls) != 0 {
		t.Errorf("Expected 0 Stop calls, got %d", len(runtime.stopCalls))
	}
	if len(proxyMgr.removeCalls) != 0 {
		t.Errorf("Expected 0 RemoveRoute calls, got %d", len(proxyMgr.removeCalls))
	}
	if len(repo.releasePortCalls) != 0 {
		t.Errorf("Expected 0 ReleasePort calls, got %d", len(repo.releasePortCalls))
	}
	if len(repo.deleteInstanceCalls) != 0 {
		t.Errorf("Expected 0 DeleteInstance calls, got %d", len(repo.deleteInstanceCalls))
	}
}
