package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"

	"atlas-api/internal/scheduler"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TestTaskPayload struct {
	AccountID int64  `json:"accountId,string"`
	Email     string `json:"email"`
}

func main() {
	slog.Info("starting E2E verification test...")

	// 1. Database Connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://admin_vc:adminpassword@localhost:5432/volvecapital?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect to TimescaleDB
	// Using standard pgx for simple connection
	importPool, err := pgxConnect(ctx, dbURL)
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer importPool.Close()

	// 2. Clear and Seed DB
	slog.Info("seeding database with test tenant, products, emails, and accounts...")
	
	// Seed master.tenant
	_, err = importPool.Exec(ctx, `
		INSERT INTO master.tenant (id, secret) 
		VALUES ('tenant_test_1', 'supersecret') 
		ON CONFLICT (id) DO UPDATE SET secret = EXCLUDED.secret
	`)
	if err != nil {
		slog.Error("failed to seed tenant", "err", err)
		os.Exit(1)
	}

	// Clean old data to avoid unique constraints (dependency order)
	_, _ = importPool.Exec(ctx, `TRUNCATE TABLE "tenant_test_1".account CASCADE`)
	_, _ = importPool.Exec(ctx, `TRUNCATE TABLE "tenant_test_1".email CASCADE`)
	_, _ = importPool.Exec(ctx, `TRUNCATE TABLE "tenant_test_1".product_variant CASCADE`)
	_, _ = importPool.Exec(ctx, `TRUNCATE TABLE "tenant_test_1".product CASCADE`)
	_, _ = importPool.Exec(ctx, `TRUNCATE TABLE master.task_queue CASCADE`)

	// Insert Product
	var productID int64
	err = importPool.QueryRow(ctx, `
		INSERT INTO "tenant_test_1".product (name) 
		VALUES ('Test E2E Product') 
		RETURNING id
	`).Scan(&productID)
	if err != nil {
		slog.Error("failed to seed product", "err", err)
		os.Exit(1)
	}

	// Insert Product Variant
	var variantID int64
	err = importPool.QueryRow(ctx, `
		INSERT INTO "tenant_test_1".product_variant (name, duration, interval, cooldown, product_id) 
		VALUES ('Test E2E Variant', 30, 1, 0, $1) 
		RETURNING id
	`, productID).Scan(&variantID)
	if err != nil {
		slog.Error("failed to seed product variant", "err", err)
		os.Exit(1)
	}

	// Insert Email
	var emailID int64
	testEmail := "e2e_netflix@example.com"
	err = importPool.QueryRow(ctx, `
		INSERT INTO "tenant_test_1".email (email, password) 
		VALUES ($1, 'password123') 
		RETURNING id
	`, testEmail).Scan(&emailID)
	if err != nil {
		slog.Error("failed to seed email", "err", err)
		os.Exit(1)
	}

	// Insert Account
	var accountID int64
	err = importPool.QueryRow(ctx, `
		INSERT INTO "tenant_test_1".account (account_password, subscription_expiry, status, billing, email_id, product_variant_id) 
		VALUES ('acc_password_123', NOW() + INTERVAL '30 days', 'active', 'monthly', $1, $2) 
		RETURNING id
	`, emailID, variantID).Scan(&accountID)
	if err != nil {
		slog.Error("failed to seed account", "err", err)
		os.Exit(1)
	}

	// Insert task queue record in master schema
	var taskID int64
	err = importPool.QueryRow(ctx, `
		INSERT INTO master.task_queue (execute_at, subject_id, context, payload, status, tenant_id) 
		VALUES (NOW(), 'netflix', 'resetPassword', $1, 'PENDING', 'tenant_test_1') 
		RETURNING id
	`, fmt.Sprintf(`{"accountId":"%d","email":"%s"}`, accountID, testEmail)).Scan(&taskID)
	if err != nil {
		slog.Error("failed to seed task queue", "err", err)
		os.Exit(1)
	}

	taskIDStr := fmt.Sprintf("%d", taskID)
	slog.Info("database seeded successfully", "account_id", accountID, "email", testEmail, "task_id", taskIDStr)

	// 3. Generate Tenant JWT for WebSocket Bot Handshake
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "tenant_test_1",
		"role":      "BOT",
	})
	tokenStr, err := token.SignedString([]byte("supersecret"))
	if err != nil {
		slog.Error("failed to sign token", "err", err)
		os.Exit(1)
	}

	// 4. Connect via WebSocket client
	wsURL := url.URL{
		Scheme:   "ws",
		Host:     "localhost:5000",
		Path:     "/ws",
		RawQuery: fmt.Sprintf("token=%s&connection_name=e2e-verification-bot&connection_type=BOT", tokenStr),
	}

	slog.Info("connecting to WebSocket Gateway...", "url", wsURL.String())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		slog.Error("websocket dial failed", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Wait and read "connected" frame
	_, msg, err := conn.ReadMessage()
	if err != nil {
		slog.Error("websocket read error", "err", err)
		os.Exit(1)
	}
	slog.Info("received message from server", "payload", string(msg))

	var initialEvent struct {
		Event string `json:"event"`
		Data  struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msg, &initialEvent); err != nil || initialEvent.Event != "connected" {
		slog.Error("expected connected event", "msg", string(msg), "err", err)
		os.Exit(1)
	}
	slog.Info("connected successfully to gateway", "connection_id", initialEvent.Data.ID)

	// 5. Enqueue asynq task trigger
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	asynqClient, err := scheduler.NewClient(redisURL)
	if err != nil {
		slog.Error("failed to create asynq client", "err", err)
		os.Exit(1)
	}
	defer asynqClient.Close()

	slog.Info("enqueuing task in Asynq queue...", "task_id", taskIDStr)
	err = scheduler.EnqueueTask(asynqClient, ctx, scheduler.TypeNetflixResetPassword, "tenant_test_1", taskIDStr, TestTaskPayload{
		AccountID: accountID,
		Email:     testEmail,
	}, time.Time{})
	if err != nil {
		slog.Error("failed to enqueue task", "err", err)
		os.Exit(1)
	}

	// 6. Read "task-dispatch" from websocket connection
	slog.Info("waiting for task-dispatch event via WebSocket...")
	_, msg, err = conn.ReadMessage()
	if err != nil {
		slog.Error("failed to read dispatched task", "err", err)
		os.Exit(1)
	}
	slog.Info("received dispatch frame", "payload", string(msg))

	var dispatchFrame struct {
		Event string `json:"event"`
		Data  struct {
			TaskID  string          `json:"taskId"`
			Module  string          `json:"module"`
			Type    string          `json:"type"`
			Payload TestTaskPayload `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msg, &dispatchFrame); err != nil || dispatchFrame.Event != "task-dispatch" {
		slog.Error("expected task-dispatch event", "msg", string(msg), "err", err)
		os.Exit(1)
	}

	if dispatchFrame.Data.TaskID != taskIDStr {
		slog.Error("mismatch task id", "expected", taskIDStr, "got", dispatchFrame.Data.TaskID)
		os.Exit(1)
	}
	slog.Info("task-dispatch validation passed successfully!")

	// 7. Send "task-accept" event to the server
	slog.Info("sending task-accept event...")
	acceptMsg := map[string]interface{}{
		"event": "task-accept",
		"data": map[string]string{
			"taskId": taskIDStr,
		},
	}
	acceptBytes, _ := json.Marshal(acceptMsg)
	err = conn.WriteMessage(websocket.TextMessage, acceptBytes)
	if err != nil {
		slog.Error("failed to send task-accept", "err", err)
		os.Exit(1)
	}

	// Wait for DB status update to 'DISPATCHED'
	time.Sleep(500 * time.Millisecond)
	var status string
	err = importPool.QueryRow(ctx, "SELECT status FROM master.task_queue WHERE id = $1", taskID).Scan(&status)
	if err != nil || status != "DISPATCHED" {
		slog.Error("failed to verify DISPATCHED status in DB", "status", status, "err", err)
		os.Exit(1)
	}
	slog.Info("DB state transition to DISPATCHED verified successfully!")

	// 8. Send "task-done" event to the server
	slog.Info("sending task-done event...")
	doneMsg := map[string]interface{}{
		"event": "task-done",
		"data": map[string]string{
			"taskId":  taskIDStr,
			"status":  "COMPLETED",
			"message": "password reset link extracted successfully by E2E bot",
		},
	}
	doneBytes, _ := json.Marshal(doneMsg)
	err = conn.WriteMessage(websocket.TextMessage, doneBytes)
	if err != nil {
		slog.Error("failed to send task-done", "err", err)
		os.Exit(1)
	}

	// Wait for DB status update to 'COMPLETED'
	time.Sleep(500 * time.Millisecond)
	err = importPool.QueryRow(ctx, "SELECT status FROM master.task_queue WHERE id = $1", taskID).Scan(&status)
	if err != nil || status != "COMPLETED" {
		slog.Error("failed to verify COMPLETED status in DB", "status", status, "err", err)
		os.Exit(1)
	}
	slog.Info("DB state transition to COMPLETED verified successfully!")

	slog.Info("E2E verification test passed successfully!")
}

// Simple pgx wrapper to avoid complex packages
type pgxConnWrapper struct {
	conn interface {
		Close(context.Context) error
		Exec(context.Context, string, ...interface{}) (interface{}, error)
		QueryRow(context.Context, string, ...interface{}) interface {
			Scan(...interface{}) error
		}
	}
	realConn *pgxpool.Pool
}

func pgxConnect(ctx context.Context, url string) (*pgxConnWrapper, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	return &pgxConnWrapper{realConn: pool}, nil
}

func (w *pgxConnWrapper) Close() {
	w.realConn.Close()
}

func (w *pgxConnWrapper) Exec(ctx context.Context, sql string, arguments ...interface{}) (interface{}, error) {
	return w.realConn.Exec(ctx, sql, arguments...)
}

func (w *pgxConnWrapper) QueryRow(ctx context.Context, sql string, args ...interface{}) pgxRow {
	return w.realConn.QueryRow(ctx, sql, args...)
}

type pgxRow interface {
	Scan(...interface{}) error
}
