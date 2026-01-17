//go:build e2e

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const defaultBaseURL = "http://localhost:8080"

// acquiredInstances tracks all instances acquired during tests for cleanup
var (
	acquiredInstances   []string
	acquiredInstancesMu sync.Mutex
)

// trackInstance adds an instance ID to the cleanup list
func trackInstance(id string) {
	acquiredInstancesMu.Lock()
	defer acquiredInstancesMu.Unlock()
	acquiredInstances = append(acquiredInstances, id)
}

// untrackInstance removes an instance ID from the cleanup list (already released)
func untrackInstance(id string) {
	acquiredInstancesMu.Lock()
	defer acquiredInstancesMu.Unlock()
	for i, tracked := range acquiredInstances {
		if tracked == id {
			acquiredInstances = append(acquiredInstances[:i], acquiredInstances[i+1:]...)
			return
		}
	}
}

// TestMain runs before/after all tests for global setup and cleanup
func TestMain(m *testing.M) {
	code := m.Run()

	// Cleanup any instances that weren't released via API
	cleanupRemainingInstances()

	os.Exit(code)
}

// cleanupRemainingInstances releases any tracked instances via API, then cleans orphan containers
func cleanupRemainingInstances() {
	acquiredInstancesMu.Lock()
	remaining := make([]string, len(acquiredInstances))
	copy(remaining, acquiredInstances)
	acquiredInstancesMu.Unlock()

	if len(remaining) == 0 {
		return
	}

	fmt.Printf("\n[E2E Cleanup] Releasing %d tracked instances...\n", len(remaining))

	// Try to release via API first
	c := &client{
		base: getBaseURL(),
		http: &http.Client{Timeout: 5 * time.Second},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, id := range remaining {
		if _, err := c.release(ctx, id); err != nil {
			fmt.Printf("[E2E Cleanup] Failed to release %s via API: %v\n", id, err)
		} else {
			fmt.Printf("[E2E Cleanup] Released %s\n", id)
		}
	}

	// Also clean any orphan demo containers directly (fallback if server is down)
	cleanupOrphanContainers()
}

// cleanupOrphanContainers removes demo containers directly via docker CLI
func cleanupOrphanContainers() {
	// Only run if E2E_CLEANUP_CONTAINERS is set (opt-in to avoid accidents)
	if os.Getenv("E2E_CLEANUP_CONTAINERS") == "" {
		return
	}

	fmt.Println("[E2E Cleanup] Checking for orphan demo containers...")
	cmd := exec.Command("docker", "ps", "-q", "--filter", "name=demo-demo-")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	containers := strings.TrimSpace(string(output))
	if containers == "" {
		fmt.Println("[E2E Cleanup] No orphan containers found")
		return
	}

	ids := strings.Split(containers, "\n")
	fmt.Printf("[E2E Cleanup] Removing %d orphan containers...\n", len(ids))

	for _, id := range ids {
		if id == "" {
			continue
		}
		exec.Command("docker", "rm", "-f", id).Run()
	}
	fmt.Println("[E2E Cleanup] Orphan containers removed")
}

// Response types matching internal/api/handler.go

type AcquireResponse struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	AdminURL  string    `json:"admin_url"`
	ExpiresAt time.Time `json:"expires_at"`
	TTL       int       `json:"ttl_seconds"`
}

type InstanceResponse struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	AdminURL     string    `json:"admin_url"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"created_at"`
	AssignedAt   time.Time `json:"assigned_at,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	TTLRemaining int       `json:"ttl_remaining_seconds"`
}

type StatsResponse struct {
	Ready    int `json:"ready"`
	Assigned int `json:"assigned"`
	Warming  int `json:"warming"`
	Target   int `json:"target"`
	Capacity int `json:"capacity"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// Test client

type client struct {
	base string
	http *http.Client
}

func getBaseURL() string {
	if url := os.Getenv("E2E_BASE_URL"); url != "" {
		return url
	}
	return defaultBaseURL
}

func setup(t *testing.T) *client {
	t.Helper()
	c := &client{
		base: getBaseURL(),
		http: &http.Client{Timeout: 30 * time.Second},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.health(ctx); err != nil {
		t.Skipf("Server not running at %s: %v", c.base, err)
	}
	return c
}

func (c *client) health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}

func (c *client) acquire(ctx context.Context) (*AcquireResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1/demo/acquire", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		json.Unmarshal(body, &errResp)
		return nil, fmt.Errorf("acquire failed: %d - %s (%s)", resp.StatusCode, errResp.Error, errResp.Code)
	}
	var result AcquireResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	// Track for cleanup if test fails or server dies
	trackInstance(result.ID)
	return &result, nil
}

func (c *client) get(ctx context.Context, id string) (*InstanceResponse, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/demo/"+id, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("get failed: %d", resp.StatusCode)
	}
	var result InstanceResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("parse response: %w", err)
	}
	return &result, resp.StatusCode, nil
}

func (c *client) extend(ctx context.Context, id string, minutes int) (int, error) {
	payload := fmt.Sprintf(`{"minutes": %d}`, minutes)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/api/v1/demo/"+id+"/extend",
		strings.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("extend failed: %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func (c *client) release(ctx context.Context, id string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.base+"/api/v1/demo/"+id, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)
	// Untrack on successful release (200 or 404 means it's gone)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		untrackInstance(id)
	}
	return resp.StatusCode, nil
}

func (c *client) stats(ctx context.Context) (*StatsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/pool/stats", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stats failed: %d", resp.StatusCode)
	}
	var result StatsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &result, nil
}

// Tests

func TestHealth(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	if err := c.health(ctx); err != nil {
		t.Errorf("health check failed: %v", err)
	}
}

func TestPoolStats(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	stats, err := c.stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if stats.Target <= 0 {
		t.Errorf("expected Target > 0, got %d", stats.Target)
	}
	if stats.Capacity <= 0 {
		t.Errorf("expected Capacity > 0, got %d", stats.Capacity)
	}
	t.Logf("Pool stats: ready=%d, assigned=%d, warming=%d, target=%d, capacity=%d",
		stats.Ready, stats.Assigned, stats.Warming, stats.Target, stats.Capacity)
}

func TestLifecycle(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	var id string
	var initialTTL int

	t.Run("acquire", func(t *testing.T) {
		resp, err := c.acquire(ctx)
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}
		if resp.ID == "" {
			t.Error("expected non-empty ID")
		}
		if resp.URL == "" {
			t.Error("expected non-empty URL")
		}
		if resp.AdminURL == "" {
			t.Error("expected non-empty AdminURL")
		}
		if resp.TTL <= 0 {
			t.Errorf("expected TTL > 0, got %d", resp.TTL)
		}
		if resp.ExpiresAt.Before(time.Now()) {
			t.Error("expected ExpiresAt in the future")
		}
		id = resp.ID
		initialTTL = resp.TTL
		t.Logf("Acquired: id=%s, url=%s, ttl=%ds", id, resp.URL, resp.TTL)
	})

	// Cleanup at parent test level (runs after all subtests complete)
	t.Cleanup(func() {
		if id != "" {
			c.release(context.Background(), id)
		}
	})

	if id == "" {
		t.Fatal("cannot continue without acquired instance")
	}

	t.Run("get", func(t *testing.T) {
		resp, status, err := c.get(ctx, id)
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if status != http.StatusOK {
			t.Errorf("expected 200, got %d", status)
		}
		if resp.ID != id {
			t.Errorf("expected ID=%s, got %s", id, resp.ID)
		}
		if resp.State != "assigned" {
			t.Errorf("expected state=assigned, got %s", resp.State)
		}
		if resp.AssignedAt.IsZero() {
			t.Error("expected AssignedAt to be set")
		}
	})

	t.Run("extend", func(t *testing.T) {
		status, err := c.extend(ctx, id, 5)
		if err != nil {
			t.Fatalf("extend failed: %v", err)
		}
		if status != http.StatusOK {
			t.Errorf("expected 200, got %d", status)
		}

		// Verify TTL increased
		resp, _, _ := c.get(ctx, id)
		// Allow some tolerance for time passing
		expectedMin := initialTTL + (5 * 60) - 30
		if resp.TTLRemaining < expectedMin {
			t.Errorf("expected TTL >= %d after extend, got %d", expectedMin, resp.TTLRemaining)
		}
		t.Logf("Extended: new TTL=%ds (was %ds)", resp.TTLRemaining, initialTTL)
	})

	t.Run("release", func(t *testing.T) {
		status, err := c.release(ctx, id)
		if err != nil {
			t.Logf("release returned error (may be expected): %v", err)
		}
		if status != http.StatusOK {
			t.Errorf("expected 200, got %d", status)
		}
		t.Logf("Released: id=%s", id)
	})

	t.Run("gone", func(t *testing.T) {
		_, status, _ := c.get(ctx, id)
		if status != http.StatusNotFound {
			t.Errorf("expected 404 after release, got %d", status)
		}
	})
}

func TestNotFound(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	_, status, _ := c.get(ctx, "nonexistent-instance-12345")
	if status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", status)
	}
}

func TestInvalidExtend(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	// Acquire instance for testing
	resp, err := c.acquire(ctx)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	t.Cleanup(func() {
		c.release(context.Background(), resp.ID)
	})

	tests := map[string]struct {
		minutes    int
		wantStatus int
	}{
		"zero":     {0, http.StatusBadRequest},
		"negative": {-1, http.StatusBadRequest},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			status, _ := c.extend(ctx, resp.ID, tc.minutes)
			if status != tc.wantStatus {
				t.Errorf("extend(%d): got status %d, want %d", tc.minutes, status, tc.wantStatus)
			}
		})
	}
}

func TestDoubleRelease(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	// Acquire instance
	resp, err := c.acquire(ctx)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// First release - should succeed
	status, _ := c.release(ctx, resp.ID)
	if status != http.StatusOK {
		t.Errorf("first release: expected 200, got %d", status)
	}

	// Second release - should return 404
	status, _ = c.release(ctx, resp.ID)
	if status != http.StatusNotFound {
		t.Errorf("second release: expected 404, got %d", status)
	}
}
