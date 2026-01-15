package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
)

// CaddyRouteManager implements RouteManager using the Caddy admin API.
type CaddyRouteManager struct {
	adminURL   string
	baseDomain string
	httpClient *http.Client
	serverName string // The Caddy server name (e.g., "srv0")
}

// NewCaddyRouteManager creates a new Caddy-based route manager.
func NewCaddyRouteManager(cfg *config.ProxyConfig) *CaddyRouteManager {
	return &CaddyRouteManager{
		adminURL:   cfg.CaddyAdminURL,
		baseDomain: cfg.BaseDomain,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		serverName: "demo", // We'll create a dedicated server for demo routes
	}
}

// caddyRoute represents a Caddy route in JSON format.
type caddyRoute struct {
	ID       string          `json:"@id,omitempty"`
	Match    []caddyMatch    `json:"match,omitempty"`
	Handle   []caddyHandler  `json:"handle"`
	Terminal bool            `json:"terminal,omitempty"`
}

type caddyMatch struct {
	Host []string `json:"host,omitempty"`
}

type caddyHandler struct {
	Handler   string          `json:"handler"`
	Routes    []caddySubroute `json:"routes,omitempty"`
	Upstreams []caddyUpstream `json:"upstreams,omitempty"`
}

type caddySubroute struct {
	Handle []caddyHandler `json:"handle"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

// caddyServer represents a Caddy HTTP server.
type caddyServer struct {
	Listen []string     `json:"listen"`
	Routes []caddyRoute `json:"routes"`
}

// AddRoute adds a route for a demo instance.
func (m *CaddyRouteManager) AddRoute(ctx context.Context, route Route) error {
	// Build the Caddy route
	cRoute := m.buildCaddyRoute(route)

	// Marshal to JSON
	data, err := json.Marshal(cRoute)
	if err != nil {
		return fmt.Errorf("failed to marshal route: %w", err)
	}

	// POST to Caddy API
	// First, ensure the server exists, then add the route
	if err := m.ensureServerExists(ctx); err != nil {
		return err
	}

	url := fmt.Sprintf("%s/config/apps/http/servers/%s/routes", m.adminURL, m.serverName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to add route: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// RemoveRoute removes a route by hostname.
func (m *CaddyRouteManager) RemoveRoute(ctx context.Context, hostname string) error {
	routeID := m.routeID(hostname)
	url := fmt.Sprintf("%s/id/%s", m.adminURL, routeID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to remove route: %w", err)
	}
	defer resp.Body.Close()

	// 404 is fine - route might already be gone
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("caddy returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetRoute returns a route by hostname.
func (m *CaddyRouteManager) GetRoute(ctx context.Context, hostname string) (*Route, error) {
	routeID := m.routeID(hostname)
	url := fmt.Sprintf("%s/id/%s", m.adminURL, routeID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get route: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, domain.ErrRouteNotFound
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("caddy returned status %d: %s", resp.StatusCode, string(body))
	}

	var cRoute caddyRoute
	if err := json.NewDecoder(resp.Body).Decode(&cRoute); err != nil {
		return nil, fmt.Errorf("failed to decode route: %w", err)
	}

	return m.caddyRouteToRoute(cRoute), nil
}

// ListRoutes returns all demo routes.
func (m *CaddyRouteManager) ListRoutes(ctx context.Context) ([]Route, error) {
	url := fmt.Sprintf("%s/config/apps/http/servers/%s/routes", m.adminURL, m.serverName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list routes: %w", err)
	}
	defer resp.Body.Close()

	// No routes yet is fine
	if resp.StatusCode == http.StatusNotFound {
		return []Route{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("caddy returned status %d: %s", resp.StatusCode, string(body))
	}

	var cRoutes []caddyRoute
	if err := json.NewDecoder(resp.Body).Decode(&cRoutes); err != nil {
		return nil, fmt.Errorf("failed to decode routes: %w", err)
	}

	routes := make([]Route, 0, len(cRoutes))
	for _, cRoute := range cRoutes {
		if route := m.caddyRouteToRoute(cRoute); route != nil {
			routes = append(routes, *route)
		}
	}

	return routes, nil
}

// Health checks if Caddy is responding.
func (m *CaddyRouteManager) Health(ctx context.Context) error {
	url := fmt.Sprintf("%s/config/", m.adminURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("caddy health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("caddy returned status %d", resp.StatusCode)
	}

	return nil
}

// ensureServerExists creates the demo server if it doesn't exist.
func (m *CaddyRouteManager) ensureServerExists(ctx context.Context) error {
	url := fmt.Sprintf("%s/config/apps/http/servers/%s", m.adminURL, m.serverName)

	// Check if server exists
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}

	// Caddy returns 200 with "null" body (possibly with newline) when path exists but has no value
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	bodyStr := strings.TrimSpace(string(body))
	if resp.StatusCode == http.StatusOK && bodyStr != "null" && bodyStr != "" {
		return nil // Server actually exists
	}

	// Create server on a separate port to avoid conflict with existing servers
	server := caddyServer{
		Listen: []string{":8443"},
		Routes: []caddyRoute{},
	}

	data, err := json.Marshal(server)
	if err != nil {
		return err
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create server: %s", string(body))
	}

	return nil
}

// buildCaddyRoute creates a Caddy route from our Route struct.
func (m *CaddyRouteManager) buildCaddyRoute(route Route) caddyRoute {
	fullHostname := route.Hostname
	if !strings.Contains(fullHostname, ".") {
		fullHostname = route.Hostname + "." + m.baseDomain
	}

	// Parse upstream URL to get host:port
	upstream := route.UpstreamURL
	upstream = strings.TrimPrefix(upstream, "http://")
	upstream = strings.TrimPrefix(upstream, "https://")

	return caddyRoute{
		ID: m.routeID(route.Hostname),
		Match: []caddyMatch{
			{Host: []string{fullHostname}},
		},
		Handle: []caddyHandler{
			{
				Handler: "subroute",
				Routes: []caddySubroute{
					{
						Handle: []caddyHandler{
							{
								Handler: "reverse_proxy",
								Upstreams: []caddyUpstream{
									{Dial: upstream},
								},
							},
						},
					},
				},
			},
		},
		Terminal: true,
	}
}

// caddyRouteToRoute converts a Caddy route back to our Route struct.
func (m *CaddyRouteManager) caddyRouteToRoute(cRoute caddyRoute) *Route {
	if len(cRoute.Match) == 0 || len(cRoute.Match[0].Host) == 0 {
		return nil
	}
	if len(cRoute.Handle) == 0 {
		return nil
	}

	hostname := cRoute.Match[0].Host[0]
	hostname = strings.TrimSuffix(hostname, "."+m.baseDomain)

	// Extract upstream from nested structure
	upstreamURL := ""
	if len(cRoute.Handle) > 0 && cRoute.Handle[0].Handler == "subroute" {
		if len(cRoute.Handle[0].Routes) > 0 {
			for _, h := range cRoute.Handle[0].Routes[0].Handle {
				if h.Handler == "reverse_proxy" && len(h.Upstreams) > 0 {
					upstreamURL = "http://" + h.Upstreams[0].Dial
					break
				}
			}
		}
	}

	// Extract instance ID from route ID
	instanceID := ""
	if cRoute.ID != "" {
		instanceID = strings.TrimPrefix(cRoute.ID, "demo-route-")
	}

	return &Route{
		Hostname:    hostname,
		UpstreamURL: upstreamURL,
		InstanceID:  instanceID,
	}
}

// routeID generates a unique ID for a route.
func (m *CaddyRouteManager) routeID(hostname string) string {
	// Clean hostname for use as ID
	clean := strings.ReplaceAll(hostname, ".", "-")
	return "demo-route-" + clean
}

// Compile-time check that CaddyRouteManager implements RouteManager
var _ RouteManager = (*CaddyRouteManager)(nil)
