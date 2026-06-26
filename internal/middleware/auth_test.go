package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"atlas-api/internal/config"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testSuperadminSecret = "superadminsecret"
	testTenantID         = "tenant_test_auth_unit"
	testTenantSecret     = "tenantsecretpass"
)

func TestAuthMiddleware(t *testing.T) {
	// Load environment variables for the test
	config.LoadEnv("../../.env")

	// Initialize database connection for TenantAuth credential lookup
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://admin:admin123@localhost:5432/atlas?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to connect to database for unit tests: %v", err)
	}
	defer dbPool.Close()

	// Seed test tenant
	_, err = dbPool.Exec(ctx, "INSERT INTO master.tenant (id, secret) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET secret = EXCLUDED.secret", testTenantID, testTenantSecret)
	if err != nil {
		t.Fatalf("failed to seed test tenant: %v", err)
	}
	defer func() {
		_, _ = dbPool.Exec(context.Background(), "DELETE FROM master.tenant WHERE id = $1", testTenantID)
	}()

	auth := NewAuthMiddleware(dbPool, testSuperadminSecret)

	// Define final handler to verify context values
	handlerFunc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uCtx, ok := GetUserContext(r.Context())
		if !ok {
			http.Error(w, "missing context", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Test-Role", uCtx.Role)
		w.Header().Set("X-Test-Tenant", uCtx.TenantID)
		w.WriteHeader(http.StatusOK)
	})

	t.Run("SuperadminAuth_Success", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"role": "SUPERADMIN",
		})
		tokenStr, _ := token.SignedString([]byte(testSuperadminSecret))

		req := httptest.NewRequest(http.MethodGet, "/test/super", nil)
		req.Header.Set("Authorization", "VC "+tokenStr)
		rec := httptest.NewRecorder()

		auth.SuperadminAuth(handlerFunc).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}
		if rec.Header().Get("X-Test-Role") != "SUPERADMIN" {
			t.Errorf("expected role SUPERADMIN, got %q", rec.Header().Get("X-Test-Role"))
		}
	})

	t.Run("SuperadminAuth_Forbidden", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"role": "USER",
		})
		tokenStr, _ := token.SignedString([]byte(testSuperadminSecret))

		req := httptest.NewRequest(http.MethodGet, "/test/super", nil)
		req.Header.Set("Authorization", "VC "+tokenStr)
		rec := httptest.NewRecorder()

		auth.SuperadminAuth(handlerFunc).ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("expected status 403, got %d", rec.Code)
		}
	})

	t.Run("TenantAuth_Success", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"tenant_id": testTenantID,
			"role":      "USER",
		})
		tokenStr, _ := token.SignedString([]byte(testTenantSecret))

		req := httptest.NewRequest(http.MethodGet, "/test/tenant", nil)
		req.Header.Set("Authorization", "VC "+tokenStr)
		req.Header.Set("x-tenant-id", testTenantID)
		rec := httptest.NewRecorder()

		auth.TenantAuth(handlerFunc).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Test-Tenant") != testTenantID {
			t.Errorf("expected tenant %q, got %q", testTenantID, rec.Header().Get("X-Test-Tenant"))
		}
	})

	t.Run("TenantAuth_MismatchedHeader", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"tenant_id": testTenantID,
			"role":      "USER",
		})
		tokenStr, _ := token.SignedString([]byte(testTenantSecret))

		req := httptest.NewRequest(http.MethodGet, "/test/tenant", nil)
		req.Header.Set("Authorization", "VC "+tokenStr)
		req.Header.Set("x-tenant-id", "another_tenant")
		rec := httptest.NewRecorder()

		auth.TenantAuth(handlerFunc).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", rec.Code)
		}
	})
}
