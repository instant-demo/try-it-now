package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/domain"
	"github.com/boss/demo-multiplexer/internal/metrics"
	"github.com/boss/demo-multiplexer/internal/pool"
	"github.com/boss/demo-multiplexer/internal/store"
	"github.com/gin-gonic/gin"
)

// Handler holds the HTTP handlers and dependencies.
type Handler struct {
	cfg     *config.Config
	pool    pool.Manager
	store   store.Repository
	metrics *metrics.Collector
}

// NewHandler creates a new API handler.
func NewHandler(cfg *config.Config, poolMgr pool.Manager, repo store.Repository, m *metrics.Collector) *Handler {
	return &Handler{
		cfg:     cfg,
		pool:    poolMgr,
		store:   repo,
		metrics: m,
	}
}

// Router returns the configured Gin router.
func (h *Handler) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	r.Use(h.metricsMiddleware())

	// Health check
	r.GET("/health", h.health)

	// API v1
	v1 := r.Group("/api/v1")
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

	// Prometheus metrics
	if h.metrics != nil {
		r.GET("/metrics", gin.WrapH(h.metrics.Handler()))
	} else {
		// Stub handler when metrics is nil (for tests)
		r.GET("/metrics", func(c *gin.Context) {
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

// health returns a simple health check response.
func (h *Handler) health(c *gin.Context) {
	// Check store connectivity
	if err := h.store.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"error":  "store unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"mode":   h.cfg.Container.Mode,
	})
}

// acquireDemo acquires an instance from the warm pool.
func (h *Handler) acquireDemo(c *gin.Context) {
	ctx := c.Request.Context()
	start := time.Now()

	// Get client IP for rate limiting
	clientIP := c.ClientIP()

	// Check rate limit
	allowed, err := h.store.CheckRateLimit(ctx, clientIP, h.cfg.RateLimit.RequestsPerHour, h.cfg.RateLimit.RequestsPerDay)
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

	// Increment rate limit
	if err := h.store.IncrementRateLimit(ctx, clientIP); err != nil {
		// Log but don't fail - instance is already assigned
	}

	// Set user IP on instance
	instance.UserIP = clientIP
	_ = h.store.SaveInstance(ctx, instance)

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

	// Get instance to check current TTL
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

	// Check if extension would exceed max TTL
	extension := time.Duration(req.Minutes) * time.Minute
	newTTL := instance.TTLRemaining() + extension
	if newTTL > h.cfg.Pool.MaxTTL {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Extension would exceed maximum TTL",
			Code:    "MAX_TTL_EXCEEDED",
			Details: fmt.Sprintf("Maximum TTL is %v", h.cfg.Pool.MaxTTL),
		})
		return
	}

	// Extend TTL
	if err := h.store.ExtendInstanceTTL(ctx, id, extension); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Failed to extend TTL",
			Code:  "EXTEND_ERROR",
		})
		return
	}

	// Get updated instance
	instance, _ = h.store.GetInstance(ctx, id)

	c.JSON(http.StatusOK, gin.H{
		"id":            instance.ID,
		"ttl_remaining": int(instance.TTLRemaining().Seconds()),
		"expires_at":    instance.ExpiresAt,
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

	c.JSON(http.StatusOK, gin.H{
		"id":      id,
		"status":  "released",
		"message": "Demo instance has been released",
	})
}

// demoStatus returns SSE stream for TTL countdown.
func (h *Handler) demoStatus(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

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
