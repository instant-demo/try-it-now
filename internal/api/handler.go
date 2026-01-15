package api

import (
	"net/http"

	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/gin-gonic/gin"
)

// Handler holds the HTTP handlers and dependencies.
type Handler struct {
	cfg *config.Config
	// TODO: Add dependencies
	// pool    pool.Manager
	// store   store.Repository
	// runtime container.Runtime
	// proxy   proxy.RouteManager
}

// NewHandler creates a new API handler.
func NewHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg: cfg,
	}
}

// Router returns the configured Gin router.
func (h *Handler) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

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
			demo.GET("/:id/status", h.demoStatus) // SSE for TTL countdown
		}

		// Pool management endpoints
		pool := v1.Group("/pool")
		{
			pool.GET("/stats", h.poolStats)
		}
	}

	// Prometheus metrics (TODO)
	r.GET("/metrics", h.metrics)

	return r
}

// health returns a simple health check response.
func (h *Handler) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"mode":   h.cfg.Container.Mode,
	})
}

// acquireDemo acquires an instance from the warm pool.
func (h *Handler) acquireDemo(c *gin.Context) {
	// TODO: Implement
	// 1. Check rate limit
	// 2. Acquire from pool
	// 3. Set TTL
	// 4. Return instance info or redirect
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented",
	})
}

// getDemo returns information about a demo instance.
func (h *Handler) getDemo(c *gin.Context) {
	id := c.Param("id")
	// TODO: Implement
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented",
		"id":    id,
	})
}

// extendDemo extends the TTL of a demo instance.
func (h *Handler) extendDemo(c *gin.Context) {
	id := c.Param("id")
	// TODO: Implement
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented",
		"id":    id,
	})
}

// releaseDemo releases a demo instance early.
func (h *Handler) releaseDemo(c *gin.Context) {
	id := c.Param("id")
	// TODO: Implement
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented",
		"id":    id,
	})
}

// demoStatus returns SSE stream for TTL countdown.
func (h *Handler) demoStatus(c *gin.Context) {
	id := c.Param("id")
	// TODO: Implement SSE
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented",
		"id":    id,
	})
}

// poolStats returns current pool statistics.
func (h *Handler) poolStats(c *gin.Context) {
	// TODO: Implement
	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented",
	})
}

// metrics returns Prometheus metrics.
func (h *Handler) metrics(c *gin.Context) {
	// TODO: Implement
	c.String(http.StatusOK, "# TODO: Prometheus metrics")
}
