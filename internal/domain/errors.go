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

	// ErrRouteExists is returned when trying to add a route that already exists.
	ErrRouteExists = errors.New("route already exists")

	// ErrRouteNotFound is returned when the route doesn't exist.
	ErrRouteNotFound = errors.New("route not found")
)
