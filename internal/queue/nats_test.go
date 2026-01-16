package queue

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/pkg/logging"
)

// skipIfNoNATS skips the test if NATS is not available.
func skipIfNoNATS(t *testing.T) *config.QueueConfig {
	t.Helper()
	if os.Getenv("NATS_TEST") == "" {
		t.Skip("Skipping NATS integration test. Set NATS_TEST=1 to run.")
	}

	return &config.QueueConfig{
		NATSURL:     getEnvOrDefault("NATS_URL", "nats://localhost:4222"),
		StreamName:  "TEST_PROVISIONING",
		WorkerCount: 2,
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func TestNATSPublisher_Connect(t *testing.T) {
	cfg := skipIfNoNATS(t)

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	// Verify stream was created
	if pub.stream == nil {
		t.Error("Expected stream to be created")
	}
}

func TestNATSPublisher_PublishProvisionTask(t *testing.T) {
	cfg := skipIfNoNATS(t)
	// Use unique stream name to avoid conflicts
	cfg.StreamName = "TEST_PUBLISH_PROV_" + time.Now().Format("20060102150405")

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	ctx := context.Background()
	task := ProvisionTask{
		TaskID:    "test-task-1",
		Priority:  1,
		CreatedAt: time.Now(),
	}

	if err := pub.PublishProvisionTask(ctx, task); err != nil {
		t.Errorf("PublishProvisionTask() error = %v", err)
	}
}

func TestNATSPublisher_PublishCleanupTask(t *testing.T) {
	cfg := skipIfNoNATS(t)
	// Use unique stream name to avoid conflicts
	cfg.StreamName = "TEST_PUBLISH_CLEANUP_" + time.Now().Format("20060102150405")

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	ctx := context.Background()
	task := CleanupTask{
		TaskID:      "cleanup-task-1",
		InstanceID:  "instance-123",
		ContainerID: "container-abc",
		DBPrefix:    "db_prefix_",
		Hostname:    "demo-host",
		Port:        32001,
		CreatedAt:   time.Now(),
	}

	if err := pub.PublishCleanupTask(ctx, task); err != nil {
		t.Errorf("PublishCleanupTask() error = %v", err)
	}
}

func TestNATSConsumer_RoundTrip(t *testing.T) {
	cfg := skipIfNoNATS(t)
	// Use unique stream name to avoid conflicts
	cfg.StreamName = "TEST_ROUNDTRIP_" + time.Now().Format("20060102150405")

	// Create publisher first (creates stream)
	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	// Track received tasks
	var provisionReceived atomic.Int32
	var cleanupReceived atomic.Int32

	provisionHandler := func(ctx context.Context, task ProvisionTask) error {
		provisionReceived.Add(1)
		t.Logf("Received provision task: %s", task.TaskID)
		return nil
	}
	cleanupHandler := func(ctx context.Context, task CleanupTask) error {
		cleanupReceived.Add(1)
		t.Logf("Received cleanup task: %s", task.TaskID)
		return nil
	}

	// Create consumer
	consumer, err := NewNATSConsumer(cfg, provisionHandler, cleanupHandler, logging.Nop())
	if err != nil {
		t.Fatalf("NewNATSConsumer() error = %v", err)
	}

	ctx := context.Background()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start() error = %v", err)
	}

	// Publish tasks
	provisionTask := ProvisionTask{
		TaskID:    "roundtrip-prov-1",
		Priority:  1,
		CreatedAt: time.Now(),
	}
	if err := pub.PublishProvisionTask(ctx, provisionTask); err != nil {
		t.Fatalf("PublishProvisionTask() error = %v", err)
	}

	cleanupTask := CleanupTask{
		TaskID:     "roundtrip-cleanup-1",
		InstanceID: "instance-xyz",
		CreatedAt:  time.Now(),
	}
	if err := pub.PublishCleanupTask(ctx, cleanupTask); err != nil {
		t.Fatalf("PublishCleanupTask() error = %v", err)
	}

	// Wait for processing (with timeout)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if provisionReceived.Load() >= 1 && cleanupReceived.Load() >= 1 {
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

	// Verify
	if provisionReceived.Load() != 1 {
		t.Errorf("Expected 1 provision task, got %d", provisionReceived.Load())
	}
	if cleanupReceived.Load() != 1 {
		t.Errorf("Expected 1 cleanup task, got %d", cleanupReceived.Load())
	}
}

func TestNATSConsumer_GracefulShutdown(t *testing.T) {
	cfg := skipIfNoNATS(t)
	cfg.StreamName = "TEST_SHUTDOWN_" + time.Now().Format("20060102150405")

	pub, err := NewNATSPublisher(cfg)
	if err != nil {
		t.Fatalf("NewNATSPublisher() error = %v", err)
	}
	defer pub.Close()

	handler := func(ctx context.Context, task ProvisionTask) error {
		// Simulate slow processing
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	noopCleanup := func(ctx context.Context, task CleanupTask) error {
		return nil
	}

	consumer, err := NewNATSConsumer(cfg, handler, noopCleanup, logging.Nop())
	if err != nil {
		t.Fatalf("NewNATSConsumer() error = %v", err)
	}

	ctx := context.Background()
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("consumer.Start() error = %v", err)
	}

	// Stop should complete within timeout
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := time.Now()
	if err := consumer.Stop(stopCtx); err != nil {
		t.Errorf("consumer.Stop() error = %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("Stop took too long: %v", elapsed)
	}
	t.Logf("Stop completed in %v", elapsed)
}
