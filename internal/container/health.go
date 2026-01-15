package container

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/boss/demo-multiplexer/pkg/logging"
)

// HealthCheckConfig contains configuration for container health checks.
type HealthCheckConfig struct {
	TCPTimeout  time.Duration
	HTTPTimeout time.Duration
}

// DefaultHealthCheckConfig returns sensible defaults for health checking.
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		TCPTimeout:  2 * time.Second,
		HTTPTimeout: 5 * time.Second,
	}
}

// CheckContainerHealth checks if a container is healthy by performing TCP and HTTP checks.
// It first performs a TCP connect check (faster) to verify the process is listening,
// then makes an HTTP request to verify the application responds.
//
// Returns:
//   - true if both TCP and HTTP checks pass
//   - false if either check fails (with no error - this is expected during startup)
//   - error only for unexpected failures (not connection refused or timeout)
func CheckContainerHealth(ctx context.Context, client *http.Client, port int, containerID string, logger *logging.Logger) (bool, error) {
	return CheckContainerHealthWithConfig(ctx, client, port, containerID, logger, DefaultHealthCheckConfig())
}

// CheckContainerHealthWithConfig performs health check with custom configuration.
func CheckContainerHealthWithConfig(ctx context.Context, client *http.Client, port int, containerID string, logger *logging.Logger, cfg HealthCheckConfig) (bool, error) {
	addr := fmt.Sprintf("localhost:%d", port)

	// Truncate container ID for logging (first 12 chars like Docker does)
	shortID := containerID
	if len(containerID) > 12 {
		shortID = containerID[:12]
	}

	// First: TCP connect check (faster than HTTP, catches "connection refused" quickly)
	conn, err := net.DialTimeout("tcp", addr, cfg.TCPTimeout)
	if err != nil {
		// Log but don't fail - connection refused is expected during startup
		logger.Debug("Health check TCP failed", "containerID", shortID, "error", err)
		return false, nil
	}
	conn.Close()

	// Then: HTTP GET to verify application responds
	url := fmt.Sprintf("http://%s/", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, nil
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("Health check HTTP failed", "containerID", shortID, "error", err)
		return false, nil
	}
	defer func() {
		// Drain body to allow connection reuse
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// Any response in 2xx-4xx range means the container is healthy
	// 5xx indicates server error, which means not ready
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 500
	if !healthy {
		logger.Debug("Health check HTTP unexpected status", "containerID", shortID, "status", resp.StatusCode)
	}
	return healthy, nil
}
