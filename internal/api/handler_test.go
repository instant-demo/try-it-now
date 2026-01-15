package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestNewHandler(t *testing.T) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "docker",
		},
	}

	h := NewHandler(cfg)

	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.cfg != cfg {
		t.Error("NewHandler did not set config correctly")
	}
}

func TestRouter(t *testing.T) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "docker",
		},
	}
	h := NewHandler(cfg)

	router := h.Router()

	if router == nil {
		t.Fatal("Router returned nil")
	}
}

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "podman",
		},
	}
	h := NewHandler(cfg)
	router := h.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var response map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if response["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", response["status"])
	}
	if response["mode"] != "podman" {
		t.Errorf("expected mode 'podman', got %q", response["mode"])
	}
}

func TestRoutesRegistered(t *testing.T) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "docker",
		},
	}
	h := NewHandler(cfg)
	router := h.Router()

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{"GET", "/health", http.StatusOK},
		{"GET", "/metrics", http.StatusOK},
		{"POST", "/api/v1/demo/acquire", http.StatusNotImplemented},
		{"GET", "/api/v1/demo/test-id", http.StatusNotImplemented},
		{"POST", "/api/v1/demo/test-id/extend", http.StatusNotImplemented},
		{"DELETE", "/api/v1/demo/test-id", http.StatusNotImplemented},
		{"GET", "/api/v1/demo/test-id/status", http.StatusNotImplemented},
		{"GET", "/api/v1/pool/stats", http.StatusNotImplemented},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(tt.method, tt.path, nil)
			router.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Errorf("expected status %d, got %d", tt.want, w.Code)
			}
		})
	}
}

func TestNotFoundRoute(t *testing.T) {
	cfg := &config.Config{
		Container: config.ContainerConfig{
			Mode: "docker",
		},
	}
	h := NewHandler(cfg)
	router := h.Router()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/nonexistent", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d for nonexistent route, got %d", http.StatusNotFound, w.Code)
	}
}
