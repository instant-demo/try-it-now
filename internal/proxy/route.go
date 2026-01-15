package proxy

import (
	"context"
)

// RouteManager defines the interface for dynamic reverse proxy routing.
// Implementation: Caddy REST API.
type RouteManager interface {
	// AddRoute adds a route for a demo instance.
	// The route becomes active immediately (via Caddy API).
	AddRoute(ctx context.Context, route Route) error

	// RemoveRoute removes a route by hostname.
	RemoveRoute(ctx context.Context, hostname string) error

	// GetRoute returns a route by hostname.
	GetRoute(ctx context.Context, hostname string) (*Route, error)

	// ListRoutes returns all demo routes.
	ListRoutes(ctx context.Context) ([]Route, error)

	// Health checks if the proxy is responding.
	Health(ctx context.Context) error
}

// Route represents a reverse proxy route to a demo instance.
type Route struct {
	Hostname    string `json:"hostname"`     // e.g., "demo-a1b2c3d4.migrationpro.com"
	UpstreamURL string `json:"upstream_url"` // e.g., "http://localhost:32001"
	InstanceID  string `json:"instance_id"`  // For tracking
}
