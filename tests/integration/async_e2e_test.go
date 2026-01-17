//go:build e2e && async

package integration

import (
	"context"
	"os"
	"testing"
	"time"
)

// Async E2E tests verify the NATS-based async provisioning flow.
// These tests require:
// - Server running with NATS enabled
// - NATS JetStream running
// - Set E2E_BASE_URL if not using default
// - Run with: go test -tags=e2e,async ./tests/integration/...

// TestAsync_PoolReplenishment tests that the pool replenishes via NATS after acquisitions.
func TestAsync_PoolReplenishment(t *testing.T) {
	if os.Getenv("E2E_ASYNC") == "" {
		t.Skip("Skipping async E2E test. Set E2E_ASYNC=1 to run.")
	}

	c := setup(t)
	ctx := context.Background()

	// Get initial pool stats
	initialStats, err := c.stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	t.Logf("Initial stats: ready=%d, assigned=%d, target=%d",
		initialStats.Ready, initialStats.Assigned, initialStats.Target)

	// Acquire an instance (this should trigger async replenishment via NATS)
	resp, err := c.acquire(ctx)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	t.Logf("Acquired instance: %s", resp.ID)

	// Release immediately
	if status, err := c.release(ctx, resp.ID); err != nil || status != 200 {
		t.Logf("release returned: status=%d, err=%v", status, err)
	}

	// Wait for pool to replenish via NATS (async flow)
	// The replenisher should detect the pool is low and publish ProvisionTask to NATS
	// The NATS consumer should process it and provision a new instance
	t.Log("Waiting for async replenishment via NATS...")

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		stats, err := c.stats(ctx)
		if err != nil {
			t.Logf("stats error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		t.Logf("Current stats: ready=%d, assigned=%d, warming=%d",
			stats.Ready, stats.Assigned, stats.Warming)

		// Pool should eventually return to target or at least match initial ready count
		if stats.Ready >= initialStats.Ready {
			t.Logf("Pool replenished: ready=%d (was %d)", stats.Ready, initialStats.Ready)
			return
		}

		time.Sleep(5 * time.Second)
	}

	t.Error("Pool did not replenish within timeout - NATS async flow may not be working")
}

// TestAsync_PoolDrainAndRefill tests draining the pool and watching it refill via NATS.
func TestAsync_PoolDrainAndRefill(t *testing.T) {
	if os.Getenv("E2E_ASYNC") == "" {
		t.Skip("Skipping async E2E test. Set E2E_ASYNC=1 to run.")
	}

	c := setup(t)
	ctx := context.Background()

	// Get initial stats
	stats, err := c.stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	t.Logf("Initial stats: ready=%d, target=%d", stats.Ready, stats.Target)

	if stats.Ready == 0 {
		t.Skip("Pool is empty - cannot test drain and refill")
	}

	// Acquire all ready instances
	var instances []string
	t.Log("Draining pool...")
	for {
		resp, err := c.acquire(ctx)
		if err != nil {
			t.Logf("Acquire stopped: %v", err)
			break
		}
		instances = append(instances, resp.ID)
		t.Logf("Acquired %d instances", len(instances))

		// Check if pool is drained
		stats, _ := c.stats(ctx)
		if stats.Ready == 0 {
			break
		}

		// Safety limit
		if len(instances) > 20 {
			t.Log("Safety limit reached")
			break
		}
	}

	// Cleanup: release all acquired instances
	t.Cleanup(func() {
		for _, id := range instances {
			c.release(context.Background(), id)
		}
	})

	t.Logf("Drained %d instances from pool", len(instances))

	// Verify pool is drained
	stats, _ = c.stats(ctx)
	t.Logf("After drain: ready=%d, assigned=%d, warming=%d",
		stats.Ready, stats.Assigned, stats.Warming)

	// Wait for pool to refill via NATS
	t.Log("Waiting for pool to refill via NATS async flow...")

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		stats, err := c.stats(ctx)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		t.Logf("Refilling: ready=%d, warming=%d", stats.Ready, stats.Warming)

		// Success: pool has instances ready
		if stats.Ready > 0 || stats.Warming > 0 {
			t.Logf("Pool is refilling! ready=%d, warming=%d", stats.Ready, stats.Warming)

			// Wait a bit more to see if we reach target
			time.Sleep(30 * time.Second)
			finalStats, _ := c.stats(ctx)
			t.Logf("Final stats: ready=%d, target=%d", finalStats.Ready, finalStats.Target)
			return
		}

		time.Sleep(10 * time.Second)
	}

	t.Error("Pool did not refill within timeout - NATS async provisioning not working")
}

// TestAsync_ConcurrentAcquire tests concurrent acquire requests triggering async replenishment.
func TestAsync_ConcurrentAcquire(t *testing.T) {
	if os.Getenv("E2E_ASYNC") == "" {
		t.Skip("Skipping async E2E test. Set E2E_ASYNC=1 to run.")
	}

	c := setup(t)
	ctx := context.Background()

	// Get initial stats
	stats, err := c.stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	t.Logf("Initial stats: ready=%d", stats.Ready)

	// Acquire multiple instances concurrently
	numConcurrent := 3
	if stats.Ready < numConcurrent {
		numConcurrent = stats.Ready
	}
	if numConcurrent == 0 {
		t.Skip("Not enough ready instances for concurrent test")
	}

	results := make(chan *AcquireResponse, numConcurrent)
	errors := make(chan error, numConcurrent)

	t.Logf("Starting %d concurrent acquire requests...", numConcurrent)
	for i := 0; i < numConcurrent; i++ {
		go func() {
			resp, err := c.acquire(ctx)
			if err != nil {
				errors <- err
			} else {
				results <- resp
			}
		}()
	}

	// Collect results
	var acquired []*AcquireResponse
	for i := 0; i < numConcurrent; i++ {
		select {
		case resp := <-results:
			acquired = append(acquired, resp)
		case err := <-errors:
			t.Logf("Concurrent acquire error: %v", err)
		case <-time.After(30 * time.Second):
			t.Fatal("Timeout waiting for concurrent acquires")
		}
	}

	// Cleanup
	t.Cleanup(func() {
		for _, resp := range acquired {
			c.release(context.Background(), resp.ID)
		}
	})

	t.Logf("Successfully acquired %d instances concurrently", len(acquired))

	// Verify all have unique IDs
	ids := make(map[string]bool)
	for _, resp := range acquired {
		if ids[resp.ID] {
			t.Errorf("Duplicate instance ID: %s", resp.ID)
		}
		ids[resp.ID] = true
	}

	// Check pool stats after concurrent acquire
	afterStats, _ := c.stats(ctx)
	t.Logf("After concurrent acquire: ready=%d, assigned=%d, warming=%d",
		afterStats.Ready, afterStats.Assigned, afterStats.Warming)
}

// TestAsync_AcquireWithEmptyPool tests acquiring when pool is empty triggers async provisioning.
func TestAsync_AcquireWithEmptyPool(t *testing.T) {
	if os.Getenv("E2E_ASYNC") == "" {
		t.Skip("Skipping async E2E test. Set E2E_ASYNC=1 to run.")
	}

	t.Log("Note: This test requires manually draining the pool first")
	t.Skip("This test requires pool to be empty - run after draining pool with make clean-data")

	c := setup(t)
	ctx := context.Background()

	// Verify pool is empty
	stats, err := c.stats(ctx)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}

	if stats.Ready > 0 {
		t.Skipf("Pool has %d ready instances - expected empty pool", stats.Ready)
	}

	t.Log("Pool is empty, attempting acquire (should trigger async provisioning)...")

	// Try to acquire - this should either:
	// 1. Return error (pool empty)
	// 2. Wait for async provisioning and return an instance
	start := time.Now()
	resp, err := c.acquire(ctx)
	acquireTime := time.Since(start)

	if err != nil {
		// Expected if pool is empty and no sync fallback
		t.Logf("Acquire failed (expected): %v", err)
		t.Log("Waiting for async provisioning to add instances to pool...")

		// Wait for NATS-triggered provisioning to add instances
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			stats, _ := c.stats(ctx)
			t.Logf("Pool stats: ready=%d, warming=%d", stats.Ready, stats.Warming)

			if stats.Ready > 0 {
				t.Log("Async provisioning succeeded - pool now has ready instances")
				return
			}

			time.Sleep(10 * time.Second)
		}

		t.Error("No instances provisioned via NATS within timeout")
		return
	}

	// If acquire succeeded, NATS async flow worked
	t.Logf("Acquire succeeded in %v: %s", acquireTime, resp.ID)
	t.Cleanup(func() {
		c.release(context.Background(), resp.ID)
	})
}
