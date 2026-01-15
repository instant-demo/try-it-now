// Package metrics provides Prometheus instrumentation for the demo multiplexer.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Histogram bucket definitions optimized for sub-500ms performance target.
var (
	// Fast operations (CRIU restore, route add) - target <500ms
	fastBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.0}

	// Medium operations (acquisition end-to-end)
	mediumBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

	// Slow operations (cold start provisioning) - can take minutes
	slowBuckets = []float64{1, 5, 10, 30, 60, 120, 180, 300, 600}
)

// Collector holds all Prometheus metrics for the demo multiplexer.
type Collector struct {
	// Gauges - Current pool state
	PoolReady     prometheus.Gauge
	PoolAssigned  prometheus.Gauge
	PoolWarming   prometheus.Gauge
	PoolTarget    prometheus.Gauge
	PoolCapacity  prometheus.Gauge
	CRIUAvailable prometheus.Gauge

	// Counters - Cumulative events
	AcquisitionsTotal  *prometheus.CounterVec
	ReleasesTotal      *prometheus.CounterVec
	ProvisionsTotal    *prometheus.CounterVec
	RateLimitHitsTotal prometheus.Counter
	HealthChecksTotal  *prometheus.CounterVec
	RouteOpsTotal      *prometheus.CounterVec

	// Histograms - Latency distributions
	AcquisitionDuration  prometheus.Histogram
	ProvisionDuration    *prometheus.HistogramVec
	CRIURestoreDuration  prometheus.Histogram
	HealthCheckDuration  prometheus.Histogram
	WaitForReadyDuration prometheus.Histogram
	RouteAddDuration     prometheus.Histogram
	RouteRemoveDuration  prometheus.Histogram
	HTTPRequestDuration  *prometheus.HistogramVec

	registry *prometheus.Registry
}

// NewCollector creates a new metrics collector with all metrics registered.
func NewCollector() *Collector {
	reg := prometheus.NewRegistry()

	c := &Collector{
		// Gauges
		PoolReady: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "demo",
			Subsystem: "pool",
			Name:      "ready_instances",
			Help:      "Number of ready instances in warm pool",
		}),
		PoolAssigned: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "demo",
			Subsystem: "pool",
			Name:      "assigned_instances",
			Help:      "Number of instances currently assigned to users",
		}),
		PoolWarming: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "demo",
			Subsystem: "pool",
			Name:      "warming_instances",
			Help:      "Number of instances being warmed up",
		}),
		PoolTarget: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "demo",
			Subsystem: "pool",
			Name:      "target_size",
			Help:      "Target pool size from configuration",
		}),
		PoolCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "demo",
			Subsystem: "pool",
			Name:      "capacity",
			Help:      "Maximum pool capacity",
		}),
		CRIUAvailable: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "demo",
			Name:      "criu_available",
			Help:      "Whether CRIU checkpoint/restore is available (1=yes, 0=no)",
		}),

		// Counters
		AcquisitionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "demo",
			Name:      "acquisitions_total",
			Help:      "Total number of instance acquisition attempts",
		}, []string{"result"}),
		ReleasesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "demo",
			Name:      "releases_total",
			Help:      "Total number of instance releases",
		}, []string{"reason"}),
		ProvisionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "demo",
			Name:      "provisions_total",
			Help:      "Total number of instance provisioning attempts",
		}, []string{"method", "result"}),
		RateLimitHitsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "demo",
			Name:      "rate_limit_hits_total",
			Help:      "Total number of rate limit rejections",
		}),
		HealthChecksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "demo",
			Name:      "health_checks_total",
			Help:      "Total number of container health checks",
		}, []string{"result"}),
		RouteOpsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "demo",
			Name:      "route_operations_total",
			Help:      "Total number of route operations",
		}, []string{"operation", "result"}),

		// Histograms
		AcquisitionDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "acquisition_duration_seconds",
			Help:      "End-to-end acquisition latency in seconds",
			Buckets:   mediumBuckets,
		}),
		ProvisionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "provision_duration_seconds",
			Help:      "Time to provision a new instance in seconds",
			Buckets:   slowBuckets,
		}, []string{"method"}),
		CRIURestoreDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "criu_restore_duration_seconds",
			Help:      "CRIU checkpoint restore latency in seconds",
			Buckets:   fastBuckets,
		}),
		HealthCheckDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "health_check_duration_seconds",
			Help:      "Single health check latency in seconds",
			Buckets:   fastBuckets,
		}),
		WaitForReadyDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "wait_for_ready_duration_seconds",
			Help:      "Total time waiting for container to become ready in seconds",
			Buckets:   slowBuckets,
		}),
		RouteAddDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "route_add_duration_seconds",
			Help:      "Caddy route add latency in seconds",
			Buckets:   fastBuckets,
		}),
		RouteRemoveDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "route_remove_duration_seconds",
			Help:      "Caddy route remove latency in seconds",
			Buckets:   fastBuckets,
		}),
		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "demo",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency in seconds",
			Buckets:   mediumBuckets,
		}, []string{"method", "path", "status"}),

		registry: reg,
	}

	// Register all metrics
	reg.MustRegister(
		// Gauges
		c.PoolReady,
		c.PoolAssigned,
		c.PoolWarming,
		c.PoolTarget,
		c.PoolCapacity,
		c.CRIUAvailable,
		// Counters
		c.AcquisitionsTotal,
		c.ReleasesTotal,
		c.ProvisionsTotal,
		c.RateLimitHitsTotal,
		c.HealthChecksTotal,
		c.RouteOpsTotal,
		// Histograms
		c.AcquisitionDuration,
		c.ProvisionDuration,
		c.CRIURestoreDuration,
		c.HealthCheckDuration,
		c.WaitForReadyDuration,
		c.RouteAddDuration,
		c.RouteRemoveDuration,
		c.HTTPRequestDuration,
	)

	return c
}

// Handler returns an HTTP handler for the metrics endpoint.
func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}
