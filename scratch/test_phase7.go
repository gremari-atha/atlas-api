package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"atlas-api/internal/config"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	baseURL      = "http://localhost:5000"
	wsURL        = "ws://localhost:5000/ws"
	superSecret  = "superadminsecret"
	testTenantID = "paytronik" // Real APP Tenant ID from GAS config
	tenantSecret = "paytroniksecret"
)

func main() {
	slog.Info("=== STARTING PHASE 7 REAL-WORLD E2E INTEGRATION TEST ===")

	// Load env
	config.LoadEnv(".env")
	config.LoadEnv("../.env")

	// 1. Database Connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://admin:admin123@localhost:5432/atlas?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		slog.Error("failed to connect to postgres database", "err", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	// Clean up previous runs if any
	slog.Info("Cleaning up potential old tenant records...")
	_, _ = dbPool.Exec(ctx, "DELETE FROM master.tenant WHERE id = $1", testTenantID)
	_, _ = dbPool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, testTenantID))
	_, _ = dbPool.Exec(ctx, "DELETE FROM master.email_forward_queue")

	// 2. Generate Superadmin Token
	superadminToken, err := generateSuperadminToken()
	if err != nil {
		slog.Error("failed to generate superadmin token", "err", err)
		os.Exit(1)
	}
	slog.Info("Generated Superadmin JWT token successfully")

	// 3. Create Tenant (POST /tenant) - Simulating Admin Portal/Superadmin
	slog.Info("Creating tenant via POST /tenant (Superadmin flow)...")
	createTenantReq := map[string]string{
		"id":     testTenantID,
		"secret": tenantSecret,
	}
	_, err = sendRequest(http.MethodPost, "/tenant", createTenantReq, superadminToken, "")
	if err != nil {
		slog.Error("failed to create tenant", "err", err)
		os.Exit(1)
	}
	slog.Info("Tenant schema provisioned and migrated successfully")

	// 4. Seed Email Subjects and Platform Products in Tenant Schema - Simulating Setup
	slog.Info("Seeding OTP and Netflix Link subjects inside tenant schema...")
	_, err = dbPool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".email_subject (context, subject, extract_method, created_at, updated_at)
		VALUES 
			('netflixOtp', 'Netflix OTP Code', 'OTP_EXTRACT', NOW(), NOW()),
			('netflixUrl', 'Netflix Password Reset Link', 'NETFLIX_URL_EXTRACT', NOW(), NOW())
	`, testTenantID))
	if err != nil {
		slog.Error("failed to seed email subjects", "err", err)
		os.Exit(1)
	}

	// 5. Generate Tenant Token - Simulating Token Sign-in
	slog.Info("Generating tenant token (POST /tenant/access-token)...")
	accessTokenReq := map[string]string{
		"tenant_id": testTenantID,
		"secret":    tenantSecret,
	}
	tokenRespBody, err := sendRequest(http.MethodPost, "/tenant/access-token", accessTokenReq, "", "")
	if err != nil {
		slog.Error("failed to get tenant token", "err", err)
		os.Exit(1)
	}
	var tokenData map[string]string
	_ = json.Unmarshal([]byte(tokenRespBody), &tokenData)
	tenantToken := tokenData["token"]

	// ==========================================
	// SIMULATE BOT2 WEB CONNECTION
	// ==========================================
	slog.Info("=== SIMULATING BOT2 WS CONNECTION ===")
	wsAddr := fmt.Sprintf("%s?token=%s&connection_name=bot2&connection_type=BOT", wsURL, tenantToken)
	conn, _, err := websocket.DefaultDialer.Dial(wsAddr, nil)
	if err != nil {
		slog.Error("failed to connect bot2 ws", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Read connected frame
	_, connMsg, err := conn.ReadMessage()
	if err != nil {
		slog.Error("failed to read connected message", "err", err)
		os.Exit(1)
	}
	slog.Info("Bot2 Connected successfully", "payload", string(connMsg))

	// Subscribe to parsed events (Sanitized email: user_e2e_netflix_gmail_com)
	otpEventName := "user_e2e_netflix_gmail_com:netflixOtp"
	urlEventName := "user_e2e_netflix_gmail_com:netflixUrl"

	slog.Info("Bot2 subscribing to event channels...", "otpEvent", otpEventName, "urlEvent", urlEventName)
	subOtp := map[string]interface{}{
		"event": "subscribe-event",
		"data": map[string]string{
			"eventName": otpEventName,
		},
	}
	subOtpBytes, _ := json.Marshal(subOtp)
	_ = conn.WriteMessage(websocket.TextMessage, subOtpBytes)

	subUrl := map[string]interface{}{
		"event": "subscribe-event",
		"data": map[string]string{
			"eventName": urlEventName,
		},
	}
	subUrlBytes, _ := json.Marshal(subUrl)
	_ = conn.WriteMessage(websocket.TextMessage, subUrlBytes)

	// Wait to make sure subscriptions register in connection hub
	time.Sleep(100 * time.Millisecond)

	// ==========================================
	// SIMULATE GAS (GOOGLE APPS SCRIPT) FLOW
	// ==========================================
	slog.Info("=== SIMULATING GOOGLE APPS SCRIPT (GAS) FLOW ===")
	
	// 1. Fetch Subjects list
	slog.Info("GAS fetches subjects list (GET /email-forward/subject?tenant=paytronik)...")
	subjectsResp, err := sendRequest(http.MethodGet, "/email-forward/subject?tenant="+testTenantID, nil, "", "")
	if err != nil {
		slog.Error("GAS failed to fetch subjects", "err", err)
		os.Exit(1)
	}
	slog.Info("GAS received subjects list", "resp", subjectsResp)

	// 2. Forward Mail to Webhook
	slog.Info("GAS forwards matching emails to webhook (POST /email-forward)...")
	receiveEmailReq := map[string]interface{}{
		"tenant": testTenantID,
		"emails": []map[string]interface{}{
			{
				"from":    "user.e2e.netflix@gmail.com",
				"subject": "Netflix OTP Code",
				"date":    time.Now().Format(time.RFC3339),
				"text":    "Hello,\nYour Netflix verification code is:\n581023\nUse this to confirm.",
			},
			{
				"from":    "user.e2e.netflix@gmail.com",
				"subject": "Netflix Password Reset Link",
				"date":    time.Now().Format(time.RFC3339),
				"text":    "Please click here to update your password:\nhttps://www.netflix.com/password?reset=993810283012983\nThank you.",
			},
		},
	}
	_, err = sendRequest(http.MethodPost, "/email-forward", receiveEmailReq, "", "")
	if err != nil {
		slog.Error("GAS failed to submit email webhook", "err", err)
		os.Exit(1)
	}
	slog.Info("GAS posted raw emails successfully")

	// ==========================================
	// VERIFY ASYNC PROCESSING & DB STORAGE
	// ==========================================
	slog.Info("Waiting for background worker to process queue...")
	time.Sleep(1500 * time.Millisecond)

	// Verify master queue is empty
	var queueCount int
	err = dbPool.QueryRow(ctx, "SELECT COUNT(*) FROM master.email_forward_queue").Scan(&queueCount)
	if err != nil || queueCount != 0 {
		slog.Error("expected queue to be empty, check worker processing log", "err", err, "count", queueCount)
		os.Exit(1)
	}
	slog.Info("Verified master email forward queue is completely empty (processed & deleted)")

	// Verify parsed results stored in tenant's email_message_ts hypertable
	var messageCount int
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM "%s".email_message_ts`, testTenantID)).Scan(&messageCount)
	if err != nil || messageCount != 2 {
		slog.Error("expected 2 parsed messages saved in email_message_ts", "err", err, "count", messageCount)
		os.Exit(1)
	}
	slog.Info("Verified 2 parsed email messages inserted into tenant TimescaleDB hypertable")

	// Read and verify the values in email_message_ts
	rows, err := dbPool.Query(ctx, fmt.Sprintf(`
		SELECT parsed_context, parsed_data FROM "%s".email_message_ts ORDER BY parsed_context ASC
	`, testTenantID))
	if err != nil {
		slog.Error("failed to select parsed email records", "err", err)
		os.Exit(1)
	}
	defer rows.Close()
	for rows.Next() {
		var contextVal, dataVal string
		_ = rows.Scan(&contextVal, &dataVal)
		slog.Info("Database Parsed Msg Result", "context", contextVal, "parsed_data", dataVal)
		if contextVal == "netflixOtp" && dataVal != "581023" {
			slog.Error("OTP extraction mismatch", "expected", "581023", "got", dataVal)
			os.Exit(1)
		}
		if contextVal == "netflixUrl" && dataVal != "https://www.netflix.com/password?reset=993810283012983" {
			slog.Error("Netflix URL extraction mismatch", "expected", "https://www.netflix.com/password?reset=993810283012983", "got", dataVal)
			os.Exit(1)
		}
	}

	// ==========================================
	// VERIFY WEBSOCKET BROADCAST TO BOT2
	// ==========================================
	slog.Info("Verifying real-time events broadcast over WebSocket to Bot2...")
	for i := 0; i < 2; i++ {
		_, wsMsg, err := conn.ReadMessage()
		if err != nil {
			slog.Error("failed to read WS broadcast event", "err", err)
			os.Exit(1)
		}
		slog.Info("Bot2 Received Frame", "payload", string(wsMsg))

		var frame struct {
			Event string `json:"event"`
			Data  struct {
				EventName string `json:"eventName"`
				Payload   struct {
					From    string `json:"from"`
					Subject string `json:"subject"`
					Data    string `json:"data"`
				} `json:"payload"`
			} `json:"data"`
		}
		if err := json.Unmarshal(wsMsg, &frame); err != nil {
			slog.Error("failed to unmarshal WS broadcast frame", "err", err)
			os.Exit(1)
		}

		if frame.Event != "event" {
			slog.Error("expected event code 'event'", "got", frame.Event)
			os.Exit(1)
		}

		if frame.Data.EventName == otpEventName {
			if frame.Data.Payload.Data != "581023" {
				slog.Error("WS OTP payload mismatch", "expected", "581023", "got", frame.Data.Payload.Data)
				os.Exit(1)
			}
			slog.Info("Bot2: WS OTP verification passed successfully!")
		} else if frame.Data.EventName == urlEventName {
			if frame.Data.Payload.Data != "https://www.netflix.com/password?reset=993810283012983" {
				slog.Error("WS URL payload mismatch", "expected", "https://www.netflix.com/password?reset=993810283012983", "got", frame.Data.Payload.Data)
				os.Exit(1)
			}
			slog.Info("Bot2: WS URL verification passed successfully!")
		} else {
			slog.Error("received unexpected event subscription channel", "name", frame.Data.EventName)
			os.Exit(1)
		}
	}

	// ==========================================
	// SIMULATE DASHBOARD API FETCH CALLS
	// ==========================================
	slog.Info("=== SIMULATING DASHBOARD REST CALLS ===")

	// 1. Get Accounts
	slog.Info("Dashboard fetches accounts list (GET /account)...")
	accResp, err := sendRequest(http.MethodGet, "/account", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("Dashboard failed to fetch accounts", "err", err)
		os.Exit(1)
	}
	slog.Info("Dashboard received accounts list successfully", "bytes", len(accResp))

	// 2. Get Transactions
	slog.Info("Dashboard fetches transactions list (GET /transaction)...")
	txResp, err := sendRequest(http.MethodGet, "/transaction", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("Dashboard failed to fetch transactions", "err", err)
		os.Exit(1)
	}
	slog.Info("Dashboard received transactions list successfully", "bytes", len(txResp))

	// 3. Get Statistics
	slog.Info("Dashboard fetches statistics (GET /statistic)...")
	statsResp, err := sendRequest(http.MethodGet, "/statistic", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("Dashboard failed to fetch statistics", "err", err)
		os.Exit(1)
	}
	slog.Info("Dashboard received statistics successfully", "bytes", len(statsResp))

	// 10. Clean up tenant
	slog.Info("Cleaning up: deleting test tenant and dropping schema...")
	_, err = sendRequest(http.MethodDelete, "/tenant/"+testTenantID, nil, superadminToken, "")
	if err != nil {
		slog.Error("failed to delete tenant", "err", err)
		os.Exit(1)
	}
	slog.Info("Tenant deleted and schema dropped successfully!")

	slog.Info("=== ALL PHASE 7 REAL-WORLD E2E INTEGRATION TESTS PASSED SUCCESSFULLY ===")
}

func generateSuperadminToken() (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": "SUPERADMIN",
	})
	return token.SignedString([]byte(superSecret))
}

func sendRequest(method, path string, body interface{}, token string, tenantID string) (string, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		bodyReader = bytes.NewBuffer(jsonBytes)
	}

	req, err := http.NewRequest(method, baseURL+path, bodyReader)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "VC "+token)
	}
	if tenantID != "" {
		req.Header.Set("x-tenant-id", tenantID)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	return string(respBytes), nil
}
