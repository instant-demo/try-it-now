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

// getTestSocketPath returns the Podman socket path for testing.
func getTestSocketPath() string {
	if path := os.Getenv("PODMAN_SOCKET_PATH"); path != "" {
		return path
	}
	// Default rootful socket
	return "unix:///run/podman/podman.sock"
}

// skipIfNoPodman skips the test if Podman is not available.
func skipIfNoPodman(t *testing.T) *PodmanRuntime {
	t.Helper()
	if os.Getenv("PODMAN_TEST") == "" {
		t.Skip("Skipping Podman integration test. Set PODMAN_TEST=1 to run.")
	}

	containerCfg := &config.ContainerConfig{
		Mode:             "podman",
		Image:            "nginx:alpine", // Use nginx for testing - faster than PrestaShop
		Network:          "",
		PortRangeStart:   32100,
		PortRangeEnd:     32199,
		PodmanSocketPath: getTestSocketPath(),
		CRIUEnabled:      false, // Disable CRIU for basic tests
	}
	psCfg := &config.PrestaShopConfig{
		DBHost:     "localhost",
		DBPort:     3306,
		DBName:     "prestashop_demos",
		DBUser:     "demo",
		DBPassword: "devpass",
		AdminPath:  "admin-demo",
		DemoUser:   "demo@demo.com",
		DemoPass:   "demodemo",
	}
	proxyCfg := &config.ProxyConfig{
		CaddyAdminURL: "http://localhost:2019",
		BaseDomain:    "localhost",
	}

	runtime, err := NewPodmanRuntime(containerCfg, psCfg, proxyCfg, logging.Nop())
	if err != nil {
		t.Skipf("Failed to connect to Podman: %v", err)
	}

	return runtime
}

func TestPodmanRuntime_RestoreFromCheckpoint_NoCRIU(t *testing.T) {
	runtime := skipIfNoPodman(t)
	defer runtime.Close()

	// Ensure CRIU is disabled for this test
	runtime.criuAvailable = false

	ctx := context.Background()
	opts := RestoreOptions{
		CheckpointPath: "/some/path",
		Name:           "test",
		Hostname:       "test",
		Port:           32160,
	}

	_, err := runtime.RestoreFromCheckpoint(ctx, opts)
	if err != domain.ErrCRIUNotAvailable {
		t.Errorf("RestoreFromCheckpoint() error = %v, want %v", err, domain.ErrCRIUNotAvailable)
	}
}

func TestPodmanRuntime_StartAndStop(t *testing.T) {
	runtime := skipIfNoPodman(t)
	defer runtime.Close()

	ctx := context.Background()

	opts := StartOptions{
		Image:    "nginx:alpine",
		Name:     "podman-test-" + time.Now().Format("20060102150405"),
		Hostname: "test-host",
		Port:     32150,
		DBPrefix: "test_",
		Labels: map[string]string{
			"app": "demo-multiplexer-test",
		},
	}

	// Start container
	instance, err := runtime.Start(ctx, opts)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Ensure cleanup
	defer func() {
		_ = runtime.Stop(ctx, instance.ContainerID)
	}()

	if instance.ContainerID == "" {
		t.Error("Start() returned empty ContainerID")
	}
	if instance.Port != opts.Port {
		t.Errorf("Start() Port = %d, want %d", instance.Port, opts.Port)
	}
	if instance.Hostname != opts.Hostname {
		t.Errorf("Start() Hostname = %s, want %s", instance.Hostname, opts.Hostname)
	}
	if instance.State != domain.StateWarming {
		t.Errorf("Start() State = %v, want %v", instance.State, domain.StateWarming)
	}

	// Wait for container to be running
	time.Sleep(2 * time.Second)

	// Inspect
	info, err := runtime.Inspect(ctx, instance.ContainerID)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	if info.State != "running" {
		t.Errorf("Inspect() State = %s, want running", info.State)
	}

	// Stop
	if err := runtime.Stop(ctx, instance.ContainerID); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	// Verify stopped
	_, err = runtime.Inspect(ctx, instance.ContainerID)
	if err != domain.ErrContainerNotFound {
		t.Errorf("Inspect() after stop error = %v, want %v", err, domain.ErrContainerNotFound)
	}
}

func TestPodmanRuntime_Inspect_NotFound(t *testing.T) {
	runtime := skipIfNoPodman(t)
	defer runtime.Close()

	ctx := context.Background()

	_, err := runtime.Inspect(ctx, "nonexistent-container-id")
	if err != domain.ErrContainerNotFound {
		t.Errorf("Inspect() error = %v, want %v", err, domain.ErrContainerNotFound)
	}
}

func TestPodmanRuntime_HealthCheck(t *testing.T) {
	runtime := skipIfNoPodman(t)
	defer runtime.Close()

	ctx := context.Background()

	opts := StartOptions{
		Image:    "nginx:alpine",
		Name:     "podman-health-test-" + time.Now().Format("20060102150405"),
		Hostname: "health-test",
		Port:     32151,
		DBPrefix: "test_",
	}

	// Start container
	instance, err := runtime.Start(ctx, opts)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		_ = runtime.Stop(ctx, instance.ContainerID)
	}()

	// Poll for health using the same pattern as production code
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Brief initial delay (start period)
	time.Sleep(1 * time.Second)

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

func TestPodmanRuntime_Stop_Idempotent(t *testing.T) {
	runtime := skipIfNoPodman(t)
	defer runtime.Close()

	ctx := context.Background()

	// Stop a non-existent container should not error
	err := runtime.Stop(ctx, "nonexistent-container-12345")
	if err != nil {
		t.Errorf("Stop() on non-existent container error = %v, want nil", err)
	}
}

func TestPodmanRuntime_CRIUAvailability(t *testing.T) {
	// This test checks CRIU detection without requiring CRIU to be installed
	containerCfg := &config.ContainerConfig{
		Mode:             "podman",
		Image:            "nginx:alpine",
		PodmanSocketPath: getTestSocketPath(),
		CRIUEnabled:      true,
		CheckpointPath:   "/nonexistent/checkpoint.tar.gz",
	}
	psCfg := &config.PrestaShopConfig{}
	proxyCfg := &config.ProxyConfig{BaseDomain: "localhost"}

	if os.Getenv("PODMAN_TEST") == "" {
		t.Skip("Skipping Podman integration test. Set PODMAN_TEST=1 to run.")
	}

	runtime, err := NewPodmanRuntime(containerCfg, psCfg, proxyCfg, logging.Nop())
	if err != nil {
		t.Skipf("Failed to connect to Podman: %v", err)
	}
	defer runtime.Close()

	// CRIU should be unavailable (checkpoint doesn't exist, or not root, or CRIU not installed)
	if runtime.CRIUAvailable() {
		t.Log("CRIU is available - checkpoint file exists and CRIU is installed")
	} else {
		t.Log("CRIU is not available as expected (checkpoint missing, not root, or CRIU not installed)")
	}
}

func TestPodmanRuntime_BuildEnvVars(t *testing.T) {
	psCfg := &config.PrestaShopConfig{
		DBHost:     "db.example.com",
		DBPort:     3306,
		DBName:     "testdb",
		DBUser:     "testuser",
		DBPassword: "testpass",
		AdminPath:  "admin123",
		DemoUser:   "demo@test.com",
		DemoPass:   "demopass",
	}
	proxyCfg := &config.ProxyConfig{
		BaseDomain: "example.com",
	}

	runtime := &PodmanRuntime{
		psCfg:    psCfg,
		proxyCfg: proxyCfg,
	}

	opts := StartOptions{
		Hostname: "demo-abc",
		DBPrefix: "abc_",
		EnvVars: map[string]string{
			"CUSTOM_VAR": "custom_value",
		},
	}

	env := runtime.buildEnvVars(opts)

	expected := map[string]string{
		"PS_DOMAIN":         "demo-abc.example.com",
		"PS_ENABLE_SSL":     "1",
		"PS_FOLDER_ADMIN":   "admin123",
		"PS_FOLDER_INSTALL": "install-disabled",
		"DB_SERVER":         "db.example.com",
		"DB_PORT":           "3306",
		"DB_NAME":           "testdb",
		"DB_USER":           "testuser",
		"DB_PASSWD":         "testpass",
		"DB_PREFIX":         "abc_",
		"ADMIN_MAIL":        "demo@test.com",
		"ADMIN_PASSWD":      "demopass",
		"CUSTOM_VAR":        "custom_value",
	}

	for k, want := range expected {
		if got := env[k]; got != want {
			t.Errorf("env[%s] = %s, want %s", k, got, want)
		}
	}
}

func TestIsVersionSupported(t *testing.T) {
	runtime := &PodmanRuntime{}

	tests := []struct {
		version string
		want    bool
	}{
		{"3.11", true},
		{"3.17.1", true},
		{"4.0", true},
		{"3.10", false},
		{"3.9.5", false},
		{"2.15", false},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := runtime.isVersionSupported(tt.version); got != tt.want {
				t.Errorf("isVersionSupported(%s) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestExtractVersion(t *testing.T) {
	runtime := &PodmanRuntime{}

	tests := []struct {
		output string
		want   string
	}{
		{"Version: 3.17.1\n", "3.17.1"},
		{"Version: 3.11\n", "3.11"},
		{"3.17.1", "3.17.1"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.output, func(t *testing.T) {
			if got := runtime.extractVersion(tt.output); got != tt.want {
				t.Errorf("extractVersion(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}
