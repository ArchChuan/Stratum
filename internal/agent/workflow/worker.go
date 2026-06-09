package workflow

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
)

const TaskQueue = "agent-react"

// TemporalWorkerComponent implements harness.Component.
type TemporalWorkerComponent struct {
	cfg    *config.TemporalConfig
	capGW  capgateway.CapabilityGateway
	logger *zap.Logger
	client client.Client
	worker worker.Worker
}

func NewTemporalWorkerComponent(
	cfg *config.TemporalConfig,
	capGW capgateway.CapabilityGateway,
	logger *zap.Logger,
) *TemporalWorkerComponent {
	return &TemporalWorkerComponent{cfg: cfg, capGW: capGW, logger: logger}
}

func (c *TemporalWorkerComponent) Name() string { return "temporal-worker" }

func (c *TemporalWorkerComponent) Start(ctx context.Context) error {
	cl, err := client.Dial(client.Options{
		HostPort:  c.cfg.HostPort,
		Namespace: c.cfg.Namespace,
		Logger:    newZapTemporalLogger(c.logger),
	})
	if err != nil {
		return fmt.Errorf("temporal-worker: dial: %w", err)
	}
	c.client = cl

	taskQueue := c.cfg.TaskQueue
	if taskQueue == "" {
		taskQueue = TaskQueue
	}
	w := worker.New(cl, taskQueue, worker.Options{
		MaxConcurrentActivityExecutionSize:     c.cfg.WorkerMaxConcurrentActivities,
		MaxConcurrentWorkflowTaskExecutionSize: c.cfg.WorkerMaxConcurrentWorkflows,
	})

	deps := &ActivityDeps{CapGateway: c.capGW}
	w.RegisterWorkflow(ReActWorkflow)
	w.RegisterActivityWithOptions(deps.ExecuteCapabilityActivity, activity.RegisterOptions{
		Name: ExecuteCapabilityActivityName,
	})

	if err := w.Start(); err != nil {
		return fmt.Errorf("temporal-worker: start: %w", err)
	}
	c.worker = w
	c.logger.Info("temporal worker started", zap.String("task_queue", taskQueue))
	return nil
}

func (c *TemporalWorkerComponent) Stop(_ context.Context) error {
	if c.worker != nil {
		c.worker.Stop()
	}
	if c.client != nil {
		c.client.Close()
	}
	return nil
}

func (c *TemporalWorkerComponent) HealthCheck(_ context.Context) error {
	if c.client == nil {
		return fmt.Errorf("temporal client not initialized")
	}
	return nil
}

// ExecuteWorkflow implements agent.TemporalWorkflowStarter.
// Delegates to the underlying Temporal client initialized in Start().
func (c *TemporalWorkerComponent) ExecuteWorkflow(
	ctx context.Context,
	options client.StartWorkflowOptions,
	workflow interface{},
	args ...interface{},
) (client.WorkflowRun, error) {
	if c.client == nil {
		return nil, fmt.Errorf("temporal-worker: client not initialized (worker not started)")
	}
	return c.client.ExecuteWorkflow(ctx, options, workflow, args...)
}
