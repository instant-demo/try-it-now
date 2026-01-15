package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/boss/demo-multiplexer/internal/api"
	"github.com/boss/demo-multiplexer/internal/config"
	"github.com/boss/demo-multiplexer/internal/container"
	"github.com/boss/demo-multiplexer/internal/pool"
	"github.com/boss/demo-multiplexer/internal/proxy"
	"github.com/boss/demo-multiplexer/internal/store"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file if present (ignore error if file doesn't exist)
	_ = godotenv.Load()

	cfg := config.Load()

	log.Printf("Starting Demo Multiplexer (mode=%s)", cfg.Container.Mode)

	// Create Valkey repository
	repo, err := store.NewValkeyRepository(&cfg.Store, &cfg.Container)
	if err != nil {
		log.Fatalf("Failed to connect to Valkey: %v", err)
	}
	defer repo.Close()

	// Initialize ports in Valkey
	ctx := context.Background()
	if err := repo.InitializePorts(ctx); err != nil {
		log.Fatalf("Failed to initialize ports: %v", err)
	}

	// Create container runtime
	runtime, err := container.NewDockerRuntime(&cfg.Container, &cfg.PrestaShop, &cfg.Proxy)
	if err != nil {
		log.Fatalf("Failed to create Docker runtime: %v", err)
	}
	defer runtime.Close()

	// Create Caddy route manager
	proxyMgr := proxy.NewCaddyRouteManager(&cfg.Proxy)

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
	)

	// Start pool replenisher
	if err := poolMgr.StartReplenisher(ctx); err != nil {
		log.Fatalf("Failed to start replenisher: %v", err)
	}
	defer poolMgr.StopReplenisher()

	// Create API handler
	handler := api.NewHandler(cfg, poolMgr, repo)

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
		log.Printf("Server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}
