package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/internal/domain"
	"github.com/instant-demo/try-it-now/internal/metrics"
	"github.com/instant-demo/try-it-now/internal/pool"
	"github.com/instant-demo/try-it-now/internal/store"
	"github.com/instant-demo/try-it-now/pkg/logging"
)

// SSEMaxDuration is the maximum time an SSE connection can remain open.
// After this duration, the client must reconnect.
const SSEMaxDuration = 30 * time.Minute

// Handler holds the HTTP handlers and dependencies.
type Handler struct {
	cfg            *config.Config
	pool           pool.Manager
	store          store.Repository
	metrics        *metrics.Collector
	logger         *logging.Logger
	sseMaxDuration time.Duration // Configurable for testing, defaults to SSEMaxDuration
}

// NewHandler creates a new API handler.
func NewHandler(cfg *config.Config, poolMgr pool.Manager, repo store.Repository, m *metrics.Collector, logger *logging.Logger) *Handler {
	if logger == nil {
		logger = logging.Nop()
	}
	return &Handler{
		cfg:            cfg,
		pool:           poolMgr,
		store:          repo,
		metrics:        m,
		logger:         logger,
		sseMaxDuration: SSEMaxDuration,
	}
}

// Router returns the configured Gin router.
func (h *Handler) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Configure trusted proxies to prevent X-Forwarded-For spoofing.
	// Only requests from these IPs will have X-Forwarded-For headers trusted.
	if err := r.SetTrustedProxies(h.cfg.Server.TrustedProxies); err != nil {
		h.logger.Warn("Failed to set trusted proxies, IP detection may be unreliable",
			"error", err,
			"proxies", h.cfg.Server.TrustedProxies)
	}

	r.Use(gin.Recovery())
	r.Use(RequestID())
	r.Use(gin.Logger())
	r.Use(h.metricsMiddleware())

	// Health check (public - no auth required)
	r.GET("/health", h.health)

	// API v1 (protected - requires API key)
	v1 := r.Group("/api/v1")
	v1.Use(APIKeyAuth(h.cfg.Server.APIKey))
	{
		// Demo instance endpoints
		demo := v1.Group("/demo")
		{
			demo.POST("/acquire", h.acquireDemo)
			demo.GET("/:id", h.getDemo)
			demo.POST("/:id/extend", h.extendDemo)
			demo.DELETE("/:id", h.releaseDemo)
			demo.GET("/:id/status", h.demoStatus)
		}

		// Pool management endpoints
		poolGroup := v1.Group("/pool")
		{
			poolGroup.GET("/stats", h.poolStats)
		}
	}

	// Prometheus metrics (protected - requires API key)
	metricsGroup := r.Group("")
	metricsGroup.Use(APIKeyAuth(h.cfg.Server.APIKey))
	if h.metrics != nil {
		metricsGroup.GET("/metrics", gin.WrapH(h.metrics.Handler()))
	} else {
		// Stub handler when metrics is nil (for tests)
		metricsGroup.GET("/metrics", func(c *gin.Context) {
			c.String(http.StatusOK, "# metrics disabled\n")
		})
	}

	return r
}

// metricsMiddleware records HTTP request duration metrics.
func (h *Handler) metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.metrics == nil {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}

		h.metrics.HTTPRequestDuration.WithLabelValues(
			c.Request.Method,
			path,
			strconv.Itoa(c.Writer.Status()),
		).Observe(time.Since(start).Seconds())
	}
}

// Response types

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

type ExtendRequest struct {
	Minutes int `json:"minutes" binding:"required,min=1,max=60"`
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

// HealthResponse for GET /health
type HealthResponse struct {
	Status string `json:"status"`
	Mode   string `json:"mode,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ExtendResponse for POST /demo/:id/extend
type ExtendResponse struct {
	ID           string     `json:"id"`
	ExpiresAt    *time.Time `json:"expires_at"`
	TTLRemaining int        `json:"ttl_remaining"`
}

// ReleaseResponse for DELETE /demo/:id
type ReleaseResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// health returns a simple health check response.
func (h *Handler) health(c *gin.Context) {
	// Check store connectivity
	if err := h.store.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, HealthResponse{
			Status: "unhealthy",
			Error:  "store unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, HealthResponse{
		Status: "ok",
		Mode:   h.cfg.Container.Mode,
	})
}

// acquireDemo acquires an instance from the warm pool.
func (h *Handler) acquireDemo(c *gin.Context) {
	ctx := c.Request.Context()
	start := time.Now()

	// Get client IP for rate limiting
	clientIP := c.ClientIP()

	// Atomically check and increment rate limit
	// This prevents TOCTOU race conditions where concurrent requests could all pass
	// the check before any increment occurs
	allowed, err := h.store.CheckAndIncrementRateLimit(ctx, clientIP, h.cfg.RateLimit.RequestsPerHour, h.cfg.RateLimit.RequestsPerDay)
	if err != nil {
		if h.metrics != nil {
			h.metrics.AcquisitionsTotal.WithLabelValues("error").Inc()
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to check rate limit",
			Code:  "RATE_LIMIT_ERROR",
		})
		return
	}
	if !allowed {
		if h.metrics != nil {
			h.metrics.RateLimitHitsTotal.Inc()
			h.metrics.AcquisitionsTotal.WithLabelValues("rate_limited").Inc()
		}
		c.JSON(http.StatusTooManyRequests, ErrorResponse{
			Error: "Rate limit exceeded",
			Code:  "RATE_LIMITED",
			Details: fmt.Sprintf("Limit: %d per hour, %d per day",
				h.cfg.RateLimit.RequestsPerHour, h.cfg.RateLimit.RequestsPerDay),
		})
		return
	}

	// Acquire from pool
	instance, err := h.pool.Acquire(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrPoolExhausted) {
			if h.metrics != nil {
				h.metrics.AcquisitionsTotal.WithLabelValues("pool_exhausted").Inc()
			}
			c.JSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "No demo instances available",
				Code:    "POOL_EXHAUSTED",
				Details: "Please try again in a few moments",
			})
			return
		}
		if errors.Is(err, domain.ErrRouteCreationFailed) {
			if h.metrics != nil {
				h.metrics.AcquisitionsTotal.WithLabelValues("route_failed").Inc()
			}
			c.JSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "Failed to configure routing for instance",
				Code:    "ROUTE_FAILED",
				Details: "Please try again in a few moments",
			})
			return
		}
		if h.metrics != nil {
			h.metrics.AcquisitionsTotal.WithLabelValues("error").Inc()
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to acquire demo instance",
			Code:  "ACQUIRE_ERROR",
		})
		return
	}

	// Record successful acquisition
	if h.metrics != nil {
		h.metrics.AcquisitionsTotal.WithLabelValues("success").Inc()
		h.metrics.AcquisitionDuration.Observe(time.Since(start).Seconds())
	}

	// Rate limit was already incremented atomically above

	// Set user IP on instance for audit/analytics purposes.
	// Instance is already successfully acquired - failing to persist the IP update
	// does not affect user experience or instance functionality.
	instance.UserIP = clientIP
	if err := h.store.SaveInstance(ctx, instance); err != nil {
		h.logger.Warn("Failed to save user IP on instance",
			"instanceID", instance.ID,
			"error", err)
	}

	// Build response
	resp := AcquireResponse{
		ID:       instance.ID,
		URL:      instance.URL(h.cfg.Proxy.BaseDomain),
		AdminURL: instance.AdminURL(h.cfg.Proxy.BaseDomain, h.cfg.PrestaShop.AdminPath),
	}
	if instance.ExpiresAt != nil {
		resp.ExpiresAt = *instance.ExpiresAt
		resp.TTL = int(instance.TTLRemaining().Seconds())
	}

	c.JSON(http.StatusOK, resp)
}

// getDemo returns information about a demo instance.
func (h *Handler) getDemo(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	instance, err := h.store.GetInstance(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Instance not found",
				Code:  "NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to get instance",
			Code:  "GET_ERROR",
		})
		return
	}

	resp := InstanceResponse{
		ID:           instance.ID,
		URL:          instance.URL(h.cfg.Proxy.BaseDomain),
		AdminURL:     instance.AdminURL(h.cfg.Proxy.BaseDomain, h.cfg.PrestaShop.AdminPath),
		State:        string(instance.State),
		CreatedAt:    instance.CreatedAt,
		TTLRemaining: int(instance.TTLRemaining().Seconds()),
	}
	if instance.AssignedAt != nil {
		resp.AssignedAt = *instance.AssignedAt
	}
	if instance.ExpiresAt != nil {
		resp.ExpiresAt = *instance.ExpiresAt
	}

	c.JSON(http.StatusOK, resp)
}

// extendDemo extends the TTL of a demo instance.
func (h *Handler) extendDemo(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	var req ExtendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request",
			Code:    "INVALID_REQUEST",
			Details: "minutes must be between 1 and 60",
		})
		return
	}

	extension := time.Duration(req.Minutes) * time.Minute

	// Atomically extend TTL with max TTL check
	// This prevents TOCTOU race conditions where concurrent extensions could all pass
	// the max TTL check before any extension is applied
	result, err := h.store.ExtendInstanceTTLAtomic(ctx, id, extension, h.cfg.Pool.MaxTTL)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Instance not found",
				Code:  "NOT_FOUND",
			})
			return
		}
		if errors.Is(err, domain.ErrMaxTTLExceeded) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "Extension would exceed maximum TTL",
				Code:    "MAX_TTL_EXCEEDED",
				Details: fmt.Sprintf("Maximum TTL is %v, current remaining: %v", h.cfg.Pool.MaxTTL, result.Remaining),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to extend TTL",
			Code:  "EXTEND_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, ExtendResponse{
		ID:           id,
		TTLRemaining: int(result.Remaining.Seconds()),
		ExpiresAt:    &result.NewExpiresAt,
	})
}

// releaseDemo releases a demo instance early.
func (h *Handler) releaseDemo(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	// Verify instance exists
	_, err := h.store.GetInstance(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Instance not found",
				Code:  "NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to get instance",
			Code:  "GET_ERROR",
		})
		return
	}

	// Release via pool manager
	if err := h.pool.Release(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to release instance",
			Code:  "RELEASE_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, ReleaseResponse{
		ID:      id,
		Status:  "released",
		Message: "Demo instance has been released",
	})
}

// demoStatus returns SSE stream for TTL countdown.
func (h *Handler) demoStatus(c *gin.Context) {
	id := c.Param("id")

	// Create a context with maximum SSE duration to prevent indefinite connections
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.sseMaxDuration)
	defer cancel()

	// Verify instance exists
	instance, err := h.store.GetInstance(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error: "Instance not found",
				Code:  "NOT_FOUND",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to get instance",
			Code:  "GET_ERROR",
		})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Stream TTL updates
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				// Max duration reached, tell client to reconnect
				c.SSEvent("reconnect", gin.H{"reason": "max duration reached"})
				c.Writer.Flush()
			}
			return
		case <-ticker.C:
			// Refresh instance data
			instance, err = h.store.GetInstance(ctx, id)
			if err != nil {
				c.SSEvent("error", gin.H{"error": "Instance not found"})
				return
			}

			ttl := int(instance.TTLRemaining().Seconds())
			c.SSEvent("ttl", gin.H{
				"id":            id,
				"ttl_remaining": ttl,
				"state":         string(instance.State),
			})
			c.Writer.Flush()

			// Stop streaming if expired
			if instance.IsExpired() {
				c.SSEvent("expired", gin.H{"id": id})
				return
			}
		}
	}
}

// poolStats returns current pool statistics.
func (h *Handler) poolStats(c *gin.Context) {
	ctx := c.Request.Context()

	stats, err := h.pool.Stats(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to get pool stats",
			Code:  "STATS_ERROR",
		})
		return
	}

	c.JSON(http.StatusOK, StatsResponse{
		Ready:    stats.Ready,
		Assigned: stats.Assigned,
		Warming:  stats.Warming,
		Target:   stats.Target,
		Capacity: stats.Capacity,
	})
}
