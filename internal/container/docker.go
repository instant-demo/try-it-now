package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// ErrCRIUNotSupported is returned when attempting CRIU restore in Docker mode.
var ErrCRIUNotSupported = errors.New("CRIU checkpoint restore not supported in Docker mode")

// DockerRuntime implements Runtime using the Docker SDK.
// This is the development/fallback mode without CRIU support.
type DockerRuntime struct {
	client    *client.Client
	cfg       *config.ContainerConfig
	psCfg     *config.PrestaShopConfig
	proxyCfg  *config.ProxyConfig
	networkID string
}

// NewDockerRuntime creates a new Docker-based runtime.
func NewDockerRuntime(cfg *config.ContainerConfig, psCfg *config.PrestaShopConfig, proxyCfg *config.ProxyConfig) (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &DockerRuntime{
		client:   cli,
		cfg:      cfg,
		psCfg:    psCfg,
		proxyCfg: proxyCfg,
	}, nil
}

// Close closes the Docker client connection.
func (r *DockerRuntime) Close() error {
	return r.client.Close()
}

// RestoreFromCheckpoint is not supported in Docker mode.
// Use Podman+CRIU for checkpoint/restore functionality.
func (r *DockerRuntime) RestoreFromCheckpoint(ctx context.Context, opts RestoreOptions) (*domain.Instance, error) {
	return nil, ErrCRIUNotSupported
}

// Start creates and starts a new container.
func (r *DockerRuntime) Start(ctx context.Context, opts StartOptions) (*domain.Instance, error) {
	// Build environment variables
	env := r.buildEnvVars(opts)

	// Container port (PrestaShop typically runs on 80)
	containerPort := nat.Port("80/tcp")

	// Port bindings
	portBindings := nat.PortMap{
		containerPort: []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: strconv.Itoa(opts.Port),
			},
		},
	}

	// Exposed ports
	exposedPorts := nat.PortSet{
		containerPort: struct{}{},
	}

	// Container config
	containerCfg := &container.Config{
		Image:        opts.Image,
		Hostname:     opts.Hostname,
		Env:          env,
		ExposedPorts: exposedPorts,
		Labels:       opts.Labels,
	}

	// Host config
	hostCfg := &container.HostConfig{
		PortBindings: portBindings,
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	}

	// Network config
	var networkCfg *network.NetworkingConfig
	if opts.NetworkID != "" {
		networkCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				opts.NetworkID: {},
			},
		}
	}

	// Create container
	resp, err := r.client.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, opts.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := r.client.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up the created container on start failure
		_ = r.client.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Build and return instance
	instance := &domain.Instance{
		ID:          generateInstanceID(),
		ContainerID: resp.ID,
		Hostname:    opts.Hostname,
		Port:        opts.Port,
		State:       domain.StateWarming,
		DBPrefix:    opts.DBPrefix,
		CreatedAt:   time.Now(),
	}

	return instance, nil
}

// Stop stops and removes a container.
func (r *DockerRuntime) Stop(ctx context.Context, containerID string) error {
	// Stop with timeout
	timeout := 10
	if err := r.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		// Ignore "not found" errors - container might already be stopped/removed
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	// Remove container
	if err := r.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		if !client.IsErrNotFound(err) {
			return fmt.Errorf("failed to remove container: %w", err)
		}
	}

	return nil
}

// Inspect returns information about a running container.
func (r *DockerRuntime) Inspect(ctx context.Context, containerID string) (*ContainerInfo, error) {
	inspect, err := r.client.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, domain.ErrContainerNotFound
		}
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	// Build port mapping
	ports := make(map[int]int)
	for containerPort, bindings := range inspect.NetworkSettings.Ports {
		if len(bindings) > 0 {
			containerPortNum, _ := strconv.Atoi(containerPort.Port())
			hostPort, _ := strconv.Atoi(bindings[0].HostPort)
			ports[containerPortNum] = hostPort
		}
	}

	// Get IP address
	ipAddress := ""
	if inspect.NetworkSettings != nil {
		ipAddress = inspect.NetworkSettings.IPAddress
		// If using custom network, get IP from that network
		for _, netSettings := range inspect.NetworkSettings.Networks {
			if netSettings.IPAddress != "" {
				ipAddress = netSettings.IPAddress
				break
			}
		}
	}

	return &ContainerInfo{
		ID:        inspect.ID,
		Name:      inspect.Name,
		State:     inspect.State.Status,
		IPAddress: ipAddress,
		Ports:     ports,
	}, nil
}

// HealthCheck checks if the container is responding to HTTP requests.
// It first performs a TCP connect check (faster) to verify the process is listening,
// then makes an HTTP request to verify the application responds.
func (r *DockerRuntime) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	info, err := r.Inspect(ctx, containerID)
	if err != nil {
		return false, err
	}

	if info.State != "running" {
		return false, nil
	}

	// Check if the container responds on port 80
	hostPort, ok := info.Ports[80]
	if !ok {
		return false, nil
	}

	addr := fmt.Sprintf("localhost:%d", hostPort)

	// First: TCP connect check (faster than HTTP, catches "connection refused" quickly)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		// Log but don't fail - connection refused is expected during startup
		log.Printf("Health check TCP failed for %s: %v", containerID[:12], err)
		return false, nil
	}
	conn.Close()

	// Then: HTTP GET to verify application responds
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	url := fmt.Sprintf("http://%s/", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, nil
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Health check HTTP failed for %s: %v", containerID[:12], err)
		return false, nil
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// Any response (even 302 redirect) means the container is healthy
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 500
	if !healthy {
		log.Printf("Health check HTTP status %d for %s", resp.StatusCode, containerID[:12])
	}
	return healthy, nil
}

// buildEnvVars constructs the environment variables for a PrestaShop container.
func (r *DockerRuntime) buildEnvVars(opts StartOptions) []string {
	env := []string{
		fmt.Sprintf("PS_DOMAIN=%s.%s", opts.Hostname, r.proxyCfg.BaseDomain),
		fmt.Sprintf("PS_ENABLE_SSL=1"),
		fmt.Sprintf("PS_FOLDER_ADMIN=%s", r.psCfg.AdminPath),
		fmt.Sprintf("PS_FOLDER_INSTALL=install-disabled"),
		fmt.Sprintf("DB_SERVER=%s", r.psCfg.DBHost),
		fmt.Sprintf("DB_PORT=%d", r.psCfg.DBPort),
		fmt.Sprintf("DB_NAME=%s", r.psCfg.DBName),
		fmt.Sprintf("DB_USER=%s", r.psCfg.DBUser),
		fmt.Sprintf("DB_PASSWD=%s", r.psCfg.DBPassword),
		fmt.Sprintf("DB_PREFIX=%s", opts.DBPrefix),
		fmt.Sprintf("ADMIN_MAIL=%s", r.psCfg.DemoUser),
		fmt.Sprintf("ADMIN_PASSWD=%s", r.psCfg.DemoPass),
	}

	// Add any custom env vars from options
	for k, v := range opts.EnvVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// generateInstanceID creates a unique instance ID.
func generateInstanceID() string {
	// Use timestamp-based ID for simplicity
	// In production, consider using UUID or similar
	return fmt.Sprintf("demo-%d", time.Now().UnixNano())
}

// Compile-time check that DockerRuntime implements Runtime
var _ Runtime = (*DockerRuntime)(nil)
