package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Task names matching the old system constants
const (
	TypeUnfreezeAccount      = "unfreezeAccount"
	TypeAccountSubsEndNotify = "accountSubsEndNotify"
	TypeSubsEndDisable       = "subsEndDisableAccount"
	TypeNetflixResetPassword = "netflixResetPassword"
	TypeEmailDisconnect      = "email:disconnect"
)

// BasePayload wraps task data with tenant and task ID context
type BasePayload[T any] struct {
	TenantID string `json:"tenant_id"`
	TaskID   string `json:"task_id,omitempty"`
	Data     T      `json:"data"`
}

// UnfreezeAccountPayload data details
type UnfreezeAccountPayload struct {
	AccountID int64 `json:"accountId,string"`
}

// AccountSubsEndNotifyPayload data details
type AccountSubsEndNotifyPayload struct {
	Message string `json:"message"`
}

// SubsEndDisableAccountPayload data details
type SubsEndDisableAccountPayload struct {
	AccountID int64 `json:"accountId,string"`
}

// NetflixResetPasswordPayload data details
type NetflixResetPasswordPayload struct {
	AccountID int64  `json:"accountId,string"`
	Email     string `json:"email"`
}

// EmailDisconnectPayload data details
type EmailDisconnectPayload struct {
	EmailAccountID string `json:"email_account_id"`
}

// Client manages task enqueuing using Asynq
type Client struct {
	asynqClient *asynq.Client
}

// NewClient constructs a new Asynq enqueuer client
func NewClient(redisURL string) (*Client, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url for asynq: %w", err)
	}

	return &Client{
		asynqClient: asynq.NewClient(opt),
	}, nil
}

// Close releases Asynq client resources
func (c *Client) Close() error {
	return c.asynqClient.Close()
}

// EnqueueTask serializes and enqueues a generic task to run at a specific time (optional delay)
func EnqueueTask[T any](c *Client, ctx context.Context, taskType string, tenantID string, taskID string, data T, runAt time.Time) error {
	payload := BasePayload[T]{
		TenantID: tenantID,
		TaskID:   taskID,
		Data:     data,
	}

	bytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal task payload: %w", err)
	}

	task := asynq.NewTask(taskType, bytes)

	var opts []asynq.Option
	if !runAt.IsZero() && runAt.After(time.Now()) {
		opts = append(opts, asynq.ProcessAt(runAt))
	}

	_, err = c.asynqClient.EnqueueContext(ctx, task, opts...)
	if err != nil {
		return fmt.Errorf("failed to enqueue task: %w", err)
	}

	return nil
}
