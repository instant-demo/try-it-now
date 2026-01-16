package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/instant-demo/try-it-now/internal/config"
	"github.com/instant-demo/try-it-now/pkg/logging"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// NATSPublisher implements Publisher using NATS JetStream.
type NATSPublisher struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	stream jetstream.Stream
	cfg    *config.QueueConfig
}

// Compile-time check that NATSPublisher implements Publisher.
var _ Publisher = (*NATSPublisher)(nil)

// NewNATSPublisher creates a new NATS JetStream publisher.
// It connects to NATS, creates the JetStream context, and ensures
// the stream exists with the required configuration.
func NewNATSPublisher(cfg *config.QueueConfig) (*NATSPublisher, error) {
	// Connect to NATS with retry options
	nc, err := nats.Connect(cfg.NATSURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// Create or update stream
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	streamConfig := jetstream.StreamConfig{
		Name:        cfg.StreamName,
		Description: "Demo instance provisioning and cleanup tasks",
		Subjects: []string{
			cfg.StreamName + ".provision",
			cfg.StreamName + ".cleanup",
		},
		Retention:    jetstream.WorkQueuePolicy,
		MaxConsumers: -1,
		MaxMsgs:      -1,
		MaxBytes:     -1,
		MaxAge:       24 * time.Hour,
		Storage:      jetstream.FileStorage,
		Replicas:     1,
		Discard:      jetstream.DiscardOld,
	}

	stream, err := js.CreateOrUpdateStream(ctx, streamConfig)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	return &NATSPublisher{
		nc:     nc,
		js:     js,
		stream: stream,
		cfg:    cfg,
	}, nil
}

// PublishProvisionTask publishes a provisioning task to the stream.
func (p *NATSPublisher) PublishProvisionTask(ctx context.Context, task ProvisionTask) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	subject := p.cfg.StreamName + ".provision"
	_, err = p.js.Publish(ctx, subject, data,
		jetstream.WithMsgID(task.TaskID),
	)
	if err != nil {
		return fmt.Errorf("failed to publish provision task: %w", err)
	}

	return nil
}

// PublishCleanupTask publishes a cleanup task to the stream.
func (p *NATSPublisher) PublishCleanupTask(ctx context.Context, task CleanupTask) error {
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to marshal task: %w", err)
	}

	subject := p.cfg.StreamName + ".cleanup"
	_, err = p.js.Publish(ctx, subject, data,
		jetstream.WithMsgID(task.TaskID),
	)
	if err != nil {
		return fmt.Errorf("failed to publish cleanup task: %w", err)
	}

	return nil
}

// Close closes the NATS connection.
func (p *NATSPublisher) Close() error {
	return p.nc.Drain()
}

// NATSConsumer implements Consumer using NATS JetStream pull consumers.
type NATSConsumer struct {
	nc               *nats.Conn
	js               jetstream.JetStream
	stream           jetstream.Stream
	provisionHandler ProvisionHandler
	cleanupHandler   CleanupHandler
	cfg              *config.QueueConfig
	logger           *logging.Logger

	stopCh  chan struct{}
	doneCh  chan struct{}
	mu      sync.Mutex
	running bool
}

// Compile-time check that NATSConsumer implements Consumer.
var _ Consumer = (*NATSConsumer)(nil)

// NewNATSConsumer creates a new NATS JetStream consumer.
// It accepts handler functions for provision and cleanup tasks.
func NewNATSConsumer(
	cfg *config.QueueConfig,
	provisionHandler ProvisionHandler,
	cleanupHandler CleanupHandler,
	logger *logging.Logger,
) (*NATSConsumer, error) {
	// Connect to NATS
	nc, err := nats.Connect(cfg.NATSURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// Get stream handle (must exist - created by publisher)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := js.Stream(ctx, cfg.StreamName)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to get stream %s: %w", cfg.StreamName, err)
	}

	return &NATSConsumer{
		nc:               nc,
		js:               js,
		stream:           stream,
		provisionHandler: provisionHandler,
		cleanupHandler:   cleanupHandler,
		cfg:              cfg,
		logger:           logger.With("component", "nats-consumer"),
	}, nil
}

// Start begins consuming messages with WorkerCount goroutines.
func (c *NATSConsumer) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("consumer already running")
	}
	c.stopCh = make(chan struct{})
	c.doneCh = make(chan struct{})
	c.running = true
	c.mu.Unlock()

	// Create durable consumers for each task type
	provisionConsumerConfig := jetstream.ConsumerConfig{
		Durable:       "provision-workers",
		Description:   "Workers that process provisioning tasks",
		FilterSubject: c.cfg.StreamName + ".provision",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    5,
		MaxAckPending: c.cfg.WorkerCount * 2,
	}

	provisionCons, err := c.stream.CreateOrUpdateConsumer(ctx, provisionConsumerConfig)
	if err != nil {
		return fmt.Errorf("failed to create provision consumer: %w", err)
	}

	cleanupConsumerConfig := jetstream.ConsumerConfig{
		Durable:       "cleanup-workers",
		Description:   "Workers that process cleanup tasks",
		FilterSubject: c.cfg.StreamName + ".cleanup",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second,
		MaxDeliver:    3,
		MaxAckPending: c.cfg.WorkerCount * 2,
	}

	cleanupCons, err := c.stream.CreateOrUpdateConsumer(ctx, cleanupConsumerConfig)
	if err != nil {
		return fmt.Errorf("failed to create cleanup consumer: %w", err)
	}

	var wg sync.WaitGroup

	// Start provision workers
	for i := 0; i < c.cfg.WorkerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			c.runProvisionWorker(provisionCons, workerID)
		}(i)
	}

	// Start cleanup workers
	for i := 0; i < c.cfg.WorkerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			c.runCleanupWorker(cleanupCons, workerID)
		}(i)
	}

	// Wait for all workers in separate goroutine
	go func() {
		wg.Wait()
		close(c.doneCh)
	}()

	c.logger.Info("NATS consumer started", "provisionWorkers", c.cfg.WorkerCount, "cleanupWorkers", c.cfg.WorkerCount)

	return nil
}

// runProvisionWorker processes provision tasks.
func (c *NATSConsumer) runProvisionWorker(cons jetstream.Consumer, workerID int) {
	c.logger.Info("Provision worker started", "workerID", workerID)
	defer c.logger.Info("Provision worker stopped", "workerID", workerID)

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		// Fetch messages with timeout
		msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			if err != context.DeadlineExceeded {
				c.logger.Warn("Provision worker fetch error", "workerID", workerID, "error", err)
			}
			continue
		}

		for msg := range msgs.Messages() {
			c.processProvisionMessage(msg, workerID)
		}

		if msgs.Error() != nil && msgs.Error() != context.DeadlineExceeded {
			c.logger.Warn("Provision worker messages error", "workerID", workerID, "error", msgs.Error())
		}
	}
}

func (c *NATSConsumer) processProvisionMessage(msg jetstream.Msg, workerID int) {
	var task ProvisionTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		c.logger.Warn("Failed to unmarshal provision task", "workerID", workerID, "error", err)
		msg.Term() // Terminate - don't redeliver malformed messages
		return
	}

	c.logger.Info("Processing provision task", "workerID", workerID, "taskID", task.TaskID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := c.provisionHandler(ctx, task); err != nil {
		c.logger.Warn("Provision task failed", "workerID", workerID, "taskID", task.TaskID, "error", err)
		msg.Nak() // Negative ack - will be redelivered
		return
	}

	msg.Ack()
	c.logger.Info("Completed provision task", "workerID", workerID, "taskID", task.TaskID)
}

// runCleanupWorker processes cleanup tasks.
func (c *NATSConsumer) runCleanupWorker(cons jetstream.Consumer, workerID int) {
	c.logger.Info("Cleanup worker started", "workerID", workerID)
	defer c.logger.Info("Cleanup worker stopped", "workerID", workerID)

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		// Fetch messages with timeout
		msgs, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			if err != context.DeadlineExceeded {
				c.logger.Warn("Cleanup worker fetch error", "workerID", workerID, "error", err)
			}
			continue
		}

		for msg := range msgs.Messages() {
			c.processCleanupMessage(msg, workerID)
		}

		if msgs.Error() != nil && msgs.Error() != context.DeadlineExceeded {
			c.logger.Warn("Cleanup worker messages error", "workerID", workerID, "error", msgs.Error())
		}
	}
}

func (c *NATSConsumer) processCleanupMessage(msg jetstream.Msg, workerID int) {
	var task CleanupTask
	if err := json.Unmarshal(msg.Data(), &task); err != nil {
		c.logger.Warn("Failed to unmarshal cleanup task", "workerID", workerID, "error", err)
		msg.Term()
		return
	}

	c.logger.Info("Processing cleanup task", "workerID", workerID, "taskID", task.TaskID, "instanceID", task.InstanceID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := c.cleanupHandler(ctx, task); err != nil {
		c.logger.Warn("Cleanup task failed", "workerID", workerID, "taskID", task.TaskID, "error", err)
		msg.Nak()
		return
	}

	msg.Ack()
	c.logger.Info("Completed cleanup task", "workerID", workerID, "taskID", task.TaskID)
}

// Stop gracefully stops the consumer.
func (c *NATSConsumer) Stop(ctx context.Context) error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	close(c.stopCh)
	c.running = false
	c.mu.Unlock()

	// Wait for workers to finish or context timeout
	select {
	case <-c.doneCh:
		c.logger.Info("All NATS consumer workers stopped")
	case <-ctx.Done():
		c.logger.Warn("NATS consumer stop timed out")
	}

	// Drain connection
	return c.nc.Drain()
}
