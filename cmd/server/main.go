package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/instant-demo/try-it-now/internal/api"
	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/internal/container"
	"github.com/instant-demo/try-it-now/internal/database"
	"github.com/instant-demo/try-it-now/internal/metrics"
	"github.com/instant-demo/try-it-now/internal/pool"
	"github.com/instant-demo/try-it-now/internal/proxy"
	"github.com/instant-demo/try-it-now/internal/queue"
	"github.com/instant-demo/try-it-now/internal/store"
	"github.com/instant-demo/try-it-now/pkg/logging"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if present (ignore error if file doesn't exist)
	_ = godotenv.Load()

	cfg := config.Load()

	// Initialize structured logger
	logger := logging.New(cfg.Log.Level, cfg.Log.Format)

	// Initialize metrics collector
	metricsCollector := metrics.NewCollector()

	logger.Info("Starting Try It Now", "mode", cfg.Container.Mode)

	// Create Valkey repository
	repo, err := store.NewValkeyRepository(&cfg.Store, &cfg.Container)
	if err != nil {
		logger.Fatal("Failed to connect to Valkey", "error", err)
	}
	defer repo.Close()

	// Initialize ports in Valkey
	ctx := context.Background()
	if err := repo.InitializePorts(ctx); err != nil {
		logger.Fatal("Failed to initialize ports", "error", err)
	}

	// Create PrestaShop database connection for post-CRIU operations
	var psDB *database.PrestaShopDB
	psDB, err = database.NewPrestaShopDB(&cfg.PrestaShop)
	if err != nil {
		// Database is optional - warn but continue without it
		logger.Warn("Failed to connect to PrestaShop database - CRIU post-restore operations will be skipped", "error", err)
		psDB = nil
	} else {
		defer psDB.Close()
		logger.Info("Connected to PrestaShop database")
	}

	// Create NATS Publisher
	publisher, err := queue.NewNATSPublisher(&cfg.Queue)
	if err != nil {
		logger.Fatal("Failed to create NATS publisher", "error", err)
	}
	defer publisher.Close()
	logger.Info("Connected to NATS JetStream")

	// Create container runtime based on configuration
	var runtime container.Runtime
	var runtimeCloser func() error

	switch cfg.Container.Mode {
	case "podman":
		podmanRuntime, err := container.NewPodmanRuntime(&cfg.Container, &cfg.PrestaShop, &cfg.Proxy, logger)
		if err != nil {
			logger.Fatal("Failed to create Podman runtime", "error", err)
		}
		runtime = podmanRuntime
		runtimeCloser = podmanRuntime.Close
		logger.Info("CRIU checkpoint/restore status", "available", podmanRuntime.CRIUAvailable())
	case "docker":
		fallthrough
	default:
		dockerRuntime, err := container.NewDockerRuntime(&cfg.Container, &cfg.PrestaShop, &cfg.Proxy, logger)
		if err != nil {
			logger.Fatal("Failed to create Docker runtime", "error", err)
		}
		runtime = dockerRuntime
		runtimeCloser = dockerRuntime.Close
	}
	defer runtimeCloser()

	// Create Caddy route manager
	proxyMgr := proxy.NewCaddyRouteManager(&cfg.Proxy, metricsCollector)

	// Create pool manager
	poolCfg := pool.ManagerConfig{
		TargetPoolSize:     cfg.Pool.TargetSize,
		MinPoolSize:        cfg.Pool.MinSize,
		MaxPoolSize:        cfg.Pool.MaxSize,
		ReplenishThreshold: cfg.Pool.ReplenishThreshold,
		ReplenishInterval:  cfg.Pool.ReplenishInterval,
		DefaultTTL:         cfg.Pool.DefaultTTL,
		MaxTTL:             cfg.Pool.MaxTTL,
	}
	poolMgr := pool.NewPoolManager(
		poolCfg,
		repo,
		runtime,
		proxyMgr,
		psDB,
		cfg.Container.Image,
		cfg.Container.Network,
		cfg.Proxy.BaseDomain,
		cfg.Container.CheckpointPath,
		logger,
		metricsCollector,
	)

	// Create NATS Consumer with real handlers
	handlers := queue.NewHandlers(poolMgr, runtime, psDB, proxyMgr, repo, logger)
	consumer, err := queue.NewNATSConsumer(&cfg.Queue, handlers.ProvisionHandler, handlers.CleanupHandler, logger)
	if err != nil {
		logger.Fatal("Failed to create NATS consumer", "error", err)
	}
	if err := consumer.Start(ctx); err != nil {
		logger.Fatal("Failed to start NATS consumer", "error", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		consumer.Stop(stopCtx)
	}()
	logger.Info("Started NATS consumer with real handlers")

	// Start pool replenisher
	if err := poolMgr.StartReplenisher(ctx); err != nil {
		logger.Fatal("Failed to start replenisher", "error", err)
	}
	defer poolMgr.StopReplenisher()

	// Start metrics gauge updater
	gaugeCtx, cancelGauge := context.WithCancel(ctx)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Recovered from panic in metrics updater",
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-gaugeCtx.Done():
				return
			case <-ticker.C:
				if stats, err := poolMgr.Stats(gaugeCtx); err == nil {
					metricsCollector.PoolReady.Set(float64(stats.Ready))
					metricsCollector.PoolAssigned.Set(float64(stats.Assigned))
					metricsCollector.PoolWarming.Set(float64(stats.Warming))
					metricsCollector.PoolTarget.Set(float64(stats.Target))
					metricsCollector.PoolCapacity.Set(float64(stats.Capacity))
				}
			}
		}
	}()
	defer cancelGauge()

	// Create API handler
	handler := api.NewHandler(cfg, poolMgr, repo, metricsCollector, logger)

	// Create HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Channel to receive server errors from goroutine
	serverErrCh := make(chan error, 1)

	// Start server in goroutine
	go func() {
		logger.Info("Server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			serverErrCh <- err
		}
	}()

	// Wait for interrupt signal or server error
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("Received signal, shutting down", "signal", sig)
	case err := <-serverErrCh:
		logger.Error("Server failed, initiating shutdown", "error", err)
	}

	logger.Info("Shutting down server")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("Server forced to shutdown", "error", err)
	}

	logger.Info("Server stopped")
}
