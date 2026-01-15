package domain

// PoolStats holds statistics about the warm pool state.
type PoolStats struct {
	Ready    int `json:"ready"`    // Instances available for immediate assignment
	Assigned int `json:"assigned"` // Instances currently in use
	Warming  int `json:"warming"`  // Instances being prepared
	Target   int `json:"target"`   // Target pool size
	Capacity int `json:"capacity"` // Maximum allowed instances
}

// NeedsReplenishment returns true if the pool is below the replenishment threshold.
func (s *PoolStats) NeedsReplenishment(threshold float64) bool {
	if s.Target == 0 {
		return false
	}
	return float64(s.Ready)/float64(s.Target) < threshold
}

// AvailableCapacity returns how many more instances can be created.
func (s *PoolStats) AvailableCapacity() int {
	used := s.Ready + s.Assigned + s.Warming
	if used >= s.Capacity {
		return 0
	}
	return s.Capacity - used
}

// ReplenishmentNeeded returns how many instances should be created.
func (s *PoolStats) ReplenishmentNeeded() int {
	gap := s.Target - s.Ready - s.Warming
	if gap <= 0 {
		return 0
	}
	available := s.AvailableCapacity()
	if gap > available {
		return available
	}
	return gap
}
