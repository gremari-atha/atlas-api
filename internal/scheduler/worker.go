package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"atlas-api/internal/websocket"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WorkerServer processes background tasks
type WorkerServer struct {
	dbPool      *pgxpool.Pool
	wsHub       *websocket.Hub
	asynqServer *asynq.Server
}

// NewWorkerServer creates a new WorkerServer instance
func NewWorkerServer(redisURL string, dbPool *pgxpool.Pool, wsHub *websocket.Hub) (*WorkerServer, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redis url for worker: %w", err)
	}

	// Create server with standard options
	srv := asynq.NewServer(opt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			"default": 10,
		},
	})

	return &WorkerServer{
		dbPool:      dbPool,
		wsHub:       wsHub,
		asynqServer: srv,
	}, nil
}

// Start registers task handlers and runs the Asynq server
func (w *WorkerServer) Start() error {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeUnfreezeAccount, w.HandleUnfreezeAccount)
	mux.HandleFunc(TypeAccountSubsEndNotify, w.HandleAccountSubsEndNotify)
	mux.HandleFunc(TypeSubsEndDisable, w.HandleSubsEndDisable)
	mux.HandleFunc(TypeNetflixResetPassword, w.HandleNetflixResetPassword)

	slog.Info("starting asynq background worker server...")
	return w.asynqServer.Start(mux)
}

// Shutdown terminates the worker server gracefully
func (w *WorkerServer) Shutdown() {
	slog.Info("stopping asynq background worker server...")
	w.asynqServer.Shutdown()
}

// HandleUnfreezeAccount updates account to clear freeze timestamp
func (w *WorkerServer) HandleUnfreezeAccount(ctx context.Context, t *asynq.Task) error {
	var payload BasePayload[UnfreezeAccountPayload]
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	slog.Info("processing unfreezeAccount task", "tenant_id", payload.TenantID, "account_id", payload.Data.AccountID)

	// Update freeze_until to NULL in tenant's schema
	query := fmt.Sprintf(`UPDATE "%s".account SET freeze_until = NULL, updated_at = NOW() WHERE id = $1`, payload.TenantID)
	_, err := w.dbPool.Exec(ctx, query, payload.Data.AccountID)
	if err != nil {
		return fmt.Errorf("failed to execute unfreeze account: %w", err)
	}

	return nil
}

// HandleAccountSubsEndNotify writes a REMINDER level syslog entry
func (w *WorkerServer) HandleAccountSubsEndNotify(ctx context.Context, t *asynq.Task) error {
	var payload BasePayload[AccountSubsEndNotifyPayload]
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	slog.Info("processing accountSubsEndNotify task", "tenant_id", payload.TenantID)

	// Write log into master.syslog_ts hypertable
	query := `
		INSERT INTO master.syslog_ts (level, context, message, tenant_id, created_at)
		VALUES ('REMINDER', 'AccountSubsEnd', $1, $2, NOW())
	`
	_, err := w.dbPool.Exec(ctx, query, payload.Data.Message, payload.TenantID)
	if err != nil {
		return fmt.Errorf("failed to write subs end reminder log to syslog: %w", err)
	}

	return nil
}

// HandleSubsEndDisable updates account status to 'disable'
func (w *WorkerServer) HandleSubsEndDisable(ctx context.Context, t *asynq.Task) error {
	var payload BasePayload[SubsEndDisableAccountPayload]
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	slog.Info("processing subsEndDisableAccount task", "tenant_id", payload.TenantID, "account_id", payload.Data.AccountID)

	// Update status to disable in tenant's schema
	query := fmt.Sprintf(`UPDATE "%s".account SET status = 'disable', updated_at = NOW() WHERE id = $1`, payload.TenantID)
	_, err := w.dbPool.Exec(ctx, query, payload.Data.AccountID)
	if err != nil {
		return fmt.Errorf("failed to execute disable account: %w", err)
	}

	return nil
}

// HandleNetflixResetPassword validates account/email, and triggers bot dispatch via WebSocket Hub
func (w *WorkerServer) HandleNetflixResetPassword(ctx context.Context, t *asynq.Task) error {
	var payload BasePayload[NetflixResetPasswordPayload]
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	slog.Info("processing netflixResetPassword task", "tenant_id", payload.TenantID, "account_id", payload.Data.AccountID, "email", payload.Data.Email)

	if payload.Data.AccountID == 0 || payload.Data.Email == "" {
		errMsg := fmt.Sprintf("Skip dispatch NETFLIX_RESET_PASSWORD task %s: missing required payload field(s)", payload.TaskID)
		w.logIssue(ctx, payload.TenantID, errMsg)
		return nil // Return nil so task is not retried due to validation error
	}

	// 1. Verify that account exists and joins email matches
	query := fmt.Sprintf(`
		SELECT a.id 
		FROM "%s".account a
		JOIN "%s".email e ON a.email_id = e.id
		WHERE a.id = $1 AND e.email = $2
	`, payload.TenantID, payload.TenantID)

	var id int64
	err := w.dbPool.QueryRow(ctx, query, payload.Data.AccountID, payload.Data.Email).Scan(&id)
	if err != nil {
		errMsg := fmt.Sprintf("[%s] Reset Password Netflix dispatch failed for accountId: %d, email: %s. Account or Email not found", payload.TenantID, payload.Data.AccountID, payload.Data.Email)
		w.logIssue(ctx, payload.TenantID, errMsg)
		return fmt.Errorf("account not found: %w", err)
	}

	// 2. Dispatch task via WebSocket Connection Hub
	_, err = w.wsHub.DispatchTask(ctx, payload.TaskID, payload.TenantID, "netflix", "resetPassword", payload.Data)
	if err != nil {
		errMsg := fmt.Sprintf("[%s] Reset Password Netflix dispatch failed for accountId: %d, email: %s. %s", payload.TenantID, payload.Data.AccountID, payload.Data.Email, err.Error())
		w.logIssue(ctx, payload.TenantID, errMsg)
		return err
	}

	return nil
}

func (w *WorkerServer) logIssue(ctx context.Context, tenantID string, message string) {
	slog.Error("netflix reset password issue", "tenant_id", tenantID, "message", message)
	query := `
		INSERT INTO master.syslog_ts (level, context, message, tenant_id, created_at)
		VALUES ('ERROR', 'TaskProcessorNetflixResetPassword', $1, $2, NOW())
	`
	_, err := w.dbPool.Exec(ctx, query, message, tenantID)
	if err != nil {
		slog.Error("failed to write netflix dispatch error to database", "err", err)
	}
}
