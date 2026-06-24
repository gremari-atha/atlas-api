package tenant

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Tenant struct {
	ID        string `json:"id"`
	Secret    string `json:"secret"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type CreateTenantPayload struct {
	ID     string `json:"id" validate:"required"`
	Secret string `json:"secret" validate:"required"`
}

type UpdateTenantPayload struct {
	Secret string `json:"secret" validate:"required"`
}

type AccessTokenPayload struct {
	TenantID string `json:"tenant_id" validate:"required"`
	Secret   string   `json:"secret" validate:"required"`
}

type TenantHandler struct {
	dbPool *pgxpool.Pool
}

func NewTenantHandler(dbPool *pgxpool.Pool) *TenantHandler {
	return &TenantHandler{dbPool: dbPool}
}

// RegisterRoutes registers all tenant endpoints
func (h *TenantHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	// Public endpoint
	r.With(middleware.ValidateBody[AccessTokenPayload]()).Post("/tenant/access-token", h.GenerateAccessToken)

	// Superadmin only endpoints
	r.Route("/tenant", func(r chi.Router) {
		r.Use(auth.SuperadminAuth)
		r.Get("/", h.FindAll)
		r.With(middleware.ValidateBody[CreateTenantPayload]()).Post("/", h.Create)
		r.Get("/{id}", h.FindOne)
		r.With(middleware.ValidateBody[UpdateTenantPayload]()).Patch("/{id}", h.Update)
		r.Delete("/{id}", h.Remove)
	})
}

func (h *TenantHandler) GenerateAccessToken(w http.ResponseWriter, r *http.Request) {
	payload := middleware.GetBody[AccessTokenPayload](r)

	var secret string
	err := h.dbPool.QueryRow(r.Context(), "SELECT secret FROM master.tenant WHERE id = $1", payload.TenantID).Scan(&secret)
	if err != nil {
		slog.Warn("tenant access token failed: tenant not found", "tenant_id", payload.TenantID)
		response.Error(w, http.StatusUnauthorized, "App Id or Secret invalid")
		return
	}

	if secret != payload.Secret {
		slog.Warn("tenant access token failed: secret mismatch", "tenant_id", payload.TenantID)
		response.Error(w, http.StatusUnauthorized, "App Id or Secret invalid")
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": payload.TenantID,
		"role":      "USER",
	})

	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		slog.Error("failed to sign jwt", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to sign access token")
		return
	}

	response.JSON(w, http.StatusCreated, map[string]string{
		"id":    payload.TenantID,
		"token": tokenString,
	})
}

func (h *TenantHandler) FindAll(w http.ResponseWriter, r *http.Request) {
	page, limit, offset := response.ParsePagination(r)

	tenantFilter := r.URL.Query().Get("tenant_id")
	var tenants []Tenant
	var total int64

	var err error
	if tenantFilter != "" {
		err = h.dbPool.QueryRow(r.Context(), "SELECT COUNT(*) FROM master.tenant WHERE id ILIKE $1", "%"+tenantFilter+"%").Scan(&total)
		if err != nil {
			slog.Error("failed to count tenants", "err", err)
			response.Error(w, http.StatusInternalServerError, "database query failed")
			return
		}

		rows, err := h.dbPool.Query(r.Context(), "SELECT id, secret, created_at, updated_at FROM master.tenant WHERE id ILIKE $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3", "%"+tenantFilter+"%", limit, offset)
		if err != nil {
			slog.Error("failed to query tenants", "err", err)
			response.Error(w, http.StatusInternalServerError, "database query failed")
			return
		}
		defer rows.Close()

		for rows.Next() {
			var t Tenant
			var cr, up sql.NullTime
			if err := rows.Scan(&t.ID, &t.Secret, &cr, &up); err != nil {
				slog.Error("failed to scan tenant", "err", err)
				response.Error(w, http.StatusInternalServerError, "database scan failed")
				return
			}
			if cr.Valid {
				t.CreatedAt = cr.Time.Format("2006-01-02T15:04:05Z07:00")
			}
			if up.Valid {
				t.UpdatedAt = up.Time.Format("2006-01-02T15:04:05Z07:00")
			}
			tenants = append(tenants, t)
		}
	} else {
		err = h.dbPool.QueryRow(r.Context(), "SELECT COUNT(*) FROM master.tenant").Scan(&total)
		if err != nil {
			slog.Error("failed to count tenants", "err", err)
			response.Error(w, http.StatusInternalServerError, "database query failed")
			return
		}

		rows, err := h.dbPool.Query(r.Context(), "SELECT id, secret, created_at, updated_at FROM master.tenant ORDER BY created_at DESC LIMIT $1 OFFSET $2", limit, offset)
		if err != nil {
			slog.Error("failed to query tenants", "err", err)
			response.Error(w, http.StatusInternalServerError, "database query failed")
			return
		}
		defer rows.Close()

		for rows.Next() {
			var t Tenant
			var cr, up sql.NullTime
			if err := rows.Scan(&t.ID, &t.Secret, &cr, &up); err != nil {
				slog.Error("failed to scan tenant", "err", err)
				response.Error(w, http.StatusInternalServerError, "database scan failed")
				return
			}
			if cr.Valid {
				t.CreatedAt = cr.Time.Format("2006-01-02T15:04:05Z07:00")
			}
			if up.Valid {
				t.UpdatedAt = up.Time.Format("2006-01-02T15:04:05Z07:00")
			}
			tenants = append(tenants, t)
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(tenants, total, page, limit))
}

func (h *TenantHandler) FindOne(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var t Tenant
	var cr, up sql.NullTime
	err := h.dbPool.QueryRow(r.Context(), "SELECT id, secret, created_at, updated_at FROM master.tenant WHERE id = $1", id).Scan(&t.ID, &t.Secret, &cr, &up)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("tenant dengan id: %s tidak ditemukan", id))
		return
	}
	if cr.Valid {
		t.CreatedAt = cr.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if up.Valid {
		t.UpdatedAt = up.Time.Format("2006-01-02T15:04:05Z07:00")
	}

	response.JSON(w, http.StatusOK, t)
}

func (h *TenantHandler) Create(w http.ResponseWriter, r *http.Request) {
	payload := middleware.GetBody[CreateTenantPayload](r)

	// Start a SQL transaction to ensure dynamic schema and migrations run atomically
	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		slog.Error("failed to begin transaction", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// 1. Insert tenant into master
	var t Tenant
	var cr, up sql.NullTime
	err = tx.QueryRow(r.Context(), `
		INSERT INTO master.tenant (id, secret, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		RETURNING id, secret, created_at, updated_at
	`, payload.ID, payload.Secret).Scan(&t.ID, &t.Secret, &cr, &up)

	if err != nil {
		slog.Error("failed to insert tenant", "id", payload.ID, "err", err)
		response.Error(w, http.StatusBadRequest, "App Id already exists or DB constraint error")
		return
	}

	// 2. Create Schema programmatically
	schemaQuery := fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, payload.ID)
	_, err = tx.Exec(r.Context(), schemaQuery)
	if err != nil {
		slog.Error("failed to create schema", "id", payload.ID, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to create tenant schema")
		return
	}

	// Commit PostgreSQL transaction first so that database-migration tool can connect and find the schema
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("failed to commit transaction", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// 3. Run migrations on the new schema using golang-migrate
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://admin_vc:adminpassword@localhost:5432/volvecapital?sslmode=disable"
	}

	slog.Info("running dynamic migration for new tenant schema", "tenant_id", payload.ID)
	if err := RunTenantMigrations(dbURL, payload.ID); err != nil {
		slog.Error("dynamic migration failed for tenant", "tenant_id", payload.ID, "err", err)
		// Try to clean up database tenant entry since migrations failed
		_, _ = h.dbPool.Exec(context.Background(), "DELETE FROM master.tenant WHERE id = $1", payload.ID)
		_, _ = h.dbPool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, payload.ID))
		response.Error(w, http.StatusInternalServerError, "dynamic migration failed: "+err.Error())
		return
	}

	if cr.Valid {
		t.CreatedAt = cr.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if up.Valid {
		t.UpdatedAt = up.Time.Format("2006-01-02T15:04:05Z07:00")
	}

	response.JSON(w, http.StatusCreated, t)
}

func (h *TenantHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	payload := middleware.GetBody[UpdateTenantPayload](r)

	var t Tenant
	var cr, up sql.NullTime
	err := h.dbPool.QueryRow(r.Context(), `
		UPDATE master.tenant
		SET secret = $1, updated_at = NOW()
		WHERE id = $2
		RETURNING id, secret, created_at, updated_at
	`, payload.Secret, id).Scan(&t.ID, &t.Secret, &cr, &up)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("tenant dengan id: %s tidak ditemukan", id))
		return
	}
	if cr.Valid {
		t.CreatedAt = cr.Time.Format("2006-01-02T15:04:05Z07:00")
	}
	if up.Valid {
		t.UpdatedAt = up.Time.Format("2006-01-02T15:04:05Z07:00")
	}

	response.JSON(w, http.StatusOK, t)
}

func (h *TenantHandler) Remove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Delete from master.tenant
	res, err := tx.Exec(r.Context(), "DELETE FROM master.tenant WHERE id = $1", id)
	if err != nil {
		slog.Error("failed to delete tenant", "id", id, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to delete tenant record")
		return
	}

	rowsAffected := res.RowsAffected()
	if rowsAffected == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("tenant dengan id: %s tidak ditemukan", id))
		return
	}

	// Drop schema CASCADE
	_, err = tx.Exec(r.Context(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "%s" CASCADE`, id))
	if err != nil {
		slog.Error("failed to drop schema", "id", id, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to drop tenant schema")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func RunTenantMigrations(dbURL string, tenantID string) error {
	urlWithSchema := dbURL
	if strings.Contains(dbURL, "?") {
		urlWithSchema += fmt.Sprintf("&search_path=%s,public", tenantID)
	} else {
		urlWithSchema += fmt.Sprintf("?search_path=%s,public", tenantID)
	}

	m, err := migrate.New("file://db/migrations/tenant", urlWithSchema)
	if err != nil {
		return fmt.Errorf("failed to initialize migration for tenant %s: %w", tenantID, err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("error during migration for tenant %s: %w", tenantID, err)
	}
	return nil
}
