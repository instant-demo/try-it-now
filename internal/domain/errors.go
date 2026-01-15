package domain

import "errors"

var (
	// ErrPoolExhausted is returned when no instances are available in the warm pool.
	ErrPoolExhausted = errors.New("pool exhausted: no instances available")

	// ErrInstanceNotFound is returned when the requested instance doesn't exist.
	ErrInstanceNotFound = errors.New("instance not found")

	// ErrRateLimited is returned when the user has exceeded their rate limit.
	ErrRateLimited = errors.New("rate limit exceeded")

	// ErrNoPortsAvailable is returned when all ports in the range are in use.
	ErrNoPortsAvailable = errors.New("no ports available")

	// ErrContainerNotFound is returned when the container doesn't exist.
	ErrContainerNotFound = errors.New("container not found")

	// ErrCheckpointNotFound is returned when the CRIU checkpoint file doesn't exist.
	ErrCheckpointNotFound = errors.New("checkpoint not found")

	// ErrCRIUNotAvailable is returned when CRIU is not installed or accessible.
	ErrCRIUNotAvailable = errors.New("CRIU not available")

	// ErrCRIUVersionUnsupported is returned when CRIU version is too old (requires >= 3.11).
	ErrCRIUVersionUnsupported = errors.New("CRIU version not supported (requires >= 3.11)")

	// ErrRouteExists is returned when trying to add a route that already exists.
	ErrRouteExists = errors.New("route already exists")

	// ErrRouteNotFound is returned when the route doesn't exist.
	ErrRouteNotFound = errors.New("route not found")

	// ErrRouteCreationFailed is returned when Caddy route creation fails during acquisition.
	ErrRouteCreationFailed = errors.New("failed to create route for instance")
)
