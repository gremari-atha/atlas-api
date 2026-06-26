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

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	baseURL      = "http://localhost:5000"
	superSecret  = "superadminsecret"
	testTenantID = "tenant_test_email"
	tenantSecret = "emailsecretpass"
)

func main() {
	slog.Info("=== STARTING EMAIL ENDPOINTS VERIFICATION TEST ===")

	// 1. Get database pool connection
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

	// 2. Generate Superadmin Token
	superadminToken, err := generateSuperadminToken()
	if err != nil {
		slog.Error("failed to generate superadmin token", "err", err)
		os.Exit(1)
	}
	slog.Info("Generated Superadmin JWT token successfully")

	// 3. Create Tenant (POST /tenant)
	slog.Info("Creating tenant via POST /tenant to initialize schema...")
	createTenantReq := map[string]string{
		"id":     testTenantID,
		"secret": tenantSecret,
	}
	_, err = sendRequest(http.MethodPost, "/tenant", createTenantReq, superadminToken, "")
	if err != nil {
		slog.Error("failed to create tenant", "err", err)
		os.Exit(1)
	}
	slog.Info("Tenant created successfully")

	// 4. Generate Tenant Token
	slog.Info("Generating tenant token...")
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
	// TEST 1: CRUD /email
	// ==========================================
	slog.Info("--- TESTING CRUD /email ---")

	// Create Email
	createEmailReq := map[string]string{
		"email":    "test.email@volvecapital.com",
		"password": "testpassword123",
	}
	createEmailResp, err := sendRequest(http.MethodPost, "/email", createEmailReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to create email via POST /email", "err", err)
		os.Exit(1)
	}
	slog.Info("POST /email success", "response", createEmailResp)

	var emailObj map[string]interface{}
	_ = json.Unmarshal([]byte(createEmailResp), &emailObj)
	emailID := emailObj["id"].(string)

	// List Emails
	listEmailResp, err := sendRequest(http.MethodGet, "/email", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to list emails via GET /email", "err", err)
		os.Exit(1)
	}
	slog.Info("GET /email success", "response", listEmailResp)

	// Get Email Detail
	getEmailResp, err := sendRequest(http.MethodGet, "/email/"+emailID, nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to get email detail via GET /email/{id}", "err", err)
		os.Exit(1)
	}
	slog.Info("GET /email/{id} success", "response", getEmailResp)

	// Update Email
	updateEmailReq := map[string]string{
		"email":    "test.email.updated@volvecapital.com",
		"password": "newpassword456",
	}
	updateEmailResp, err := sendRequest(http.MethodPatch, "/email/"+emailID, updateEmailReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to update email via PATCH /email/{id}", "err", err)
		os.Exit(1)
	}
	slog.Info("PATCH /email/{id} success", "response", updateEmailResp)

	// ==========================================
	// TEST 2: /email-message
	// ==========================================
	slog.Info("--- TESTING /email-message ---")

	// Insert dummy message directly into DB
	slog.Info("Inserting mock parsed email message directly into TimescaleDB...")
	_, err = dbPool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".email_message_ts (tenant_id, from_email, subject, email_date, parsed_context, parsed_data, created_at)
		VALUES ($1, $2, $3, NOW(), $4, $5, NOW())
	`, testTenantID), testTenantID, "netflix@info.netflix.com", "Your Verification Code", "netflixOtp", "998273")
	if err != nil {
		slog.Error("failed to insert mock email message", "err", err)
		os.Exit(1)
	}

	// Fetch /email-message
	listMessagesResp, err := sendRequest(http.MethodGet, "/email-message", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to list email messages via GET /email-message", "err", err)
		os.Exit(1)
	}
	slog.Info("GET /email-message success", "response", listMessagesResp)

	// ==========================================
	// TEST 3: CRUD /email-subject
	// ==========================================
	slog.Info("--- TESTING CRUD /email-subject ---")

	// Create Email Subject
	createSubjectReq := map[string]string{
		"context":        "netflixOtpTest",
		"subject":        "Your Verification Code",
		"extract_method": "OTP_EXTRACT",
	}
	createSubjectResp, err := sendRequest(http.MethodPost, "/email-subject", createSubjectReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to create email subject via POST /email-subject", "err", err)
		os.Exit(1)
	}
	slog.Info("POST /email-subject success", "response", createSubjectResp)

	var subjectObj map[string]interface{}
	_ = json.Unmarshal([]byte(createSubjectResp), &subjectObj)
	subjectID := subjectObj["id"].(string)

	// List Email Subjects
	listSubjectResp, err := sendRequest(http.MethodGet, "/email-subject", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to list email subjects via GET /email-subject", "err", err)
		os.Exit(1)
	}
	slog.Info("GET /email-subject success", "response", listSubjectResp)

	// Get Email Subject Detail
	getSubjectResp, err := sendRequest(http.MethodGet, "/email-subject/"+subjectID, nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to get email subject detail via GET /email-subject/{id}", "err", err)
		os.Exit(1)
	}
	slog.Info("GET /email-subject/{id} success", "response", getSubjectResp)

	// Update Email Subject
	updateSubjectReq := map[string]string{
		"subject": "Your Updated Verification Code",
	}
	updateSubjectResp, err := sendRequest(http.MethodPatch, "/email-subject/"+subjectID, updateSubjectReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to update email subject via PATCH /email-subject/{id}", "err", err)
		os.Exit(1)
	}
	slog.Info("PATCH /email-subject/{id} success", "response", updateSubjectResp)

	// Delete Email Subject
	_, err = sendRequest(http.MethodDelete, "/email-subject/"+subjectID, nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to delete email subject via DELETE /email-subject/{id}", "err", err)
		os.Exit(1)
	}
	slog.Info("DELETE /email-subject/{id} success")

	// ==========================================
	// TEST 4: Cleanup
	// ==========================================
	slog.Info("--- TESTING DELETE /email ---")
	_, err = sendRequest(http.MethodDelete, "/email/"+emailID, nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to delete email via DELETE /email/{id}", "err", err)
		os.Exit(1)
	}
	slog.Info("DELETE /email/{id} success")

	// Delete Tenant
	slog.Info("Cleaning up: deleting test tenant and dropping schema...")
	_, err = sendRequest(http.MethodDelete, "/tenant/"+testTenantID, nil, superadminToken, "")
	if err != nil {
		slog.Error("failed to delete tenant", "err", err)
		os.Exit(1)
	}
	slog.Info("Tenant deleted and schema dropped successfully!")

	slog.Info("=== ALL EMAIL ENDPOINTS TESTS PASSED SUCCESSFULLY ===")
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
