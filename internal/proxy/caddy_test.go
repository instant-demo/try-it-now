package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
)

// skipIfNoCaddy skips the test if Caddy is not available.
func skipIfNoCaddy(t *testing.T) *CaddyRouteManager {
	t.Helper()
	if os.Getenv("CADDY_TEST") == "" {
		t.Skip("Skipping Caddy integration test. Set CADDY_TEST=1 to run.")
	}

	cfg := &config.ProxyConfig{
		CaddyAdminURL: getEnvOrDefault("CADDY_ADMIN_URL", "http://localhost:2019"),
		BaseDomain:    "localhost",
	}

	manager := NewCaddyRouteManager(cfg, nil)

	// Check if Caddy is available
	ctx := context.Background()
	if err := manager.Health(ctx); err != nil {
		t.Skipf("Failed to connect to Caddy: %v", err)
	}

	return manager
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func TestCaddyRouteManager_BuildCaddyRoute(t *testing.T) {
	cfg := &config.ProxyConfig{
		CaddyAdminURL: "http://localhost:2019",
		BaseDomain:    "example.com",
	}
	manager := NewCaddyRouteManager(cfg, nil)

	route := Route{
		Hostname:    "demo-abc123",
		UpstreamURL: "http://localhost:32000",
		InstanceID:  "instance-123",
	}

	cRoute := manager.buildCaddyRoute(route)

	if cRoute.ID != "demo-route-demo-abc123" {
		t.Errorf("ID = %s, want demo-route-demo-abc123", cRoute.ID)
	}

	if len(cRoute.Match) != 1 {
		t.Fatalf("Match length = %d, want 1", len(cRoute.Match))
	}

	if len(cRoute.Match[0].Host) != 1 {
		t.Fatalf("Host length = %d, want 1", len(cRoute.Match[0].Host))
	}

	expectedHost := "demo-abc123.example.com"
	if cRoute.Match[0].Host[0] != expectedHost {
		t.Errorf("Host = %s, want %s", cRoute.Match[0].Host[0], expectedHost)
	}

	if !cRoute.Terminal {
		t.Error("Terminal = false, want true")
	}

	// Check the nested handler structure
	if len(cRoute.Handle) != 1 {
		t.Fatalf("Handle length = %d, want 1", len(cRoute.Handle))
	}

	if cRoute.Handle[0].Handler != "subroute" {
		t.Errorf("Handler = %s, want subroute", cRoute.Handle[0].Handler)
	}

	if len(cRoute.Handle[0].Routes) != 1 {
		t.Fatalf("Subroute length = %d, want 1", len(cRoute.Handle[0].Routes))
	}

	if len(cRoute.Handle[0].Routes[0].Handle) != 1 {
		t.Fatalf("Inner handle length = %d, want 1", len(cRoute.Handle[0].Routes[0].Handle))
	}

	innerHandler := cRoute.Handle[0].Routes[0].Handle[0]
	if innerHandler.Handler != "reverse_proxy" {
		t.Errorf("Inner handler = %s, want reverse_proxy", innerHandler.Handler)
	}

	if len(innerHandler.Upstreams) != 1 {
		t.Fatalf("Upstreams length = %d, want 1", len(innerHandler.Upstreams))
	}

	if innerHandler.Upstreams[0].Dial != "localhost:32000" {
		t.Errorf("Upstream dial = %s, want localhost:32000", innerHandler.Upstreams[0].Dial)
	}
}

func TestCaddyRouteManager_CaddyRouteToRoute(t *testing.T) {
	cfg := &config.ProxyConfig{
		CaddyAdminURL: "http://localhost:2019",
		BaseDomain:    "example.com",
	}
	manager := NewCaddyRouteManager(cfg, nil)

	cRoute := caddyRoute{
		ID: "demo-route-demo-xyz789",
		Match: []caddyMatch{
			{Host: []string{"demo-xyz789.example.com"}},
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
									{Dial: "localhost:32001"},
								},
							},
						},
					},
				},
			},
		},
		Terminal: true,
	}

	route := manager.caddyRouteToRoute(cRoute)

	if route == nil {
		t.Fatal("caddyRouteToRoute returned nil")
	}

	if route.Hostname != "demo-xyz789" {
		t.Errorf("Hostname = %s, want demo-xyz789", route.Hostname)
	}

	if route.UpstreamURL != "http://localhost:32001" {
		t.Errorf("UpstreamURL = %s, want http://localhost:32001", route.UpstreamURL)
	}

	if route.InstanceID != "demo-xyz789" {
		t.Errorf("InstanceID = %s, want demo-xyz789", route.InstanceID)
	}
}

func TestCaddyRouteManager_CaddyRouteToRoute_EmptyMatch(t *testing.T) {
	cfg := &config.ProxyConfig{
		CaddyAdminURL: "http://localhost:2019",
		BaseDomain:    "example.com",
	}
	manager := NewCaddyRouteManager(cfg, nil)

	cRoute := caddyRoute{
		Match:  []caddyMatch{},
		Handle: []caddyHandler{},
	}

	route := manager.caddyRouteToRoute(cRoute)
	if route != nil {
		t.Error("caddyRouteToRoute should return nil for empty match")
	}
}

func TestCaddyRouteManager_RouteID(t *testing.T) {
	cfg := &config.ProxyConfig{
		CaddyAdminURL: "http://localhost:2019",
		BaseDomain:    "example.com",
	}
	manager := NewCaddyRouteManager(cfg, nil)

	tests := []struct {
		hostname string
		expected string
	}{
		{"demo-abc", "demo-route-demo-abc"},
		{"demo.with.dots", "demo-route-demo-with-dots"},
		{"simple", "demo-route-simple"},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			got := manager.routeID(tt.hostname)
			if got != tt.expected {
				t.Errorf("routeID(%s) = %s, want %s", tt.hostname, got, tt.expected)
			}
		})
	}
}

func TestCaddyRouteManager_WithMockServer(t *testing.T) {
	routes := make(map[string]caddyRoute)
	serverExists := false

	// Create a mock Caddy server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/config/":
			// Health check
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{"apps": map[string]interface{}{}})

		case r.Method == http.MethodGet && path == "/config/apps/http/servers/demo":
			if serverExists {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(caddyServer{Listen: []string{":443"}, Routes: []caddyRoute{}})
			} else {
				w.WriteHeader(http.StatusNotFound)
			}

		case r.Method == http.MethodPut && strings.HasPrefix(path, "/config/apps/http/servers/demo"):
			serverExists = true
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/routes"):
			var route caddyRoute
			if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			routes[route.ID] = route
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && strings.HasPrefix(path, "/id/"):
			id := strings.TrimPrefix(path, "/id/")
			if route, ok := routes[id]; ok {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(route)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}

		case r.Method == http.MethodDelete && strings.HasPrefix(path, "/id/"):
			id := strings.TrimPrefix(path, "/id/")
			delete(routes, id)
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && strings.HasSuffix(path, "/routes"):
			routeList := make([]caddyRoute, 0, len(routes))
			for _, route := range routes {
				routeList = append(routeList, route)
			}
			w.WriteHeader(http.StatusOK)
			if len(routeList) == 0 {
				w.Write([]byte("[]"))
			} else {
				json.NewEncoder(w).Encode(routeList)
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := &config.ProxyConfig{
		CaddyAdminURL: server.URL,
		BaseDomain:    "test.local",
	}
	manager := NewCaddyRouteManager(cfg, nil)
	ctx := context.Background()

	// Test Health
	if err := manager.Health(ctx); err != nil {
		t.Fatalf("Health() error = %v", err)
	}

	// Test AddRoute
	route := Route{
		Hostname:    "demo-test",
		UpstreamURL: "http://localhost:8080",
		InstanceID:  "test-instance",
	}

	if err := manager.AddRoute(ctx, route); err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}

	// Test GetRoute
	got, err := manager.GetRoute(ctx, "demo-test")
	if err != nil {
		t.Fatalf("GetRoute() error = %v", err)
	}

	if got.Hostname != route.Hostname {
		t.Errorf("GetRoute() Hostname = %s, want %s", got.Hostname, route.Hostname)
	}

	// Test ListRoutes
	list, err := manager.ListRoutes(ctx)
	if err != nil {
		t.Fatalf("ListRoutes() error = %v", err)
	}

	if len(list) != 1 {
		t.Errorf("ListRoutes() len = %d, want 1", len(list))
	}

	// Test RemoveRoute
	if err := manager.RemoveRoute(ctx, "demo-test"); err != nil {
		t.Fatalf("RemoveRoute() error = %v", err)
	}

	// Verify removed
	_, err = manager.GetRoute(ctx, "demo-test")
	if err != domain.ErrRouteNotFound {
		t.Errorf("GetRoute() after remove error = %v, want %v", err, domain.ErrRouteNotFound)
	}
}

// Integration tests (require running Caddy)

func TestCaddyRouteManager_Health_Integration(t *testing.T) {
	manager := skipIfNoCaddy(t)

	ctx := context.Background()
	if err := manager.Health(ctx); err != nil {
		t.Errorf("Health() error = %v", err)
	}
}

func TestCaddyRouteManager_AddAndRemoveRoute_Integration(t *testing.T) {
	manager := skipIfNoCaddy(t)

	ctx := context.Background()

	route := Route{
		Hostname:    "demo-integration-test",
		UpstreamURL: "http://localhost:9999",
		InstanceID:  "int-test-1",
	}

	// Add route
	if err := manager.AddRoute(ctx, route); err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}

	// Cleanup
	defer func() {
		_ = manager.RemoveRoute(ctx, route.Hostname)
	}()

	// Get route
	got, err := manager.GetRoute(ctx, route.Hostname)
	if err != nil {
		t.Fatalf("GetRoute() error = %v", err)
	}

	if got.Hostname != route.Hostname {
		t.Errorf("GetRoute() Hostname = %s, want %s", got.Hostname, route.Hostname)
	}

	// List routes
	list, err := manager.ListRoutes(ctx)
	if err != nil {
		t.Fatalf("ListRoutes() error = %v", err)
	}

	found := false
	for _, r := range list {
		if r.Hostname == route.Hostname {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListRoutes() did not include added route")
	}

	// Remove route
	if err := manager.RemoveRoute(ctx, route.Hostname); err != nil {
		t.Fatalf("RemoveRoute() error = %v", err)
	}

	// Verify removed
	_, err = manager.GetRoute(ctx, route.Hostname)
	if err != domain.ErrRouteNotFound {
		t.Errorf("GetRoute() after remove error = %v, want %v", err, domain.ErrRouteNotFound)
	}
}
