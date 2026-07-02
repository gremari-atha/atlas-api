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
	testTenantID = "tenant_test_e2e"
	tenantSecret = "e2esecretpassword123"
)

func main() {
	slog.Info("=== STARTING PHASE 5 E2E VERIFICATION TEST ===")

	// 1. Get database pool connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/atlas?sslmode=disable"
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

	// 3. Test Superadmin Tenant CRUD: Create Tenant (POST /tenant)
	// This will trigger dynamic schema creation and golang-migrate migrations
	slog.Info("Testing POST /tenant for schema provisioning...")
	createTenantReq := map[string]string{
		"id":     testTenantID,
		"secret": tenantSecret,
	}
	tenantRespBody, err := sendRequest(http.MethodPost, "/tenant", createTenantReq, superadminToken, "")
	if err != nil {
		slog.Error("failed to create tenant", "err", err)
		os.Exit(1)
	}
	slog.Info("Tenant created successfully", "resp", tenantRespBody)

	// Verify tenant table exists in the new schema using SQL query
	var tableCount int
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`
		SELECT COUNT(*) FROM information_schema.tables 
		WHERE table_schema = '%s' AND table_name = 'product'
	`, testTenantID)).Scan(&tableCount)
	if err != nil || tableCount != 1 {
		slog.Error("failed to verify dynamic schema tables creation", "err", err, "count", tableCount)
		os.Exit(1)
	}
	slog.Info("Verified tables migrated successfully in schema: " + testTenantID)

	// 4. Generate Tenant Access Token (POST /tenant/access-token)
	slog.Info("Testing POST /tenant/access-token...")
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
	if err := json.Unmarshal([]byte(tokenRespBody), &tokenData); err != nil {
		slog.Error("failed to parse token json", "err", err)
		os.Exit(1)
	}
	tenantToken := tokenData["token"]
	if tenantToken == "" {
		slog.Error("tenant token is empty in response")
		os.Exit(1)
	}
	slog.Info("Tenant JWT token generated successfully")

	// 5. Test Access controls: Test Tenant Auth
	slog.Info("Testing middleware validation for Tenant auth...")
	// Try without token
	_, err = sendRequest(http.MethodGet, "/test/tenant", nil, "", "")
	if err == nil {
		slog.Error("expected failure on unauthenticated tenant endpoint")
		os.Exit(1)
	}
	slog.Info("Unauthenticated access correctly rejected: " + err.Error())

	// Try with correct token and correct x-tenant-id
	testTenantResp, err := sendRequest(http.MethodGet, "/test/tenant", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to access authenticated tenant endpoint", "err", err)
		os.Exit(1)
	}
	slog.Info("Tenant authenticated endpoint access verified", "resp", testTenantResp)

	// Try with wrong x-tenant-id
	_, err = sendRequest(http.MethodGet, "/test/tenant", nil, tenantToken, "wrong_tenant_id")
	if err == nil {
		slog.Error("expected failure on mismatched tenant id header")
		os.Exit(1)
	}
	slog.Info("Mismatched x-tenant-id correctly rejected: " + err.Error())

	// 6. Seed email row directly via SQL (since no REST endpoint exists for email CRUD)
	slog.Info("Seeding test email directly in schema...")
	var emailID int64
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`
		INSERT INTO "%s".email (email, password, created_at, updated_at)
		VALUES ('user_e2e_netflix@gmail.com', 'securenetflix123', NOW(), NOW())
		RETURNING id
	`, testTenantID)).Scan(&emailID)
	if err != nil {
		slog.Error("failed to seed email row", "err", err)
		os.Exit(1)
	}
	slog.Info("Email row seeded successfully", "email_id", emailID)

	// 7. Test Product & Variant REST Endpoints
	slog.Info("Testing Product REST endpoints...")
	// POST /product
	createProdReq := map[string]string{
		"name": "Netflix Premium Plan",
	}
	prodResp, err := sendRequest(http.MethodPost, "/product", createProdReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to create product", "err", err)
		os.Exit(1)
	}
	slog.Info("Product created successfully", "resp", prodResp)

	var prodData map[string]interface{}
	_ = json.Unmarshal([]byte(prodResp), &prodData)
	prodIDStr, ok := prodData["id"].(string)
	if !ok {
		slog.Error("product ID should be returned as string, check JSON tags", "id", prodData["id"])
		os.Exit(1)
	}
	slog.Info("Product ID received (properly as a JSON string)", "id", prodIDStr)

	// POST /product-variant
	slog.Info("Testing Product Variant REST endpoints...")
	createVarReq := map[string]interface{}{
		"name":       "Premium 1 Screen Ultra HD",
		"duration":   30,
		"interval":   1,
		"cooldown":   300000, // 5 min cooldown in ms
		"product_id": prodIDStr,
	}
	varResp, err := sendRequest(http.MethodPost, "/product-variant", createVarReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to create variant", "err", err)
		os.Exit(1)
	}
	slog.Info("Product variant created successfully", "resp", varResp)

	var varData map[string]interface{}
	_ = json.Unmarshal([]byte(varResp), &varData)
	varIDStr := varData["id"].(string)
	slog.Info("Product Variant ID received (properly as a JSON string)", "id", varIDStr)

	// Seed base price directly into DB (wait! variant has base_price in migrations)
	// Let's update variant's base_price since we couldn't pass it through CreateVariant if it doesn't support it, or check:
	// Let's see: `000021_add_base_price_to_product_variant.up.sql` adds base_price. Let's update it to 150000
	_, err = dbPool.Exec(ctx, fmt.Sprintf(`UPDATE "%s".product_variant SET base_price = 150000 WHERE id = $1`, testTenantID), varIDStr)
	if err != nil {
		slog.Error("failed to update variant base_price", "err", err)
		os.Exit(1)
	}

	// 8. Test Account REST Endpoints (With Nested Profiles)
	slog.Info("Testing Account REST endpoints...")
	// POST /account
	createAccReq := map[string]interface{}{
		"account_password":    "secureNetflixPassword123!",
		"subscription_expiry": time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
		"status":              "active",
		"billing":             "monthly",
		"email_id":            fmt.Sprintf("%d", emailID), // string ID
		"product_variant_id":  varIDStr,
		"pinned":              true,
		"profile": []map[string]interface{}{
			{
				"name":           "Screen Alpha",
				"max_user":       4,
				"allow_generate": true,
			},
			{
				"name":           "Screen Beta",
				"max_user":       4,
				"allow_generate": true,
			},
		},
	}
	accResp, err := sendRequest(http.MethodPost, "/account", createAccReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to create account", "err", err)
		os.Exit(1)
	}
	slog.Info("Account created successfully with nested profiles", "resp", accResp)

	var accData map[string]interface{}
	_ = json.Unmarshal([]byte(accResp), &accData)
	accIDStr := accData["id"].(string)

	// Get Profiles to find their IDs
	profResp, err := sendRequest(http.MethodGet, "/account-profile?account_id="+accIDStr, nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to query profiles", "err", err)
		os.Exit(1)
	}
	slog.Info("Account Profiles listed successfully", "resp", profResp)

	var profListResp map[string]interface{}
	_ = json.Unmarshal([]byte(profResp), &profListResp)
	profItems := profListResp["data"].([]interface{})
	if len(profItems) == 0 {
		slog.Error("no profiles returned for created account")
		os.Exit(1)
	}
	firstProf := profItems[0].(map[string]interface{})
	profIDStr := firstProf["id"].(string)

	// 9. Test Transaction REST Endpoints
	slog.Info("Testing Transaction REST endpoints...")

	// 9a. Test Create Transaction with Candidate Profile Lookup (allow_generate)
	slog.Info("Creating Transaction using candidate profile auto-lookup...")
	createTxReq := map[string]interface{}{
		"customer": "Andy Customer",
		"platform": "Tokopedia",
		"items": []map[string]interface{}{
			{
				"product_variant_id": varIDStr,
			},
		},
	}
	txResp, err := sendRequest(http.MethodPost, "/transaction", createTxReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to create transaction using CTE lookup", "err", err)
		os.Exit(1)
	}
	slog.Info("Transaction created via auto-routing successfully!", "resp", txResp)

	// 9b. Test Create Transaction with Specific Profile ID
	slog.Info("Creating Transaction specifying exact profile ID...")
	createTxSpecificReq := map[string]interface{}{
		"customer": "Jessica Customer",
		"platform": "Shopee",
		"items": []map[string]interface{}{
			{
				"product_variant_id": varIDStr,
				"account_profile_id": profIDStr,
			},
		},
	}
	txSpecificResp, err := sendRequest(http.MethodPost, "/transaction", createTxSpecificReq, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to create transaction with specific profile ID", "err", err)
		os.Exit(1)
	}
	slog.Info("Transaction created specifying exact profile successfully!", "resp", txSpecificResp)

	// Extract Transaction ID and query it
	var txSpecificData map[string]interface{}
	_ = json.Unmarshal([]byte(txSpecificResp), &txSpecificData)
	txObj := txSpecificData["transaction"].(map[string]interface{})
	txIDStr := txObj["id"].(string)

	slog.Info("Testing GET /transaction/{id}...")
	getTxResp, err := sendRequest(http.MethodGet, "/transaction/"+txIDStr, nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to retrieve transaction by ID", "err", err)
		os.Exit(1)
	}
	slog.Info("Retrieved transaction details successfully", "resp", getTxResp)

	// 10. Test Count Status Account Endpoint
	slog.Info("Testing GET /account/count...")
	countResp, err := sendRequest(http.MethodGet, "/account/count", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to retrieve account status counts", "err", err)
		os.Exit(1)
	}
	slog.Info("Account counts retrieved successfully", "resp", countResp)

	// 11. Test Statistics View aggregation (GetAllStatistic)
	slog.Info("Testing GET /statistic...")
	statsResp, err := sendRequest(http.MethodGet, "/statistic?range=month", nil, tenantToken, testTenantID)
	if err != nil {
		slog.Error("failed to get statistics", "err", err)
		os.Exit(1)
	}
	slog.Info("Statistics retrieved successfully!", "resp", statsResp)

	// 12. Clean up Tenant using SUPERADMIN DELETE /tenant/{id}
	slog.Info("Cleaning up: Deleting tenant and dropping schema...")
	_, err = sendRequest(http.MethodDelete, "/tenant/"+testTenantID, nil, superadminToken, "")
	if err != nil {
		slog.Error("failed to delete tenant", "err", err)
		os.Exit(1)
	}
	slog.Info("Tenant deleted and schema dropped successfully!")

	// Verify schema no longer exists
	err = dbPool.QueryRow(ctx, fmt.Sprintf(`
		SELECT COUNT(*) FROM information_schema.schemata 
		WHERE schema_name = '%s'
	`, testTenantID)).Scan(&tableCount)
	if err != nil || tableCount != 0 {
		slog.Error("schema drop could not be verified", "err", err, "count", tableCount)
		os.Exit(1)
	}
	slog.Info("Verified schema dropped fully.")

	slog.Info("=== ALL E2E TESTS PASSED SUCCESSFULLY ===")
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
