package domain

import (
	"testing"
)

func TestPoolStats_NeedsReplenishment(t *testing.T) {
	tests := []struct {
		name      string
		stats     *PoolStats
		threshold float64
		want      bool
	}{
		{
			name: "zero target returns false",
			stats: &PoolStats{
				Ready:  0,
				Target: 0,
			},
			threshold: 0.5,
			want:      false,
		},
		{
			name: "pool below threshold needs replenishment",
			stats: &PoolStats{
				Ready:  2,
				Target: 10,
			},
			threshold: 0.5, // 2/10 = 0.2 < 0.5
			want:      true,
		},
		{
			name: "pool at threshold does not need replenishment",
			stats: &PoolStats{
				Ready:  5,
				Target: 10,
			},
			threshold: 0.5, // 5/10 = 0.5, not less than 0.5
			want:      false,
		},
		{
			name: "pool above threshold does not need replenishment",
			stats: &PoolStats{
				Ready:  8,
				Target: 10,
			},
			threshold: 0.5, // 8/10 = 0.8 > 0.5
			want:      false,
		},
		{
			name: "full pool does not need replenishment",
			stats: &PoolStats{
				Ready:  10,
				Target: 10,
			},
			threshold: 0.5,
			want:      false,
		},
		{
			name: "empty pool needs replenishment",
			stats: &PoolStats{
				Ready:  0,
				Target: 10,
			},
			threshold: 0.5,
			want:      true,
		},
		{
			name: "threshold of 1.0 - pool below",
			stats: &PoolStats{
				Ready:  9,
				Target: 10,
			},
			threshold: 1.0,
			want:      true,
		},
		{
			name: "threshold of 0.0 - any ready is sufficient",
			stats: &PoolStats{
				Ready:  1,
				Target: 10,
			},
			threshold: 0.0,
			want:      false,
		},
		{
			name: "over-provisioned pool",
			stats: &PoolStats{
				Ready:  15,
				Target: 10,
			},
			threshold: 0.5,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.stats.NeedsReplenishment(tt.threshold)
			if got != tt.want {
				t.Errorf("PoolStats.NeedsReplenishment(%v) = %v, want %v", tt.threshold, got, tt.want)
			}
		})
	}
}

func TestPoolStats_AvailableCapacity(t *testing.T) {
	tests := []struct {
		name  string
		stats *PoolStats
		want  int
	}{
		{
			name: "all slots free",
			stats: &PoolStats{
				Ready:    0,
				Assigned: 0,
				Warming:  0,
				Capacity: 100,
			},
			want: 100,
		},
		{
			name: "some slots used",
			stats: &PoolStats{
				Ready:    10,
				Assigned: 20,
				Warming:  5,
				Capacity: 100,
			},
			want: 65, // 100 - (10 + 20 + 5)
		},
		{
			name: "at capacity",
			stats: &PoolStats{
				Ready:    30,
				Assigned: 50,
				Warming:  20,
				Capacity: 100,
			},
			want: 0,
		},
		{
			name: "over capacity returns zero",
			stats: &PoolStats{
				Ready:    40,
				Assigned: 50,
				Warming:  20,
				Capacity: 100,
			},
			want: 0, // 110 > 100, but returns 0 not negative
		},
		{
			name: "zero capacity",
			stats: &PoolStats{
				Ready:    0,
				Assigned: 0,
				Warming:  0,
				Capacity: 0,
			},
			want: 0,
		},
		{
			name: "only ready instances",
			stats: &PoolStats{
				Ready:    50,
				Assigned: 0,
				Warming:  0,
				Capacity: 100,
			},
			want: 50,
		},
		{
			name: "only assigned instances",
			stats: &PoolStats{
				Ready:    0,
				Assigned: 75,
				Warming:  0,
				Capacity: 100,
			},
			want: 25,
		},
		{
			name: "only warming instances",
			stats: &PoolStats{
				Ready:    0,
				Assigned: 0,
				Warming:  30,
				Capacity: 100,
			},
			want: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.stats.AvailableCapacity()
			if got != tt.want {
				t.Errorf("PoolStats.AvailableCapacity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPoolStats_ReplenishmentNeeded(t *testing.T) {
	tests := []struct {
		name  string
		stats *PoolStats
		want  int
	}{
		{
			name: "gap less than available capacity",
			stats: &PoolStats{
				Ready:    5,
				Assigned: 0,
				Warming:  0,
				Target:   10,
				Capacity: 100,
			},
			want: 5, // gap = 10 - 5 - 0 = 5, available = 95
		},
		{
			name: "gap greater than available capacity",
			stats: &PoolStats{
				Ready:    5,
				Assigned: 90,
				Warming:  0,
				Target:   10,
				Capacity: 100,
			},
			want: 5, // gap = 10 - 5 - 0 = 5, available = 5
		},
		{
			name: "gap exceeds available capacity",
			stats: &PoolStats{
				Ready:    0,
				Assigned: 98,
				Warming:  0,
				Target:   10,
				Capacity: 100,
			},
			want: 2, // gap = 10, available = 2, return min(10, 2) = 2
		},
		{
			name: "no gap needed - ready meets target",
			stats: &PoolStats{
				Ready:    10,
				Assigned: 0,
				Warming:  0,
				Target:   10,
				Capacity: 100,
			},
			want: 0,
		},
		{
			name: "no gap needed - ready exceeds target",
			stats: &PoolStats{
				Ready:    15,
				Assigned: 0,
				Warming:  0,
				Target:   10,
				Capacity: 100,
			},
			want: 0,
		},
		{
			name: "warming instances count toward gap",
			stats: &PoolStats{
				Ready:    3,
				Assigned: 0,
				Warming:  5,
				Target:   10,
				Capacity: 100,
			},
			want: 2, // gap = 10 - 3 - 5 = 2
		},
		{
			name: "warming instances fill gap completely",
			stats: &PoolStats{
				Ready:    3,
				Assigned: 0,
				Warming:  7,
				Target:   10,
				Capacity: 100,
			},
			want: 0, // gap = 10 - 3 - 7 = 0
		},
		{
			name: "at capacity returns zero",
			stats: &PoolStats{
				Ready:    30,
				Assigned: 50,
				Warming:  20,
				Target:   50,
				Capacity: 100,
			},
			want: 0, // at capacity, available = 0
		},
		{
			name: "zero target",
			stats: &PoolStats{
				Ready:    0,
				Assigned: 0,
				Warming:  0,
				Target:   0,
				Capacity: 100,
			},
			want: 0, // gap = 0 - 0 - 0 = 0
		},
		{
			name: "zero capacity",
			stats: &PoolStats{
				Ready:    0,
				Assigned: 0,
				Warming:  0,
				Target:   10,
				Capacity: 0,
			},
			want: 0, // available = 0
		},
		{
			name: "negative gap returns zero",
			stats: &PoolStats{
				Ready:    8,
				Assigned: 0,
				Warming:  5,
				Target:   10,
				Capacity: 100,
			},
			want: 0, // gap = 10 - 8 - 5 = -3
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.stats.ReplenishmentNeeded()
			if got != tt.want {
				t.Errorf("PoolStats.ReplenishmentNeeded() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPoolStats_MethodInteraction(t *testing.T) {
	// Test that the methods work together correctly
	t.Run("replenishment matches need", func(t *testing.T) {
		stats := &PoolStats{
			Ready:    2,
			Assigned: 10,
			Warming:  3,
			Target:   10,
			Capacity: 50,
		}

		// Verify needs replenishment
		if !stats.NeedsReplenishment(0.5) {
			t.Error("expected NeedsReplenishment(0.5) = true")
		}

		// Verify available capacity
		expectedCapacity := 50 - (2 + 10 + 3) // 35
		if stats.AvailableCapacity() != expectedCapacity {
			t.Errorf("AvailableCapacity() = %v, want %v", stats.AvailableCapacity(), expectedCapacity)
		}

		// Verify replenishment needed
		expectedNeeded := 10 - 2 - 3 // 5 (gap), which is less than available capacity
		if stats.ReplenishmentNeeded() != expectedNeeded {
			t.Errorf("ReplenishmentNeeded() = %v, want %v", stats.ReplenishmentNeeded(), expectedNeeded)
		}
	})

	t.Run("capacity constrained replenishment", func(t *testing.T) {
		stats := &PoolStats{
			Ready:    0,
			Assigned: 95,
			Warming:  2,
			Target:   10,
			Capacity: 100,
		}

		// Available capacity is 3 (100 - 95 - 2 - 0)
		if stats.AvailableCapacity() != 3 {
			t.Errorf("AvailableCapacity() = %v, want 3", stats.AvailableCapacity())
		}

		// Gap is 8 (10 - 0 - 2), but only 3 available
		if stats.ReplenishmentNeeded() != 3 {
			t.Errorf("ReplenishmentNeeded() = %v, want 3", stats.ReplenishmentNeeded())
		}
	})
}

func TestPoolStats_ZeroValues(t *testing.T) {
	// Test behavior with zero-value struct
	stats := &PoolStats{}

	t.Run("NeedsReplenishment with zero target", func(t *testing.T) {
		if stats.NeedsReplenishment(0.5) {
			t.Error("NeedsReplenishment should return false for zero target")
		}
	})

	t.Run("AvailableCapacity with zero values", func(t *testing.T) {
		if stats.AvailableCapacity() != 0 {
			t.Errorf("AvailableCapacity() = %v, want 0", stats.AvailableCapacity())
		}
	})

	t.Run("ReplenishmentNeeded with zero values", func(t *testing.T) {
		if stats.ReplenishmentNeeded() != 0 {
			t.Errorf("ReplenishmentNeeded() = %v, want 0", stats.ReplenishmentNeeded())
		}
	})
}

func TestPoolStats_EdgeCases(t *testing.T) {
	t.Run("large numbers", func(t *testing.T) {
		stats := &PoolStats{
			Ready:    100000,
			Assigned: 500000,
			Warming:  50000,
			Target:   200000,
			Capacity: 1000000,
		}

		// available = 1000000 - 650000 = 350000
		if stats.AvailableCapacity() != 350000 {
			t.Errorf("AvailableCapacity() = %v, want 350000", stats.AvailableCapacity())
		}

		// gap = 200000 - 100000 - 50000 = 50000
		if stats.ReplenishmentNeeded() != 50000 {
			t.Errorf("ReplenishmentNeeded() = %v, want 50000", stats.ReplenishmentNeeded())
		}
	})

	t.Run("threshold boundary 0.5 exactly", func(t *testing.T) {
		stats := &PoolStats{
			Ready:  5,
			Target: 10,
		}
		// 5/10 = 0.5, which is NOT less than 0.5
		if stats.NeedsReplenishment(0.5) {
			t.Error("NeedsReplenishment(0.5) should return false when ratio equals threshold")
		}
	})

	t.Run("threshold boundary just below", func(t *testing.T) {
		stats := &PoolStats{
			Ready:  4,
			Target: 10,
		}
		// 4/10 = 0.4 < 0.5
		if !stats.NeedsReplenishment(0.5) {
			t.Error("NeedsReplenishment(0.5) should return true when ratio is below threshold")
		}
	})

	t.Run("threshold boundary just above", func(t *testing.T) {
		stats := &PoolStats{
			Ready:  6,
			Target: 10,
		}
		// 6/10 = 0.6 > 0.5
		if stats.NeedsReplenishment(0.5) {
			t.Error("NeedsReplenishment(0.5) should return false when ratio is above threshold")
		}
	})
}
