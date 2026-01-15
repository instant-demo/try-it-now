package queue

import (
	"context"
	"time"
)

// Publisher defines the interface for publishing tasks to the queue.
// Implementation: NATS JetStream.
type Publisher interface {
	// PublishProvisionTask queues a new instance provisioning task.
	PublishProvisionTask(ctx context.Context, task ProvisionTask) error

	// PublishCleanupTask queues a cleanup task.
	PublishCleanupTask(ctx context.Context, task CleanupTask) error

	// Close closes the publisher connection.
	Close() error
}

// Consumer defines the interface for consuming tasks from the queue.
type Consumer interface {
	// Start begins consuming messages and processing them with the handler.
	Start(ctx context.Context) error

	// Stop gracefully stops the consumer.
	Stop(ctx context.Context) error
}

// ProvisionTask represents a request to create a new instance.
type ProvisionTask struct {
	TaskID    string    `json:"task_id"`
	Priority  int       `json:"priority"`   // Higher = more urgent
	CreatedAt time.Time `json:"created_at"`
}

// CleanupTask represents a request to clean up an instance.
type CleanupTask struct {
	TaskID      string    `json:"task_id"`
	InstanceID  string    `json:"instance_id"`
	ContainerID string    `json:"container_id"`
	DBPrefix    string    `json:"db_prefix"`
	Hostname    string    `json:"hostname"`
	Port        int       `json:"port"`
	CreatedAt   time.Time `json:"created_at"`
}

// ProvisionHandler processes provision tasks.
type ProvisionHandler func(ctx context.Context, task ProvisionTask) error

// CleanupHandler processes cleanup tasks.
type CleanupHandler func(ctx context.Context, task CleanupTask) error
