package queue

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/internal/container"
	"github.com/instant-demo/try-it-now/internal/domain"
	"github.com/instant-demo/try-it-now/internal/proxy"
	"github.com/instant-demo/try-it-now/pkg/logging"
)

// Integration tests for NATS queue with real handlers.
// These tests require NATS to be running. Set NATS_TEST=1 to run.

// TestNATSIntegration_ProvisionHandlerWithRetry tests that provision tasks
// are retried when the handler returns an error.
func TestNATSIntegration_ProvisionHandlerWithRetry(t *testing.T) {
	cfg := skipIfNoNATS(t)
	cfg.StreamName = "TEST_RETRY_PROV_" + time.Now().Format("20060102150405")

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	// Handler fails first 2 times, succeeds on 3rd attempt
	var attempts atomic.Int32
	provisionHandler := func(ctx context.Context, task ProvisionTask) error {
		attempt := attempts.Add(1)
		t.Logf("Provision handler attempt %d for task %s", attempt, task.TaskID)
		if attempt < 3 {
			return errors.New("simulated failure")
		}
		return nil
	}
	noopCleanup := func(ctx context.Context, task CleanupTask) error {
		return nil
	}

	consumer, err := NewNATSConsumer(cfg, provisionHandler, noopCleanup, logging.New("debug", "text"))
	if err != nil {
		t.Fatalf("NewNATSConsumer() error = %v", err)
	}

	ctx := context.Background()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start() error = %v", err)
	}

	// Publish task
	task := ProvisionTask{
		TaskID:    "retry-test-1",
		Priority:  1,
		CreatedAt: time.Now(),
	}
	if err := pub.PublishProvisionTask(ctx, task); err != nil {
		t.Fatalf("PublishProvisionTask() error = %v", err)
	}

	// Wait for successful processing (with timeout)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 3 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Stop consumer
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := consumer.Stop(stopCtx); err != nil {
		t.Errorf("consumer.Stop() error = %v", err)
	}

	// Should have attempted at least 3 times (2 failures + 1 success)
	if attempts.Load() < 3 {
		t.Errorf("Expected at least 3 attempts, got %d", attempts.Load())
	}
}

// TestNATSIntegration_CleanupHandlerWithRetry tests that cleanup tasks
// are retried when the handler returns an error.
func TestNATSIntegration_CleanupHandlerWithRetry(t *testing.T) {
	cfg := skipIfNoNATS(t)
	cfg.StreamName = "TEST_RETRY_CLEANUP_" + time.Now().Format("20060102150405")

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	// Handler fails first time, succeeds on 2nd attempt
	var attempts atomic.Int32
	noopProvision := func(ctx context.Context, task ProvisionTask) error {
		return nil
	}
	cleanupHandler := func(ctx context.Context, task CleanupTask) error {
		attempt := attempts.Add(1)
		t.Logf("Cleanup handler attempt %d for task %s", attempt, task.TaskID)
		if attempt < 2 {
			return errors.New("simulated cleanup failure")
		}
		return nil
	}

	consumer, err := NewNATSConsumer(cfg, noopProvision, cleanupHandler, logging.New("debug", "text"))
	if err != nil {
		t.Fatalf("NewNATSConsumer() error = %v", err)
	}

	ctx := context.Background()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start() error = %v", err)
	}

	// Publish task
	task := CleanupTask{
		TaskID:      "cleanup-retry-1",
		InstanceID:  "inst-abc",
		ContainerID: "container-123",
		CreatedAt:   time.Now(),
	}
	if err := pub.PublishCleanupTask(ctx, task); err != nil {
		t.Fatalf("PublishCleanupTask() error = %v", err)
	}

	// Wait for successful processing
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 2 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Stop consumer
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := consumer.Stop(stopCtx); err != nil {
		t.Errorf("consumer.Stop() error = %v", err)
	}

	// Should have attempted at least 2 times
	if attempts.Load() < 2 {
		t.Errorf("Expected at least 2 attempts, got %d", attempts.Load())
	}
}

// TestNATSIntegration_Deduplication tests that messages with the same
// TaskID are deduplicated and not processed twice.
func TestNATSIntegration_Deduplication(t *testing.T) {
	cfg := skipIfNoNATS(t)
	cfg.StreamName = "TEST_DEDUP_" + time.Now().Format("20060102150405")

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	var processedCount atomic.Int32
	provisionHandler := func(ctx context.Context, task ProvisionTask) error {
		processedCount.Add(1)
		t.Logf("Processed task %s", task.TaskID)
		return nil
	}
	noopCleanup := func(ctx context.Context, task CleanupTask) error {
		return nil
	}

	consumer, err := NewNATSConsumer(cfg, provisionHandler, noopCleanup, logging.New("debug", "text"))
	if err != nil {
		t.Fatalf("NewNATSConsumer() error = %v", err)
	}

	ctx := context.Background()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start() error = %v", err)
	}

	// Publish the same task twice with same TaskID (MsgID)
	task := ProvisionTask{
		TaskID:    "dedup-test-same-id",
		Priority:  1,
		CreatedAt: time.Now(),
	}

	// First publish
	if err := pub.PublishProvisionTask(ctx, task); err != nil {
		t.Fatalf("First PublishProvisionTask() error = %v", err)
	}

	// Second publish with same TaskID - should be deduplicated
	if err := pub.PublishProvisionTask(ctx, task); err != nil {
		t.Fatalf("Second PublishProvisionTask() error = %v", err)
	}

	// Give time for processing
	time.Sleep(3 * time.Second)

	// Stop consumer
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := consumer.Stop(stopCtx); err != nil {
		t.Errorf("consumer.Stop() error = %v", err)
	}

	// Should only process once due to deduplication
	if processedCount.Load() != 1 {
		t.Errorf("Expected 1 processed task (deduplicated), got %d", processedCount.Load())
	}
}

// TestNATSIntegration_RealHandlers tests the real Handlers struct with NATS.
// This is a more comprehensive integration test.
func TestNATSIntegration_RealHandlers(t *testing.T) {
	cfg := skipIfNoNATS(t)
	cfg.StreamName = "TEST_REAL_HANDLERS_" + time.Now().Format("20060102150405")

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	// Create mock dependencies for Handlers
	var provisionCalled atomic.Int32
	var cleanupCalled atomic.Int32

	mockProvisioner := &testProvisioner{
		provisionFunc: func(ctx context.Context) error {
			provisionCalled.Add(1)
			t.Log("Mock provisioner called")
			return nil
		},
	}

	mockRuntime := &testRuntime{}
	mockProxy := &testProxy{}
	mockRepo := &testRepo{}
	logger := logging.New("debug", "text")

	handlers := NewHandlers(mockProvisioner, mockRuntime, nil, mockProxy, mockRepo, logger)

	// Wrap cleanup handler to track calls
	originalCleanupHandler := handlers.CleanupHandler
	wrappedCleanupHandler := func(ctx context.Context, task CleanupTask) error {
		cleanupCalled.Add(1)
		return originalCleanupHandler(ctx, task)
	}

	consumer, err := NewNATSConsumer(cfg, handlers.ProvisionHandler, wrappedCleanupHandler, logger)
	if err != nil {
		t.Fatalf("NewNATSConsumer() error = %v", err)
	}

	ctx := context.Background()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start() error = %v", err)
	}

	// Publish provision task
	provTask := ProvisionTask{
		TaskID:    "real-handler-prov-1",
		Priority:  1,
		CreatedAt: time.Now(),
	}
	if err := pub.PublishProvisionTask(ctx, provTask); err != nil {
		t.Fatalf("PublishProvisionTask() error = %v", err)
	}

	// Publish cleanup task
	cleanupTask := CleanupTask{
		TaskID:      "real-handler-cleanup-1",
		InstanceID:  "inst-xyz",
		ContainerID: "container-xyz",
		Hostname:    "demo-xyz",
		Port:        8001,
		CreatedAt:   time.Now(),
	}
	if err := pub.PublishCleanupTask(ctx, cleanupTask); err != nil {
		t.Fatalf("PublishCleanupTask() error = %v", err)
	}

	// Wait for processing
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if provisionCalled.Load() >= 1 && cleanupCalled.Load() >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Stop consumer
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := consumer.Stop(stopCtx); err != nil {
		t.Errorf("consumer.Stop() error = %v", err)
	}

	// Verify handlers were called
	if provisionCalled.Load() != 1 {
		t.Errorf("Expected provision handler called 1 time, got %d", provisionCalled.Load())
	}
	if cleanupCalled.Load() != 1 {
		t.Errorf("Expected cleanup handler called 1 time, got %d", cleanupCalled.Load())
	}
}

// Test helper types

type testProvisioner struct {
	provisionFunc func(ctx context.Context) error
}

func (p *testProvisioner) ProvisionOne(ctx context.Context) error {
	if p.provisionFunc != nil {
		return p.provisionFunc(ctx)
	}
	return nil
}

// testRuntime implements container.Runtime for integration testing.
type testRuntime struct {
	stopCalls []string
}

var _ container.Runtime = (*testRuntime)(nil)

func (r *testRuntime) Stop(ctx context.Context, containerID string) error {
	r.stopCalls = append(r.stopCalls, containerID)
	return nil
}

func (r *testRuntime) Start(ctx context.Context, opts container.StartOptions) (*domain.Instance, error) {
	return nil, nil
}

func (r *testRuntime) RestoreFromCheckpoint(ctx context.Context, opts container.RestoreOptions) (*domain.Instance, error) {
	return nil, nil
}

func (r *testRuntime) Inspect(ctx context.Context, containerID string) (*container.ContainerInfo, error) {
	return nil, nil
}

func (r *testRuntime) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	return true, nil
}

// testProxy implements proxy.RouteManager for integration testing.
type testProxy struct {
	removeCalls []string
}

var _ proxy.RouteManager = (*testProxy)(nil)

func (p *testProxy) RemoveRoute(ctx context.Context, hostname string) error {
	p.removeCalls = append(p.removeCalls, hostname)
	return nil
}

func (p *testProxy) AddRoute(ctx context.Context, route proxy.Route) error {
	return nil
}

func (p *testProxy) GetRoute(ctx context.Context, hostname string) (*proxy.Route, error) {
	return nil, nil
}

func (p *testProxy) ListRoutes(ctx context.Context) ([]proxy.Route, error) {
	return nil, nil
}

func (p *testProxy) Health(ctx context.Context) error {
	return nil
}

// testRepo implements store.Repository for integration testing.
type testRepo struct {
	releasePortCalls    []int
	deleteInstanceCalls []string
}

func (r *testRepo) ReleasePort(ctx context.Context, port int) error {
	r.releasePortCalls = append(r.releasePortCalls, port)
	return nil
}

func (r *testRepo) DeleteInstance(ctx context.Context, id string) error {
	r.deleteInstanceCalls = append(r.deleteInstanceCalls, id)
	return nil
}

// Implement other Repository methods as no-ops
func (r *testRepo) SaveInstance(ctx context.Context, instance *domain.Instance) error {
	return nil
}
func (r *testRepo) GetInstance(ctx context.Context, id string) (*domain.Instance, error) {
	return nil, nil
}
func (r *testRepo) UpdateInstanceState(ctx context.Context, id string, state domain.InstanceState) error {
	return nil
}
func (r *testRepo) AcquireFromPool(ctx context.Context) (*domain.Instance, error) { return nil, nil }
func (r *testRepo) AddToPool(ctx context.Context, instance *domain.Instance) error {
	return nil
}
func (r *testRepo) RemoveFromPool(ctx context.Context, id string) error { return nil }
func (r *testRepo) ListByState(ctx context.Context, state domain.InstanceState) ([]*domain.Instance, error) {
	return nil, nil
}
func (r *testRepo) ListExpired(ctx context.Context) ([]*domain.Instance, error) { return nil, nil }
func (r *testRepo) SetInstanceTTL(ctx context.Context, id string, ttl time.Duration) error {
	return nil
}
func (r *testRepo) GetInstanceTTL(ctx context.Context, id string) (time.Duration, error) {
	return 0, nil
}
func (r *testRepo) ExtendInstanceTTL(ctx context.Context, id string, extension time.Duration) error {
	return nil
}
func (r *testRepo) AllocatePort(ctx context.Context) (int, error) { return 0, nil }
func (r *testRepo) CheckRateLimit(ctx context.Context, ip string, hourly, daily int) (bool, error) {
	return true, nil
}
func (r *testRepo) IncrementRateLimit(ctx context.Context, ip string) error { return nil }
func (r *testRepo) GetPoolStats(ctx context.Context) (*domain.PoolStats, error) {
	return nil, nil
}
func (r *testRepo) IncrementCounter(ctx context.Context, name string) error { return nil }
func (r *testRepo) Ping(ctx context.Context) error                          { return nil }

// TestNATSIntegration_MultipleWorkers tests concurrent message processing.
func TestNATSIntegration_MultipleWorkers(t *testing.T) {
	if os.Getenv("NATS_TEST") == "" {
		t.Skip("Skipping NATS integration test. Set NATS_TEST=1 to run.")
	}

	cfg := &config.QueueConfig{
		NATSURL:     getEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		StreamName:  "TEST_WORKERS_" + time.Now().Format("20060102150405"),
		WorkerCount: 4, // Multiple workers
	}

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	var processedCount atomic.Int32
	provisionHandler := func(ctx context.Context, task ProvisionTask) error {
		// Simulate some work
		time.Sleep(50 * time.Millisecond)
		processedCount.Add(1)
		return nil
	}
	noopCleanup := func(ctx context.Context, task CleanupTask) error {
		return nil
	}

	consumer, err := NewNATSConsumer(cfg, provisionHandler, noopCleanup, logging.New("debug", "text"))
	if err != nil {
		t.Fatalf("NewNATSConsumer() error = %v", err)
	}

	ctx := context.Background()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start() error = %v", err)
	}

	// Publish multiple tasks
	taskCount := 10
	for i := 0; i < taskCount; i++ {
		task := ProvisionTask{
			TaskID:    "worker-test-" + time.Now().Format("20060102150405.000000") + "-" + string(rune('a'+i)),
			Priority:  1,
			CreatedAt: time.Now(),
		}
		if err := pub.PublishProvisionTask(ctx, task); err != nil {
			t.Errorf("PublishProvisionTask() error = %v", err)
		}
	}

	// Wait for all to be processed
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if processedCount.Load() >= int32(taskCount) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Stop consumer
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := consumer.Stop(stopCtx); err != nil {
		t.Errorf("consumer.Stop() error = %v", err)
	}

	// All tasks should be processed
	if processedCount.Load() != int32(taskCount) {
		t.Errorf("Expected %d processed tasks, got %d", taskCount, processedCount.Load())
	}
}
