package email

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB models
type Email struct {
	ID        int64     `json:"id,string"`
	Email     string    `json:"email"`
	Password  string    `json:"password,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type EmailMessage struct {
	ID            int64     `json:"id,string"`
	TenantID      string    `json:"tenant_id"`
	FromEmail     string    `json:"from_email"`
	Subject       string    `json:"subject"`
	EmailDate     time.Time `json:"email_date"`
	ParsedContext string    `json:"parsed_context"`
	ParsedData    string    `json:"parsed_data"`
	CreatedAt     time.Time `json:"created_at"`
}

// Payloads
type CreateEmailPayload struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

type UpdateEmailPayload struct {
	Email    *string `json:"email" validate:"omitempty,email"`
	Password *string `json:"password"`
}

type EmailHandler struct {
	dbPool *pgxpool.Pool
}

func NewEmailHandler(dbPool *pgxpool.Pool) *EmailHandler {
	return &EmailHandler{dbPool: dbPool}
}

// RegisterRoutes registers endpoints for email and email-message
func (h *EmailHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	r.Route("/email", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllEmails)
		r.Get("/{id}", h.FindOneEmail)
		r.With(middleware.ValidateBody[CreateEmailPayload]()).Post("/", h.CreateEmail)
		r.With(middleware.ValidateBody[UpdateEmailPayload]()).Patch("/{id}", h.UpdateEmail)
		r.Delete("/{id}", h.RemoveEmail)
	})

	r.Route("/email-message", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllEmailMessages)
	})
}

// ==========================================
// EMAIL CRUD
// ==========================================

func (h *EmailHandler) FindAllEmails(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	emailFilter := r.URL.Query().Get("email")
	whereClause := ""
	var args []interface{}
	if emailFilter != "" {
		whereClause = "WHERE email ILIKE $1"
		args = append(args, "%"+emailFilter+"%")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".email %s`, tenantID, whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count emails", "tenant", tenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, email, password, created_at, updated_at 
		FROM "%s".email 
		%s 
		ORDER BY email ASC 
		LIMIT $%d OFFSET $%d
	`, tenantID, whereClause, len(args)+1, len(args)+2)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query emails", "tenant", tenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	var emails []Email
	for rows.Next() {
		var em Email
		if err := rows.Scan(&em.ID, &em.Email, &em.Password, &em.CreatedAt, &em.UpdatedAt); err == nil {
			emails = append(emails, em)
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(emails, total, page, limit))
}

func (h *EmailHandler) FindOneEmail(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var em Email
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, email, password, created_at, updated_at 
		FROM "%s".email WHERE id = $1
	`, tenantID), id).Scan(&em.ID, &em.Email, &em.Password, &em.CreatedAt, &em.UpdatedAt)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("email dengan id: %d tidak ditemukan", id))
		return
	}

	response.JSON(w, http.StatusOK, em)
}

func (h *EmailHandler) CreateEmail(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateEmailPayload](r)

	// Check duplicate email
	var count int
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".email WHERE email = $1`, tenantID), payload.Email).Scan(&count)
	if count > 0 {
		response.Error(w, http.StatusBadRequest, "Email sudah terdaftar")
		return
	}

	var em Email
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".email (email, password, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		RETURNING id, email, password, created_at, updated_at
	`, tenantID), payload.Email, payload.Password).Scan(&em.ID, &em.Email, &em.Password, &em.CreatedAt, &em.UpdatedAt)

	if err != nil {
		slog.Error("failed to create email", "tenant", tenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert email")
		return
	}

	response.JSON(w, http.StatusCreated, em)
}

func (h *EmailHandler) UpdateEmail(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateEmailPayload](r)

	if payload.Email == nil && payload.Password == nil {
		response.Error(w, http.StatusBadRequest, "Tidak ada data yang diupdate")
		return
	}

	// Verify target exists
	var dummy int
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT 1 FROM "%s".email WHERE id = $1`, tenantID), id).Scan(&dummy)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("email dengan id: %d tidak ditemukan", id))
		return
	}

	// Check duplicate email if it's changing
	if payload.Email != nil {
		var duplicate int
		_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".email WHERE email = $1 AND id != $2`, tenantID), *payload.Email, id).Scan(&duplicate)
		if duplicate > 0 {
			response.Error(w, http.StatusBadRequest, "Email sudah terdaftar")
			return
		}
	}

	query := fmt.Sprintf(`UPDATE "%s".email SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Email != nil {
		query += fmt.Sprintf("email = $%d, ", argIdx)
		args = append(args, *payload.Email)
		argIdx++
	}
	if payload.Password != nil {
		query += fmt.Sprintf("password = $%d, ", argIdx)
		args = append(args, *payload.Password)
		argIdx++
	}

	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d", argIdx)
	args = append(args, id)

	_, err = h.dbPool.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update email", "tenant", tenantID, "id", id, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update email")
		return
	}

	var em Email
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, email, password, created_at, updated_at 
		FROM "%s".email WHERE id = $1
	`, tenantID), id).Scan(&em.ID, &em.Email, &em.Password, &em.CreatedAt, &em.UpdatedAt)

	response.JSON(w, http.StatusOK, em)
}

func (h *EmailHandler) RemoveEmail(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".email WHERE id = $1`, tenantID), id)
	if err != nil {
		slog.Error("failed to delete email", "tenant", tenantID, "id", id, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to delete email")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("email dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ==========================================
// EMAIL MESSAGE READ-ONLY
// ==========================================

func (h *EmailHandler) FindAllEmailMessages(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	fromEmailFilter := r.URL.Query().Get("from_email")
	whereClause := ""
	var args []interface{}
	if fromEmailFilter != "" {
		whereClause = "WHERE from_email ILIKE $1"
		args = append(args, "%"+fromEmailFilter+"%")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".email_message_ts %s`, tenantID, whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count email messages", "tenant", tenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, tenant_id, from_email, subject, email_date, parsed_context, parsed_data, created_at 
		FROM "%s".email_message_ts 
		%s 
		ORDER BY created_at DESC 
		LIMIT $%d OFFSET $%d
	`, tenantID, whereClause, len(args)+1, len(args)+2)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query email messages", "tenant", tenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	var messages []EmailMessage
	for rows.Next() {
		var em EmailMessage
		err = rows.Scan(&em.ID, &em.TenantID, &em.FromEmail, &em.Subject, &em.EmailDate, &em.ParsedContext, &em.ParsedData, &em.CreatedAt)
		if err == nil {
			messages = append(messages, em)
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(messages, total, page, limit))
}
