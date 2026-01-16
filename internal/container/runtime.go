package container

import (
	"context"

	"github.com/instant-demo/try-it-now/internal/domain"
)

// Runtime defines the interface for container operations.
// Implementations: Docker (dev), Podman+CRIU (production).
type Runtime interface {
	// RestoreFromCheckpoint restores a container from a CRIU checkpoint.
	// Returns the new instance with container ID populated.
	RestoreFromCheckpoint(ctx context.Context, opts RestoreOptions) (*domain.Instance, error)

	// Start starts a new container without checkpoint (fallback for dev).
	Start(ctx context.Context, opts StartOptions) (*domain.Instance, error)

	// Stop stops and removes a container.
	Stop(ctx context.Context, containerID string) error

	// Inspect returns information about a running container.
	Inspect(ctx context.Context, containerID string) (*ContainerInfo, error)

	// HealthCheck checks if the container is responding.
	HealthCheck(ctx context.Context, containerID string) (bool, error)
}

// RestoreOptions configures checkpoint restoration.
type RestoreOptions struct {
	CheckpointPath string            // Path to checkpoint archive
	Name           string            // Container name
	Hostname       string            // Hostname for PS_DOMAIN
	Port           int               // Host port to map
	DBPrefix       string            // Database table prefix
	Labels         map[string]string // Container labels
}

// StartOptions configures a new container start (no CRIU).
type StartOptions struct {
	Image     string            // Container image
	Name      string            // Container name
	Hostname  string            // Hostname for PS_DOMAIN
	Port      int               // Host port to map
	DBPrefix  string            // Database table prefix
	EnvVars   map[string]string // Environment variables
	Labels    map[string]string // Container labels
	NetworkID string            // Docker/Podman network
}

// ContainerInfo holds information about a running container.
type ContainerInfo struct {
	ID        string
	Name      string
	State     string // running, stopped, etc.
	IPAddress string
	Ports     map[int]int // container port -> host port
}
