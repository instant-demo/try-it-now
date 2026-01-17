//go:build linux && criu

package container

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/internal/domain"
	"github.com/instant-demo/try-it-now/pkg/logging"
)

// CRIU integration tests for Linux with checkpoint/restore.
// These tests require:
// - Linux operating system
// - Podman running rootful with CRIU-enabled OCI runtime (crun +CRIU)
// - A valid checkpoint file at CHECKPOINT_PATH
// - Run with: CRIU_TEST=1 CHECKPOINT_PATH=/path/to/checkpoint.tar.gz go test -v -tags=criu ./internal/container/...

// skipIfNoCRIU skips the test if CRIU is not available or checkpoint doesn't exist.
func skipIfNoCRIU(t *testing.T) (*PodmanRuntime, string) {
	t.Helper()
	if os.Getenv("CRIU_TEST") == "" {
		t.Skip("Skipping CRIU integration test. Set CRIU_TEST=1 to run.")
	}

	checkpointPath := os.Getenv("CHECKPOINT_PATH")
	if checkpointPath == "" {
		t.Skip("Skipping CRIU test. Set CHECKPOINT_PATH to checkpoint file location.")
	}

	if _, err := os.Stat(checkpointPath); os.IsNotExist(err) {
		t.Skipf("Checkpoint file not found at %s", checkpointPath)
	}

	containerCfg := &config.ContainerConfig{
		Mode:             "podman",
		Image:            "nginx:alpine",
		PortRangeStart:   32200,
		PortRangeEnd:     32299,
		PodmanSocketPath: getTestSocketPath(),
		CRIUEnabled:      true,
		CheckpointPath:   checkpointPath,
	}
	psCfg := &config.PrestaShopConfig{
		DBHost:     os.Getenv("PS_DB_HOST"),
		DBPort:     3306,
		DBName:     os.Getenv("PS_DB_NAME"),
		DBUser:     os.Getenv("PS_DB_USER"),
		DBPassword: os.Getenv("PS_DB_PASSWORD"),
		AdminPath:  "admin-demo",
	}
	proxyCfg := &config.ProxyConfig{
		CaddyAdminURL: "http://localhost:2019",
		BaseDomain:    "localhost",
	}

	runtime, err := NewPodmanRuntime(containerCfg, psCfg, proxyCfg, logging.New("debug", "text"))
	if err != nil {
		t.Skipf("Failed to connect to Podman: %v", err)
	}

	if !runtime.CRIUAvailable() {
		runtime.Close()
		t.Skip("CRIU not available. Ensure Podman is rootful with CRIU-enabled OCI runtime (crun +CRIU).")
	}

	return runtime, checkpointPath
}

// TestCRIU_RestoreFromCheckpoint tests the complete CRIU restore flow.
func TestCRIU_RestoreFromCheckpoint(t *testing.T) {
	runtime, checkpointPath := skipIfNoCRIU(t)
	defer runtime.Close()

	ctx := context.Background()
	testPort := 32201

	// Restore from checkpoint
	opts := RestoreOptions{
		CheckpointPath: checkpointPath,
		Name:           "criu-restore-test",
		Hostname:       "criu-test-host",
		Port:           testPort,
		DBPrefix:       "criutest_",
		Labels: map[string]string{
			"app":  "try-it-now-test",
			"test": "criu-restore",
		},
	}

	t.Log("Restoring container from checkpoint...")
	instance, err := runtime.RestoreFromCheckpoint(ctx, opts)
	if err != nil {
		t.Fatalf("RestoreFromCheckpoint() error = %v", err)
	}

	// Ensure cleanup
	defer func() {
		t.Log("Cleaning up restored container...")
		if err := runtime.Stop(ctx, instance.ContainerID); err != nil {
			t.Errorf("Stop() cleanup error = %v", err)
		}
	}()

	// Verify instance fields
	if instance.ContainerID == "" {
		t.Error("RestoreFromCheckpoint() returned empty ContainerID")
	}
	if instance.Port != opts.Port {
		t.Errorf("RestoreFromCheckpoint() Port = %d, want %d", instance.Port, opts.Port)
	}
	if instance.State != domain.StateWarming {
		t.Errorf("RestoreFromCheckpoint() State = %v, want %v", instance.State, domain.StateWarming)
	}

	t.Logf("Container restored: ID=%s Port=%d", instance.ContainerID, instance.Port)

	// Give container time to fully restore
	time.Sleep(2 * time.Second)

	// Verify container is running
	t.Log("Inspecting restored container...")
	info, err := runtime.Inspect(ctx, instance.ContainerID)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	if info.State != "running" {
		t.Errorf("Inspect() State = %s, want running", info.State)
	}

	// Verify port mapping
	if hostPort, ok := info.Ports[80]; !ok || hostPort != testPort {
		t.Errorf("Inspect() Ports[80] = %d, want %d", hostPort, testPort)
	}

	t.Logf("Container state: %s, IP: %s, Ports: %v", info.State, info.IPAddress, info.Ports)
}

// TestCRIU_RestoreAndHealthCheck tests CRIU restore followed by health check.
func TestCRIU_RestoreAndHealthCheck(t *testing.T) {
	runtime, checkpointPath := skipIfNoCRIU(t)
	defer runtime.Close()

	ctx := context.Background()
	testPort := 32202

	opts := RestoreOptions{
		CheckpointPath: checkpointPath,
		Name:           "criu-health-test",
		Hostname:       "criu-health-host",
		Port:           testPort,
		DBPrefix:       "criuhealth_",
	}

	t.Log("Restoring container for health check test...")
	instance, err := runtime.RestoreFromCheckpoint(ctx, opts)
	if err != nil {
		t.Fatalf("RestoreFromCheckpoint() error = %v", err)
	}
	defer func() {
		_ = runtime.Stop(ctx, instance.ContainerID)
	}()

	t.Logf("Container restored: ID=%s Port=%d", instance.ContainerID, instance.Port)

	// Wait for container to be healthy (with timeout)
	t.Log("Waiting for container to pass health check...")
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Initial delay
	time.Sleep(2 * time.Second)

	attempt := 0
	for {
		select {
		case <-timeout:
			t.Fatalf("Timed out waiting for container to be healthy after %d attempts", attempt)
		case <-ticker.C:
			attempt++
			healthy, err := runtime.HealthCheck(ctx, instance.ContainerID)
			if err != nil {
				t.Logf("Attempt %d: HealthCheck error: %v", attempt, err)
				continue
			}
			if healthy {
				t.Logf("Container became healthy after %d attempts", attempt)
				return // Test passed
			}
			t.Logf("Attempt %d: Container not yet healthy", attempt)
		}
	}
}

// TestCRIU_RestoreWithInvalidCheckpoint tests error handling for missing checkpoint.
func TestCRIU_RestoreWithInvalidCheckpoint(t *testing.T) {
	runtime, _ := skipIfNoCRIU(t)
	defer runtime.Close()

	ctx := context.Background()

	opts := RestoreOptions{
		CheckpointPath: "/nonexistent/checkpoint.tar.gz",
		Name:           "criu-invalid-test",
		Hostname:       "criu-invalid-host",
		Port:           32203,
	}

	_, err := runtime.RestoreFromCheckpoint(ctx, opts)
	if err != domain.ErrCheckpointNotFound {
		t.Errorf("RestoreFromCheckpoint() error = %v, want %v", err, domain.ErrCheckpointNotFound)
	}
}

// TestCRIU_MultipleRestores tests restoring multiple containers from the same checkpoint.
func TestCRIU_MultipleRestores(t *testing.T) {
	runtime, checkpointPath := skipIfNoCRIU(t)
	defer runtime.Close()

	ctx := context.Background()
	baseName := "criu-multi-test"
	basePort := 32210

	// Track instances for cleanup
	var instances []*domain.Instance

	// Restore multiple containers
	for i := 0; i < 3; i++ {
		opts := RestoreOptions{
			CheckpointPath: checkpointPath,
			Name:           baseName,
			Hostname:       baseName,
			Port:           basePort + i,
			DBPrefix:       "criumlti_",
		}

		t.Logf("Restoring container %d on port %d...", i+1, opts.Port)
		instance, err := runtime.RestoreFromCheckpoint(ctx, opts)
		if err != nil {
			// Clean up already created instances
			for _, inst := range instances {
				_ = runtime.Stop(ctx, inst.ContainerID)
			}
			t.Fatalf("RestoreFromCheckpoint() for instance %d error = %v", i+1, err)
		}
		instances = append(instances, instance)
	}

	// Cleanup all at the end
	defer func() {
		for i, instance := range instances {
			t.Logf("Cleaning up container %d...", i+1)
			if err := runtime.Stop(ctx, instance.ContainerID); err != nil {
				t.Errorf("Stop() cleanup for instance %d error = %v", i+1, err)
			}
		}
	}()

	// Verify all containers are running
	time.Sleep(2 * time.Second)

	for i, instance := range instances {
		info, err := runtime.Inspect(ctx, instance.ContainerID)
		if err != nil {
			t.Errorf("Inspect() for instance %d error = %v", i+1, err)
			continue
		}
		if info.State != "running" {
			t.Errorf("Instance %d State = %s, want running", i+1, info.State)
		} else {
			t.Logf("Instance %d running on port %d", i+1, instance.Port)
		}
	}
}

// TestCRIU_RestorePerformance measures CRIU restore time.
func TestCRIU_RestorePerformance(t *testing.T) {
	runtime, checkpointPath := skipIfNoCRIU(t)
	defer runtime.Close()

	ctx := context.Background()
	testPort := 32220

	opts := RestoreOptions{
		CheckpointPath: checkpointPath,
		Name:           "criu-perf-test",
		Hostname:       "criu-perf-host",
		Port:           testPort,
		DBPrefix:       "criuperf_",
	}

	t.Log("Measuring CRIU restore performance...")
	start := time.Now()
	instance, err := runtime.RestoreFromCheckpoint(ctx, opts)
	restoreTime := time.Since(start)

	if err != nil {
		t.Fatalf("RestoreFromCheckpoint() error = %v", err)
	}
	defer func() {
		_ = runtime.Stop(ctx, instance.ContainerID)
	}()

	t.Logf("CRIU restore completed in %v", restoreTime)

	// CRIU restore should be fast - warn if it takes too long
	if restoreTime > 5*time.Second {
		t.Logf("WARNING: CRIU restore took longer than expected (%v > 5s)", restoreTime)
	}

	// Now measure time to health check
	start = time.Now()
	timeout := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatalf("Timed out waiting for healthy after %v", time.Since(start))
		case <-ticker.C:
			healthy, err := runtime.HealthCheck(ctx, instance.ContainerID)
			if err == nil && healthy {
				healthyTime := time.Since(start)
				t.Logf("Container became healthy in %v (restore: %v)", healthyTime, restoreTime)
				t.Logf("Total time to ready: %v", restoreTime+healthyTime)
				return
			}
		}
	}
}
