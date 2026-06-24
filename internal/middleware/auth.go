package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"atlas-api/internal/response"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserContext contains authenticated user credentials
type UserContext struct {
	TenantID string
	Role     string
}

const userContextKey contextKey = "userContext"

// AuthMiddleware manages JWT validations for both Superadmin and Tenant scopes
type AuthMiddleware struct {
	dbPool           *pgxpool.Pool
	superadminSecret []byte
}

// NewAuthMiddleware constructs a new AuthMiddleware instance
func NewAuthMiddleware(dbPool *pgxpool.Pool, superadminSecret string) *AuthMiddleware {
	return &AuthMiddleware{
		dbPool:           dbPool,
		superadminSecret: []byte(superadminSecret),
	}
}

// GetUserContext retrieves user info from context
func GetUserContext(ctx context.Context) (UserContext, bool) {
	val := ctx.Value(userContextKey)
	if val == nil {
		return UserContext{}, false
	}
	u, ok := val.(UserContext)
	return u, ok
}

// extractToken retrieves the JWT token from the Authorization header (VC <token>)
func extractToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", fmt.Errorf("authorization header is missing")
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "VC" {
		return "", fmt.Errorf("invalid authorization header format (expected: VC <token>)")
	}

	return parts[1], nil
}

// SuperadminAuth guards endpoints meant exclusively for Superadmins
func (am *AuthMiddleware) SuperadminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr, err := extractToken(r)
		if err != nil {
			response.Error(w, http.StatusUnauthorized, err.Error())
			return
		}

		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return am.superadminSecret, nil
		})

		if err != nil || !token.Valid {
			response.Error(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			response.Error(w, http.StatusUnauthorized, "invalid token claims")
			return
		}

		role, _ := claims["role"].(string)
		if role != "SUPERADMIN" {
			response.Error(w, http.StatusForbidden, "action forbidden: superadmin privilege required")
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, UserContext{
			Role: role,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TenantAuth guards endpoints for operational tenant business logic
func (am *AuthMiddleware) TenantAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr, err := extractToken(r)
		if err != nil {
			response.Error(w, http.StatusUnauthorized, err.Error())
			return
		}

		// 1. Decode token unverified to extract tenant_id first
		claims := jwt.MapClaims{}
		parser := jwt.NewParser()
		_, _, err = parser.ParseUnverified(tokenStr, &claims)
		if err != nil {
			response.Error(w, http.StatusUnauthorized, "malformed authorization token")
			return
		}

		tenantID, _ := claims["tenant_id"].(string)
		role, _ := claims["role"].(string)

		if tenantID == "" {
			response.Error(w, http.StatusUnauthorized, "invalid token: missing tenant_id")
			return
		}

		// 2. Validate header x-tenant-id matches token tenant_id
		headerTenantID := r.Header.Get("x-tenant-id")
		if headerTenantID == "" {
			response.Error(w, http.StatusBadRequest, "missing x-tenant-id header")
			return
		}

		if headerTenantID != tenantID {
			response.Error(w, http.StatusUnauthorized, "tenant mismatch")
			return
		}

		// 3. Query tenant secret from master.tenant
		var secret string
		err = am.dbPool.QueryRow(r.Context(), "SELECT secret FROM master.tenant WHERE id = $1", tenantID).Scan(&secret)
		if err != nil {
			slog.Error("failed to retrieve tenant credentials from database", "tenant_id", tenantID, "err", err)
			response.Error(w, http.StatusUnauthorized, "invalid tenant")
			return
		}

		// 4. Verify token signature with tenant's secret key
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			response.Error(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, UserContext{
			TenantID: tenantID,
			Role:     role,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
