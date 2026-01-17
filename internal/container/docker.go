package container

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/internal/domain"
	"github.com/instant-demo/try-it-now/pkg/logging"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
)

// ErrCRIUNotSupported is returned when attempting CRIU restore in Docker mode.
var ErrCRIUNotSupported = errors.New("CRIU checkpoint restore not supported in Docker mode")

// DockerRuntime implements Runtime using the Docker SDK.
// This is the development/fallback mode without CRIU support.
type DockerRuntime struct {
	client       *client.Client
	cfg          *config.ContainerConfig
	psCfg        *config.PrestaShopConfig
	proxyCfg     *config.ProxyConfig
	networkID    string
	logger       *logging.Logger
	healthClient *http.Client // Shared HTTP client for health checks
}

// NewDockerRuntime creates a new Docker-based runtime.
func NewDockerRuntime(cfg *config.ContainerConfig, psCfg *config.PrestaShopConfig, proxyCfg *config.ProxyConfig, logger *logging.Logger) (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &DockerRuntime{
		client:   cli,
		cfg:      cfg,
		psCfg:    psCfg,
		proxyCfg: proxyCfg,
		logger:   logger.With("component", "docker"),
		healthClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
			// Don't follow redirects - a 302 response means the container is healthy
			// (PrestaShop redirects HTTP to HTTPS which we can't follow without valid certs)
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
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
				HostIP:   "127.0.0.1",
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
		// Mount MySQL client config to disable SSL (required for prestashop-flashlight)
		// Mount to /etc/my.cnf which is the global config that ALL MariaDB clients read
		Binds: []string{
			"/etc/mysql-client-no-ssl.cnf:/etc/my.cnf:ro",
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
		// Clean up container on start failure. Error ignored because container may not
		// have been fully created, and orphaned containers are cleaned by external process.
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

	return CheckContainerHealth(ctx, r.healthClient, hostPort, containerID, r.logger)
}

// buildEnvVars constructs the environment variables for a PrestaShop container.
// WARNING: Contains sensitive data (MYSQL_PASSWORD, ADMIN_PASSWORD_OVERRIDE). Never log this output.
// Uses prestashop-flashlight env var names (MYSQL_* prefix).
func (r *DockerRuntime) buildEnvVars(opts StartOptions) []string {
	env := []string{
		// Required by prestashop-flashlight
		fmt.Sprintf("PS_DOMAIN=%s.%s", opts.Hostname, r.proxyCfg.BaseDomain),
		// Database connection (prestashop-flashlight uses MYSQL_* prefix)
		fmt.Sprintf("MYSQL_HOST=%s", r.psCfg.DBHost),
		fmt.Sprintf("MYSQL_PORT=%d", r.psCfg.DBPort),
		fmt.Sprintf("MYSQL_DATABASE=%s", r.psCfg.DBName),
		fmt.Sprintf("MYSQL_USER=%s", r.psCfg.DBUser),
		fmt.Sprintf("MYSQL_PASSWORD=%s", r.psCfg.DBPassword),
		// Admin credentials override
		fmt.Sprintf("ADMIN_MAIL_OVERRIDE=%s", r.psCfg.DemoUser),
		fmt.Sprintf("ADMIN_PASSWORD_OVERRIDE=%s", r.psCfg.DemoPass),
	}

	// Add any custom env vars from options
	for k, v := range opts.EnvVars {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// generateInstanceID creates a unique instance ID using UUID.
// UUIDs prevent ID enumeration attacks and information leakage.
func generateInstanceID() string {
	return fmt.Sprintf("demo-%s", uuid.New().String())
}

// Compile-time check that DockerRuntime implements Runtime
var _ Runtime = (*DockerRuntime)(nil)
