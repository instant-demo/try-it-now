package container

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/system"
	"github.com/containers/podman/v5/pkg/specgen"
	nettypes "go.podman.io/common/libnetwork/types"
)

// MinCRIUVersion is the minimum CRIU version required (3.11 = 31100).
const MinCRIUVersion = 31100

// PodmanRuntime implements Runtime using the Podman API with CRIU support.
type PodmanRuntime struct {
	conn          context.Context // Podman connection context
	cfg           *config.ContainerConfig
	psCfg         *config.PrestaShopConfig
	proxyCfg      *config.ProxyConfig
	criuAvailable bool
	criuVersion   string
}

// NewPodmanRuntime creates a new Podman-based runtime with optional CRIU support.
func NewPodmanRuntime(cfg *config.ContainerConfig, psCfg *config.PrestaShopConfig, proxyCfg *config.ProxyConfig) (*PodmanRuntime, error) {
	// Establish connection to Podman socket
	conn, err := bindings.NewConnection(context.Background(), cfg.PodmanSocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Podman socket at %s: %w", cfg.PodmanSocketPath, err)
	}

	runtime := &PodmanRuntime{
		conn:     conn,
		cfg:      cfg,
		psCfg:    psCfg,
		proxyCfg: proxyCfg,
	}

	// Check CRIU availability if enabled
	if cfg.CRIUEnabled {
		runtime.checkCRIUAvailability()
	}

	return runtime, nil
}

// checkCRIUAvailability verifies CRIU is available via Podman API.
// This checks the Podman host info rather than local process UID, which is
// essential for macOS where Go runs as user but Podman VM runs rootful.
func (r *PodmanRuntime) checkCRIUAvailability() {
	// Get Podman system info via API
	info, err := system.Info(r.conn, nil)
	if err != nil {
		log.Printf("Warning: Failed to get Podman info - CRIU disabled: %v", err)
		r.criuAvailable = false
		return
	}

	// Check if Podman is running rootful (required for CRIU)
	if info.Host.Security.Rootless {
		log.Printf("Warning: Podman is rootless - CRIU checkpoint/restore disabled")
		r.criuAvailable = false
		return
	}

	// Check if OCI runtime has CRIU support compiled in
	// crun reports "+CRIU" in version string when CRIU is available
	if info.Host.OCIRuntime == nil || !strings.Contains(info.Host.OCIRuntime.Version, "+CRIU") {
		log.Printf("Warning: OCI runtime does not have CRIU support - checkpoint/restore disabled")
		r.criuAvailable = false
		return
	}

	// CRIU is available via the OCI runtime
	r.criuVersion = "available"

	// Check checkpoint file exists
	if _, err := os.Stat(r.cfg.CheckpointPath); os.IsNotExist(err) {
		log.Printf("Warning: Checkpoint file not found at %s - will use Start() fallback", r.cfg.CheckpointPath)
		r.criuAvailable = false
		return
	}

	r.criuAvailable = true
	log.Printf("CRIU available via %s - checkpoint/restore enabled", info.Host.OCIRuntime.Name)
}

// extractVersion extracts version string from CRIU output.
func (r *PodmanRuntime) extractVersion(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Version:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	// Try direct parse if format is different
	return strings.TrimSpace(output)
}

// isVersionSupported checks if CRIU version >= 3.11.
func (r *PodmanRuntime) isVersionSupported(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > 3 || (major == 3 && minor >= 11)
}

// Close is a no-op for Podman (connection is context-based).
func (r *PodmanRuntime) Close() error {
	// Podman bindings don't require explicit close
	return nil
}

// CRIUAvailable returns whether CRIU restore is available.
func (r *PodmanRuntime) CRIUAvailable() bool {
	return r.criuAvailable
}

// RestoreFromCheckpoint restores a container from a CRIU checkpoint.
func (r *PodmanRuntime) RestoreFromCheckpoint(ctx context.Context, opts RestoreOptions) (*domain.Instance, error) {
	if !r.criuAvailable {
		return nil, domain.ErrCRIUNotAvailable
	}

	// Verify checkpoint exists
	if _, err := os.Stat(opts.CheckpointPath); os.IsNotExist(err) {
		return nil, domain.ErrCheckpointNotFound
	}

	// Generate unique container name to avoid conflicts
	containerName := fmt.Sprintf("%s-%d", opts.Name, time.Now().UnixNano())

	// Configure restore options
	// Using --tcp-close to close stale TCP connections (allows custom naming/ports)
	// PrestaShop will re-establish database connections on first request
	restoreOpts := new(containers.RestoreOptions).
		WithImportArchive(opts.CheckpointPath).
		WithName(containerName).
		WithIgnoreStaticIP(true).  // Get new IP address
		WithIgnoreStaticMAC(true). // Get new MAC address
		WithPublishPorts([]string{fmt.Sprintf("%d:80", opts.Port)})

	// Perform restore
	report, err := containers.Restore(r.conn, "", restoreOpts)
	if err != nil {
		return nil, fmt.Errorf("CRIU restore failed: %w", err)
	}

	// Build instance from restored container
	instance := &domain.Instance{
		ID:          generateInstanceID(),
		ContainerID: report.Id,
		Hostname:    opts.Hostname,
		Port:        opts.Port,
		State:       domain.StateWarming,
		DBPrefix:    opts.DBPrefix,
		CreatedAt:   time.Now(),
	}

	return instance, nil
}

// Start creates and starts a new container without checkpoint (fallback mode).
func (r *PodmanRuntime) Start(ctx context.Context, opts StartOptions) (*domain.Instance, error) {
	// Build environment variables
	env := r.buildEnvVars(opts)

	// Create specgen for container
	s := specgen.NewSpecGenerator(opts.Image, false)
	s.Name = opts.Name
	s.Hostname = opts.Hostname
	s.Env = env
	s.Labels = opts.Labels

	// Port mapping
	s.PortMappings = []nettypes.PortMapping{
		{
			ContainerPort: 80,
			HostPort:      uint16(opts.Port),
			HostIP:        "0.0.0.0",
			Protocol:      "tcp",
		},
	}

	// Network configuration
	if opts.NetworkID != "" {
		s.Networks = map[string]nettypes.PerNetworkOptions{
			opts.NetworkID: {},
		}
	}

	// Create container
	createResponse, err := containers.CreateWithSpec(r.conn, s, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := containers.Start(r.conn, createResponse.ID, nil); err != nil {
		// Cleanup on failure
		_, _ = containers.Remove(r.conn, createResponse.ID, new(containers.RemoveOptions).WithForce(true))
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	instance := &domain.Instance{
		ID:          generateInstanceID(),
		ContainerID: createResponse.ID,
		Hostname:    opts.Hostname,
		Port:        opts.Port,
		State:       domain.StateWarming,
		DBPrefix:    opts.DBPrefix,
		CreatedAt:   time.Now(),
	}

	return instance, nil
}

// Stop stops and removes a container.
func (r *PodmanRuntime) Stop(ctx context.Context, containerID string) error {
	// Stop container with timeout
	timeout := uint(10)
	stopOpts := new(containers.StopOptions).WithTimeout(timeout).WithIgnore(true)
	if err := containers.Stop(r.conn, containerID, stopOpts); err != nil {
		// Log but continue to remove
		if !strings.Contains(err.Error(), "no such container") {
			log.Printf("Warning: failed to stop container %s: %v", containerID, err)
		}
	}

	// Remove container
	removeOpts := new(containers.RemoveOptions).WithForce(true).WithIgnore(true)
	if _, err := containers.Remove(r.conn, containerID, removeOpts); err != nil {
		if !strings.Contains(err.Error(), "no such container") {
			return fmt.Errorf("failed to remove container: %w", err)
		}
	}

	return nil
}

// Inspect returns information about a running container.
func (r *PodmanRuntime) Inspect(ctx context.Context, containerID string) (*ContainerInfo, error) {
	data, err := containers.Inspect(r.conn, containerID, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no such container") {
			return nil, domain.ErrContainerNotFound
		}
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	// Build port mapping
	ports := make(map[int]int)
	for portProto, bindings := range data.NetworkSettings.Ports {
		if len(bindings) > 0 {
			// Parse "80/tcp" format
			portStr := strings.Split(string(portProto), "/")[0]
			containerPort, _ := strconv.Atoi(portStr)
			hostPort, _ := strconv.Atoi(bindings[0].HostPort)
			ports[containerPort] = hostPort
		}
	}

	// Get IP address
	ipAddress := ""
	if data.NetworkSettings != nil {
		ipAddress = data.NetworkSettings.IPAddress
		for _, netSettings := range data.NetworkSettings.Networks {
			if netSettings.IPAddress != "" {
				ipAddress = netSettings.IPAddress
				break
			}
		}
	}

	return &ContainerInfo{
		ID:        data.ID,
		Name:      data.Name,
		State:     data.State.Status,
		IPAddress: ipAddress,
		Ports:     ports,
	}, nil
}

// HealthCheck checks if the container is responding to HTTP requests.
func (r *PodmanRuntime) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	info, err := r.Inspect(ctx, containerID)
	if err != nil {
		return false, err
	}

	if info.State != "running" {
		return false, nil
	}

	hostPort, ok := info.Ports[80]
	if !ok {
		return false, nil
	}

	addr := fmt.Sprintf("localhost:%d", hostPort)

	// TCP check first (faster, catches connection refused quickly)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		log.Printf("Health check TCP failed for %s: %v", containerID[:12], err)
		return false, nil
	}
	conn.Close()

	// HTTP GET to verify application responds
	httpClient := &http.Client{Timeout: 5 * time.Second}
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

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 500
	if !healthy {
		log.Printf("Health check HTTP status %d for %s", resp.StatusCode, containerID[:12])
	}
	return healthy, nil
}

// buildEnvVars constructs environment variables for PrestaShop container.
func (r *PodmanRuntime) buildEnvVars(opts StartOptions) map[string]string {
	env := map[string]string{
		"PS_DOMAIN":         fmt.Sprintf("%s.%s", opts.Hostname, r.proxyCfg.BaseDomain),
		"PS_ENABLE_SSL":     "1",
		"PS_FOLDER_ADMIN":   r.psCfg.AdminPath,
		"PS_FOLDER_INSTALL": "install-disabled",
		"DB_SERVER":         r.psCfg.DBHost,
		"DB_PORT":           strconv.Itoa(r.psCfg.DBPort),
		"DB_NAME":           r.psCfg.DBName,
		"DB_USER":           r.psCfg.DBUser,
		"DB_PASSWD":         r.psCfg.DBPassword,
		"DB_PREFIX":         opts.DBPrefix,
		"ADMIN_MAIL":        r.psCfg.DemoUser,
		"ADMIN_PASSWD":      r.psCfg.DemoPass,
	}

	// Add custom env vars
	for k, v := range opts.EnvVars {
		env[k] = v
	}

	return env
}

// Compile-time check that PodmanRuntime implements Runtime
var _ Runtime = (*PodmanRuntime)(nil)
