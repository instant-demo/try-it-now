package container

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
)

// skipIfNoDocker skips the test if Docker is not available.
func skipIfNoDocker(t *testing.T) *DockerRuntime {
	t.Helper()
	if os.Getenv("DOCKER_TEST") == "" {
		t.Skip("Skipping Docker integration test. Set DOCKER_TEST=1 to run.")
	}

	containerCfg := &config.ContainerConfig{
		Mode:           "docker",
		Image:          "nginx:alpine", // Use nginx for testing - faster than PrestaShop
		Network:        "",
		PortRangeStart: 32000,
		PortRangeEnd:   32099,
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

	runtime, err := NewDockerRuntime(containerCfg, psCfg, proxyCfg)
	if err != nil {
		t.Skipf("Failed to connect to Docker: %v", err)
	}

	return runtime
}

func TestDockerRuntime_RestoreFromCheckpoint_NotSupported(t *testing.T) {
	runtime := skipIfNoDocker(t)
	defer runtime.Close()

	ctx := context.Background()
	opts := RestoreOptions{
		CheckpointPath: "/some/path",
		Name:           "test",
		Hostname:       "test",
		Port:           32000,
	}

	_, err := runtime.RestoreFromCheckpoint(ctx, opts)
	if err != ErrCRIUNotSupported {
		t.Errorf("RestoreFromCheckpoint() error = %v, want %v", err, ErrCRIUNotSupported)
	}
}

func TestDockerRuntime_StartAndStop(t *testing.T) {
	runtime := skipIfNoDocker(t)
	defer runtime.Close()

	ctx := context.Background()

	opts := StartOptions{
		Image:    "nginx:alpine",
		Name:     "demo-test-" + time.Now().Format("20060102150405"),
		Hostname: "test-host",
		Port:     32050,
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

func TestDockerRuntime_Inspect_NotFound(t *testing.T) {
	runtime := skipIfNoDocker(t)
	defer runtime.Close()

	ctx := context.Background()

	_, err := runtime.Inspect(ctx, "nonexistent-container-id")
	if err != domain.ErrContainerNotFound {
		t.Errorf("Inspect() error = %v, want %v", err, domain.ErrContainerNotFound)
	}
}

func TestDockerRuntime_HealthCheck(t *testing.T) {
	runtime := skipIfNoDocker(t)
	defer runtime.Close()

	ctx := context.Background()

	opts := StartOptions{
		Image:    "nginx:alpine",
		Name:     "demo-health-test-" + time.Now().Format("20060102150405"),
		Hostname: "health-test",
		Port:     32051,
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

	// Wait for nginx to start
	time.Sleep(3 * time.Second)

	// Health check should pass for nginx
	healthy, err := runtime.HealthCheck(ctx, instance.ContainerID)
	if err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}

	if !healthy {
		t.Error("HealthCheck() returned false, expected true for running nginx")
	}
}

func TestDockerRuntime_Stop_Idempotent(t *testing.T) {
	runtime := skipIfNoDocker(t)
	defer runtime.Close()

	ctx := context.Background()

	// Stop a non-existent container should not error
	err := runtime.Stop(ctx, "nonexistent-container-12345")
	if err != nil {
		t.Errorf("Stop() on non-existent container error = %v, want nil", err)
	}
}

func TestBuildEnvVars(t *testing.T) {
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

	runtime := &DockerRuntime{
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

	expected := map[string]bool{
		"PS_DOMAIN=demo-abc.example.com":   true,
		"PS_ENABLE_SSL=1":                  true,
		"PS_FOLDER_ADMIN=admin123":         true,
		"PS_FOLDER_INSTALL=install-disabled": true,
		"DB_SERVER=db.example.com":         true,
		"DB_PORT=3306":                     true,
		"DB_NAME=testdb":                   true,
		"DB_USER=testuser":                 true,
		"DB_PASSWD=testpass":               true,
		"DB_PREFIX=abc_":                   true,
		"ADMIN_MAIL=demo@test.com":         true,
		"ADMIN_PASSWD=demopass":            true,
		"CUSTOM_VAR=custom_value":          true,
	}

	for _, e := range env {
		if !expected[e] {
			t.Errorf("Unexpected env var: %s", e)
		}
		delete(expected, e)
	}

	for e := range expected {
		t.Errorf("Missing expected env var: %s", e)
	}
}

func TestGenerateInstanceID(t *testing.T) {
	id1 := generateInstanceID()
	id2 := generateInstanceID()

	if id1 == id2 {
		t.Error("generateInstanceID() returned same ID twice")
	}

	if len(id1) < 10 {
		t.Errorf("generateInstanceID() returned too short ID: %s", id1)
	}
}
