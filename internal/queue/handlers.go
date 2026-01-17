package queue

import (
	"context"

	"github.com/instant-demo/try-it-now/internal/container"
	"github.com/instant-demo/try-it-now/internal/database"
	"github.com/instant-demo/try-it-now/internal/proxy"
	"github.com/instant-demo/try-it-now/internal/store"
	"github.com/instant-demo/try-it-now/pkg/logging"
)

// Provisioner provisions new instances for the pool.
type Provisioner interface {
	// ProvisionOne provisions a single instance and adds it to the pool.
	ProvisionOne(ctx context.Context) error
}

// Handlers processes NATS queue tasks.
type Handlers struct {
	provisioner Provisioner
	runtime     container.Runtime
	psDB        *database.PrestaShopDB // Optional - can be nil
	proxy       proxy.RouteManager
	repo        store.Repository
	logger      *logging.Logger
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(
	provisioner Provisioner,
	runtime container.Runtime,
	psDB *database.PrestaShopDB,
	proxyMgr proxy.RouteManager,
	repo store.Repository,
	logger *logging.Logger,
) *Handlers {
	return &Handlers{
		provisioner: provisioner,
		runtime:     runtime,
		psDB:        psDB,
		proxy:       proxyMgr,
		repo:        repo,
		logger:      logger.With("component", "queue-handlers"),
	}
}

// ProvisionHandler processes provision tasks by provisioning a new instance.
func (h *Handlers) ProvisionHandler(ctx context.Context, task ProvisionTask) error {
	h.logger.Info("Processing provision task", "taskID", task.TaskID, "priority", task.Priority)

	if err := h.provisioner.ProvisionOne(ctx); err != nil {
		h.logger.Error("Provision task failed", "taskID", task.TaskID, "error", err)
		return err
	}

	h.logger.Info("Provision task completed", "taskID", task.TaskID)
	return nil
}

// CleanupHandler processes cleanup tasks by stopping the container
// and releasing all associated resources.
//
// Operations are idempotent - if a resource is already deleted,
// the operation succeeds silently.
func (h *Handlers) CleanupHandler(ctx context.Context, task CleanupTask) error {
	h.logger.Info("Processing cleanup task",
		"taskID", task.TaskID,
		"instanceID", task.InstanceID,
		"containerID", task.ContainerID)

	// 1. Stop container (idempotent - ignores "not found" errors)
	if task.ContainerID != "" {
		if err := h.runtime.Stop(ctx, task.ContainerID); err != nil {
			h.logger.Warn("Failed to stop container (may already be stopped)",
				"containerID", task.ContainerID, "error", err)
			// Continue - container may already be gone
		} else {
			h.logger.Debug("Stopped container", "containerID", task.ContainerID)
		}
	}

	// 2. Drop prefixed database tables (idempotent - no tables is fine)
	if h.psDB != nil && task.DBPrefix != "" {
		if err := h.psDB.DropPrefixedTables(ctx, task.DBPrefix); err != nil {
			h.logger.Warn("Failed to drop prefixed tables",
				"dbPrefix", task.DBPrefix, "error", err)
			// Continue - tables may already be dropped
		} else {
			h.logger.Debug("Dropped prefixed tables", "dbPrefix", task.DBPrefix)
		}
	}

	// 3. Remove route from proxy (idempotent - Caddy returns 404 on not found)
	if task.Hostname != "" {
		if err := h.proxy.RemoveRoute(ctx, task.Hostname); err != nil {
			h.logger.Warn("Failed to remove route (may already be removed)",
				"hostname", task.Hostname, "error", err)
			// Continue - route may already be gone
		} else {
			h.logger.Debug("Removed route", "hostname", task.Hostname)
		}
	}

	// 4. Release port back to pool (idempotent - set add is idempotent)
	if task.Port > 0 {
		if err := h.repo.ReleasePort(ctx, task.Port); err != nil {
			h.logger.Warn("Failed to release port",
				"port", task.Port, "error", err)
			// Continue - port may already be available
		} else {
			h.logger.Debug("Released port", "port", task.Port)
		}
	}

	// 5. Delete instance record from store (idempotent - returns nil on not found)
	if task.InstanceID != "" {
		if err := h.repo.DeleteInstance(ctx, task.InstanceID); err != nil {
			h.logger.Warn("Failed to delete instance",
				"instanceID", task.InstanceID, "error", err)
			// Continue - instance may already be deleted
		} else {
			h.logger.Debug("Deleted instance", "instanceID", task.InstanceID)
		}
	}

	h.logger.Info("Cleanup task completed", "taskID", task.TaskID, "instanceID", task.InstanceID)
	return nil
}
