package domain

import "time"

// InstanceState represents the lifecycle state of a demo instance.
type InstanceState string

const (
	StateWarming  InstanceState = "warming"  // Being prepared in background
	StateReady    InstanceState = "ready"    // In warm pool, available for assignment
	StateAssigned InstanceState = "assigned" // Assigned to a user
	StateExpired  InstanceState = "expired"  // TTL exceeded, pending cleanup
)

// Instance represents a single PrestaShop demo instance.
type Instance struct {
	ID          string        `json:"id"`
	ContainerID string        `json:"container_id"`
	Hostname    string        `json:"hostname"` // e.g., "demo-a1b2c3d4"
	Port        int           `json:"port"`     // Host port mapping
	State       InstanceState `json:"state"`
	DBPrefix    string        `json:"db_prefix"` // e.g., "a1b2c3d4_"
	UserIP      string        `json:"user_ip,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	AssignedAt  *time.Time    `json:"assigned_at,omitempty"`
	ExpiresAt   *time.Time    `json:"expires_at,omitempty"`
}

// URL returns the public URL for this instance.
func (i *Instance) URL(baseDomain string) string {
	return "https://" + i.Hostname + "." + baseDomain
}

// AdminURL returns the admin panel URL for this instance.
func (i *Instance) AdminURL(baseDomain, adminPath string) string {
	return i.URL(baseDomain) + "/" + adminPath
}

// IsExpired returns true if the instance has passed its expiry time.
func (i *Instance) IsExpired() bool {
	if i.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*i.ExpiresAt)
}

// TTLRemaining returns the remaining time until expiry.
func (i *Instance) TTLRemaining() time.Duration {
	if i.ExpiresAt == nil {
		return 0
	}
	remaining := time.Until(*i.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}
