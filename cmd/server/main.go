package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/boss/demo-multiplexer/internal/api"
	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/container"
	"github.com/boss/demo-multiplexer/internal/metrics"
	"github.com/boss/demo-multiplexer/internal/pool"
	"github.com/boss/demo-multiplexer/internal/proxy"
	"github.com/boss/demo-multiplexer/internal/queue"
	"github.com/boss/demo-multiplexer/internal/store"
	"github.com/boss/demo-multiplexer/pkg/logging"
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

	logger.Info("Starting Demo Multiplexer", "mode", cfg.Container.Mode)

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

	// Create NATS Publisher
	publisher, err := queue.NewNATSPublisher(&cfg.Queue)
	if err != nil {
		logger.Fatal("Failed to create NATS publisher", "error", err)
	}
	defer publisher.Close()
	logger.Info("Connected to NATS JetStream")

	// Create NATS Consumer with stub handlers
	// TODO: Replace with real handlers that call poolMgr methods
	queueLogger := logger.With("component", "queue")
	provisionHandler := func(ctx context.Context, task queue.ProvisionTask) error {
		queueLogger.Debug("Provision handler called", "taskID", task.TaskID, "priority", task.Priority)
		return nil
	}
	cleanupHandler := func(ctx context.Context, task queue.CleanupTask) error {
		queueLogger.Debug("Cleanup handler called", "instanceID", task.InstanceID, "containerID", task.ContainerID)
		return nil
	}

	consumer, err := queue.NewNATSConsumer(&cfg.Queue, provisionHandler, cleanupHandler, logger)
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
		cfg.Container.Image,
		cfg.Container.Network,
		cfg.Proxy.BaseDomain,
		cfg.Container.CheckpointPath,
		logger,
		metricsCollector,
	)

	// Start pool replenisher
	if err := poolMgr.StartReplenisher(ctx); err != nil {
		logger.Fatal("Failed to start replenisher", "error", err)
	}
	defer poolMgr.StopReplenisher()

	// Start metrics gauge updater
	gaugeCtx, cancelGauge := context.WithCancel(ctx)
	go func() {
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
	handler := api.NewHandler(cfg, poolMgr, repo, metricsCollector)

	// Create HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in goroutine
	go func() {
		logger.Info("Server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("Server forced to shutdown", "error", err)
	}

	logger.Info("Server stopped")
}
