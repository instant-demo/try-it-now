package domain

import (
	"testing"
	"time"
)

func TestInstance_URL(t *testing.T) {
	tests := []struct {
		name       string
		instance   *Instance
		baseDomain string
		want       string
	}{
		{
			name: "standard hostname and domain",
			instance: &Instance{
				Hostname: "demo-a1b2c3d4",
			},
			baseDomain: "example.com",
			want:       "https://demo-a1b2c3d4.example.com",
		},
		{
			name: "empty hostname",
			instance: &Instance{
				Hostname: "",
			},
			baseDomain: "example.com",
			want:       "https://.example.com",
		},
		{
			name: "empty base domain",
			instance: &Instance{
				Hostname: "demo-xyz",
			},
			baseDomain: "",
			want:       "https://demo-xyz.",
		},
		{
			name: "both empty",
			instance: &Instance{
				Hostname: "",
			},
			baseDomain: "",
			want:       "https://.",
		},
		{
			name: "subdomain in base domain",
			instance: &Instance{
				Hostname: "demo-test",
			},
			baseDomain: "staging.example.com",
			want:       "https://demo-test.staging.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.instance.URL(tt.baseDomain)
			if got != tt.want {
				t.Errorf("Instance.URL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstance_AdminURL(t *testing.T) {
	tests := []struct {
		name       string
		instance   *Instance
		baseDomain string
		adminPath  string
		want       string
	}{
		{
			name: "standard admin path",
			instance: &Instance{
				Hostname: "demo-a1b2c3d4",
			},
			baseDomain: "example.com",
			adminPath:  "admin123",
			want:       "https://demo-a1b2c3d4.example.com/admin123",
		},
		{
			name: "empty admin path",
			instance: &Instance{
				Hostname: "demo-xyz",
			},
			baseDomain: "example.com",
			adminPath:  "",
			want:       "https://demo-xyz.example.com/",
		},
		{
			name: "admin path with subdirectories",
			instance: &Instance{
				Hostname: "demo-test",
			},
			baseDomain: "example.com",
			adminPath:  "admin/dashboard",
			want:       "https://demo-test.example.com/admin/dashboard",
		},
		{
			name: "all empty",
			instance: &Instance{
				Hostname: "",
			},
			baseDomain: "",
			adminPath:  "",
			want:       "https://./",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.instance.AdminURL(tt.baseDomain, tt.adminPath)
			if got != tt.want {
				t.Errorf("Instance.AdminURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstance_IsExpired(t *testing.T) {
	now := time.Now()
	pastTime := now.Add(-1 * time.Hour)
	futureTime := now.Add(1 * time.Hour)

	tests := []struct {
		name     string
		instance *Instance
		want     bool
	}{
		{
			name: "nil ExpiresAt returns false",
			instance: &Instance{
				ExpiresAt: nil,
			},
			want: false,
		},
		{
			name: "expired instance (past time)",
			instance: &Instance{
				ExpiresAt: &pastTime,
			},
			want: true,
		},
		{
			name: "not expired instance (future time)",
			instance: &Instance{
				ExpiresAt: &futureTime,
			},
			want: false,
		},
		{
			name: "expired by one second",
			instance: &Instance{
				ExpiresAt: func() *time.Time {
					t := now.Add(-1 * time.Second)
					return &t
				}(),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.instance.IsExpired()
			if got != tt.want {
				t.Errorf("Instance.IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInstance_TTLRemaining(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		instance *Instance
		wantZero bool
		wantMin  time.Duration // minimum expected value (for non-zero cases)
		wantMax  time.Duration // maximum expected value (for non-zero cases)
	}{
		{
			name: "nil ExpiresAt returns zero",
			instance: &Instance{
				ExpiresAt: nil,
			},
			wantZero: true,
		},
		{
			name: "expired instance returns zero",
			instance: &Instance{
				ExpiresAt: func() *time.Time {
					t := now.Add(-1 * time.Hour)
					return &t
				}(),
			},
			wantZero: true,
		},
		{
			name: "future expiry returns positive duration",
			instance: &Instance{
				ExpiresAt: func() *time.Time {
					t := now.Add(30 * time.Minute)
					return &t
				}(),
			},
			wantZero: false,
			wantMin:  29 * time.Minute,
			wantMax:  31 * time.Minute,
		},
		{
			name: "one hour remaining",
			instance: &Instance{
				ExpiresAt: func() *time.Time {
					t := now.Add(1 * time.Hour)
					return &t
				}(),
			},
			wantZero: false,
			wantMin:  59 * time.Minute,
			wantMax:  61 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.instance.TTLRemaining()
			if tt.wantZero {
				if got != 0 {
					t.Errorf("Instance.TTLRemaining() = %v, want 0", got)
				}
			} else {
				if got < tt.wantMin || got > tt.wantMax {
					t.Errorf("Instance.TTLRemaining() = %v, want between %v and %v", got, tt.wantMin, tt.wantMax)
				}
			}
		})
	}
}

func TestInstance_TTLRemaining_BoundaryConditions(t *testing.T) {
	// Test that negative durations are clamped to zero
	t.Run("just expired returns zero", func(t *testing.T) {
		pastTime := time.Now().Add(-1 * time.Millisecond)
		instance := &Instance{
			ExpiresAt: &pastTime,
		}
		got := instance.TTLRemaining()
		if got != 0 {
			t.Errorf("Instance.TTLRemaining() = %v, want 0 for expired instance", got)
		}
	})

	// Test very far in the future
	t.Run("far future expiry", func(t *testing.T) {
		futureTime := time.Now().Add(24 * 365 * time.Hour) // ~1 year
		instance := &Instance{
			ExpiresAt: &futureTime,
		}
		got := instance.TTLRemaining()
		if got <= 0 {
			t.Errorf("Instance.TTLRemaining() = %v, want positive duration for far future expiry", got)
		}
	})
}

func TestInstanceState_Constants(t *testing.T) {
	// Verify the state constants have expected values
	tests := []struct {
		state InstanceState
		want  string
	}{
		{StateWarming, "warming"},
		{StateReady, "ready"},
		{StateAssigned, "assigned"},
		{StateExpired, "expired"},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if string(tt.state) != tt.want {
				t.Errorf("InstanceState = %q, want %q", tt.state, tt.want)
			}
		})
	}
}

func TestInstance_FullStruct(t *testing.T) {
	// Test that a fully populated Instance struct works correctly
	now := time.Now()
	assignedAt := now.Add(-30 * time.Minute)
	expiresAt := now.Add(30 * time.Minute)

	instance := &Instance{
		ID:          "inst-12345",
		ContainerID: "container-abc123",
		Hostname:    "demo-a1b2c3d4",
		Port:        8080,
		State:       StateAssigned,
		DBPrefix:    "a1b2c3d4_",
		UserIP:      "192.168.1.100",
		CreatedAt:   now.Add(-1 * time.Hour),
		AssignedAt:  &assignedAt,
		ExpiresAt:   &expiresAt,
	}

	t.Run("URL generation", func(t *testing.T) {
		url := instance.URL("demo.example.com")
		expected := "https://demo-a1b2c3d4.demo.example.com"
		if url != expected {
			t.Errorf("URL() = %q, want %q", url, expected)
		}
	})

	t.Run("AdminURL generation", func(t *testing.T) {
		adminURL := instance.AdminURL("demo.example.com", "admin456")
		expected := "https://demo-a1b2c3d4.demo.example.com/admin456"
		if adminURL != expected {
			t.Errorf("AdminURL() = %q, want %q", adminURL, expected)
		}
	})

	t.Run("not expired", func(t *testing.T) {
		if instance.IsExpired() {
			t.Error("IsExpired() = true, want false")
		}
	})

	t.Run("TTL remaining is positive", func(t *testing.T) {
		ttl := instance.TTLRemaining()
		if ttl <= 0 {
			t.Errorf("TTLRemaining() = %v, want positive duration", ttl)
		}
	})
}
