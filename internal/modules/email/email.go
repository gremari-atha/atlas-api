package email

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"
	"atlas-api/internal/scheduler"

	"github.com/emersion/go-imap/client"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DB models
type Email struct {
	ID             int64     `json:"id,string"`
	Email          string    `json:"email"`
	Password       string    `json:"password,omitempty"`
	EmailAccountID *string   `json:"email_account_id,omitempty"`
	Provider       *string   `json:"provider,omitempty"`
	Status         *string   `json:"status,omitempty"`
	LastError      *string   `json:"last_error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
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

type GCPProject struct {
	ID           int32     `json:"id"`
	ProjectName  string    `json:"project_name"`
	ClientID     string    `json:"client_id"`
	ClientSecret string    `json:"client_secret,omitempty"`
	Domain       string    `json:"domain"`
	ActiveCount  int32     `json:"active_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CreateGCPProjectPayload struct {
	ProjectName  string `json:"project_name" validate:"required"`
	ClientID     string `json:"client_id" validate:"required"`
	ClientSecret string `json:"client_secret" validate:"required"`
	Domain       string `json:"domain" validate:"required"`
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
	dbPool      *pgxpool.Pool
	redisClient *redis.Client
	asynqClient *scheduler.Client
}

func NewEmailHandler(dbPool *pgxpool.Pool, redisClient *redis.Client, asynqClient *scheduler.Client) *EmailHandler {
	return &EmailHandler{
		dbPool:      dbPool,
		redisClient: redisClient,
		asynqClient: asynqClient,
	}
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
		r.With(middleware.ValidateBody[ConnectIMAPPayload]()).Post("/connect-imap", h.ConnectIMAP)
	})

	r.Route("/email-connections", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.With(middleware.ValidateBody[InitializeConnectionPayload]()).Post("/initialize", h.InitializeConnection)
		r.Delete("/{email_account_id}", h.DisconnectEmailConnection)
	})

	r.Route("/email-message", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllEmailMessages)
	})

	r.Route("/admin/gcp-projects", func(r chi.Router) {
		r.Use(auth.SuperadminAuth)
		r.Get("/", h.FindAllGCPProjects)
		r.With(middleware.ValidateBody[CreateGCPProjectPayload]()).Post("/", h.CreateGCPProject)
		r.Delete("/{id}", h.RemoveGCPProject)
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
		whereClause = "WHERE e.email ILIKE $2" // $2 because $1 is tenantID
		args = append(args, "%"+emailFilter+"%")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".email e %s`, tenantID, whereClause)
	var total int64
	var countArgs []interface{}
	if emailFilter != "" {
		countQuery = strings.ReplaceAll(countQuery, "$2", "$1")
		countArgs = append(countArgs, "%"+emailFilter+"%")
	}
	err := h.dbPool.QueryRow(r.Context(), countQuery, countArgs...).Scan(&total)
	if err != nil {
		slog.Error("failed to count emails", "tenant", tenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed", err)
		return
	}

	// Dynamic sorting construction
	orderByClause := "e.email ASC"
	orderBy := r.URL.Query().Get("order_by")
	orderDir := r.URL.Query().Get("order_direction")
	if orderBy == "email" {
		dir := "ASC"
		if strings.ToUpper(orderDir) == "DESC" {
			dir = "DESC"
		}
		orderByClause = fmt.Sprintf("e.email %s", dir)
	}

	selectQuery := fmt.Sprintf(`
		SELECT e.id, e.email, e.password, e.created_at, e.updated_at,
		       ma.id as email_account_id, ma.provider, ma.status, ma.last_error
		FROM "%s".email e
		LEFT JOIN master.email_accounts ma ON ma.tenant_id = $1 AND ma.email = e.email
		%s 
		ORDER BY %s 
		LIMIT $%d OFFSET $%d
	`, tenantID, whereClause, orderByClause, len(args)+2, len(args)+3)

	selectArgs := append([]interface{}{tenantID}, args...)
	selectArgs = append(selectArgs, limit, offset)

	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query emails", "tenant", tenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	var emails []Email
	for rows.Next() {
		var em Email
		if err := rows.Scan(&em.ID, &em.Email, &em.Password, &em.CreatedAt, &em.UpdatedAt, &em.EmailAccountID, &em.Provider, &em.Status, &em.LastError); err == nil {
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
		SELECT e.id, e.email, e.password, e.created_at, e.updated_at,
		       ma.id as email_account_id, ma.provider, ma.status, ma.last_error
		FROM "%s".email e
		LEFT JOIN master.email_accounts ma ON ma.tenant_id = $1 AND ma.email = e.email
		WHERE e.id = $2
	`, tenantID), tenantID, id).Scan(&em.ID, &em.Email, &em.Password, &em.CreatedAt, &em.UpdatedAt, &em.EmailAccountID, &em.Provider, &em.Status, &em.LastError)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("email dengan id: %d tidak ditemukan", id), err)
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
		response.Error(w, http.StatusInternalServerError, "failed to insert email", err)
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
		response.Error(w, http.StatusNotFound, fmt.Sprintf("email dengan id: %d tidak ditemukan", id), err)
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
		response.Error(w, http.StatusInternalServerError, "failed to update email", err)
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
		response.Error(w, http.StatusInternalServerError, "failed to delete email", err)
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
		response.Error(w, http.StatusInternalServerError, "database count failed", err)
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
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
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

// ==========================================
// ADMIN GCP PROJECTS POOL CRUD
// ==========================================

func (h *EmailHandler) FindAllGCPProjects(w http.ResponseWriter, r *http.Request) {
	rows, err := h.dbPool.Query(r.Context(), "SELECT id, project_name, client_id, client_secret, domain, active_count, created_at, updated_at FROM master.gcp_projects ORDER BY id ASC")
	if err != nil {
		slog.Error("failed to query gcp projects", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	var projects []GCPProject
	for rows.Next() {
		var p GCPProject
		if err := rows.Scan(&p.ID, &p.ProjectName, &p.ClientID, &p.ClientSecret, &p.Domain, &p.ActiveCount, &p.CreatedAt, &p.UpdatedAt); err == nil {
			p.ClientSecret = "" // redact secret
			projects = append(projects, p)
		}
	}

	response.JSON(w, http.StatusOK, projects)
}

func (h *EmailHandler) CreateGCPProject(w http.ResponseWriter, r *http.Request) {
	payload := middleware.GetBody[CreateGCPProjectPayload](r)

	var p GCPProject
	err := h.dbPool.QueryRow(r.Context(), `
		INSERT INTO master.gcp_projects (project_name, client_id, client_secret, domain, active_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 0, NOW(), NOW())
		RETURNING id, project_name, client_id, client_secret, domain, active_count, created_at, updated_at
	`, payload.ProjectName, payload.ClientID, payload.ClientSecret, payload.Domain).Scan(&p.ID, &p.ProjectName, &p.ClientID, &p.ClientSecret, &p.Domain, &p.ActiveCount, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		slog.Error("failed to create gcp project", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert gcp project", err)
		return
	}

	p.ClientSecret = "" // redact secret
	response.JSON(w, http.StatusCreated, p)
}

func (h *EmailHandler) RemoveGCPProject(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "GCP project ID must be integer")
		return
	}

	// Verify active count is zero before deletion
	var activeCount int
	err = h.dbPool.QueryRow(r.Context(), "SELECT active_count FROM master.gcp_projects WHERE id = $1", id).Scan(&activeCount)
	if err != nil {
		response.Error(w, http.StatusNotFound, "GCP project not found", err)
		return
	}

	if activeCount > 0 {
		response.Error(w, http.StatusBadRequest, "Cannot delete a GCP project with active email connections")
		return
	}

	_, err = h.dbPool.Exec(r.Context(), "DELETE FROM master.gcp_projects WHERE id = $1", id)
	if err != nil {
		slog.Error("failed to delete gcp project", "id", id, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to delete gcp project", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type ConnectIMAPPayload struct {
	EmailAccountID string `json:"email_account_id" validate:"required,uuid"`
	Host           string `json:"host" validate:"required"`
	Port           int    `json:"port" validate:"required"`
	Username       string `json:"username" validate:"required"`
	Password       string `json:"password" validate:"required"`
	Security       string `json:"security" validate:"required,oneof=ssl starttls none"`
}

func (h *EmailHandler) ConnectIMAP(w http.ResponseWriter, r *http.Request) {
	payload := middleware.GetBody[ConnectIMAPPayload](r)

	// 1. Verify target email account exists in master.email_accounts
	var exists bool
	err := h.dbPool.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM master.email_accounts WHERE id = $1)", payload.EmailAccountID).Scan(&exists)
	if err != nil || !exists {
		response.Error(w, http.StatusNotFound, "Email account record not found in master database", err)
		return
	}

	// 2. Perform test connection and capabilities check
	addr := fmt.Sprintf("%s:%d", payload.Host, payload.Port)
	var c *client.Client

	if payload.Security == "ssl" {
		// Implicit TLS
		c, err = client.DialTLS(addr, &tls.Config{InsecureSkipVerify: true})
	} else {
		// Plain or explicit STARTTLS
		c, err = client.Dial(addr)
		if err == nil && payload.Security == "starttls" {
			err = c.StartTLS(&tls.Config{InsecureSkipVerify: true})
		}
	}

	if err != nil {
		response.Error(w, http.StatusBadRequest, "Gagal terhubung ke server IMAP: "+err.Error(), err)
		return
	}
	defer c.Logout()

	// 3. Authenticate
	err = c.Login(payload.Username, payload.Password)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Gagal login ke server IMAP (Username/Password salah): "+err.Error(), err)
		return
	}

	// 4. Verify IDLE support
	supported, err := c.Support("IDLE")
	if err != nil || !supported {
		response.Error(w, http.StatusBadRequest, "Server IMAP ini tidak mendukung IDLE capability", err)
		return
	}

	// 5. Save credentials to master.email_accounts
	creds := map[string]interface{}{
		"host":     payload.Host,
		"port":     payload.Port,
		"username": payload.Username,
		"password": payload.Password,
		"security": payload.Security,
	}
	credsBytes, _ := json.Marshal(creds)

	_, err = h.dbPool.Exec(r.Context(), `
		UPDATE master.email_accounts
		SET credentials = $1, provider = 'imap', status = 'ACTIVE', last_error = NULL, updated_at = NOW()
		WHERE id = $2
	`, string(credsBytes), payload.EmailAccountID)

	if err != nil {
		slog.Error("failed to update imap credentials", "id", payload.EmailAccountID, "err", err)
		response.Error(w, http.StatusInternalServerError, "Gagal menyimpan kredensial ke database", err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"status": "SUCCESS", "message": "IMAP account connected successfully"})
}

func (h *EmailHandler) DisconnectEmailConnection(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	emailAccountID := chi.URLParam(r, "email_account_id")

	// 1. Verify connection exists and belongs to tenant
	var provider string
	err := h.dbPool.QueryRow(r.Context(), `
		SELECT provider FROM master.email_accounts 
		WHERE id = $1 AND tenant_id = $2
	`, emailAccountID, tenantID).Scan(&provider)
	if err != nil {
		response.Error(w, http.StatusNotFound, "Koneksi email tidak ditemukan", err)
		return
	}

	// 2. Publish disconnect event to Redis pubsub for active connection terminations (e.g. IMAP)
	err = h.redisClient.Publish(r.Context(), "email_connections:disconnect", emailAccountID).Err()
	if err != nil {
		slog.Error("failed to publish disconnect event to Redis", "id", emailAccountID, "err", err)
	}

	// 3. Enqueue the cleanups task to Asynq
	payload := scheduler.EmailDisconnectPayload{
		EmailAccountID: emailAccountID,
	}
	err = scheduler.EnqueueTask(h.asynqClient, r.Context(), scheduler.TypeEmailDisconnect, tenantID, "", payload, time.Time{})
	if err != nil {
		slog.Error("failed to enqueue disconnect task in Asynq", "id", emailAccountID, "err", err)
		response.Error(w, http.StatusInternalServerError, "Gagal memproses pemutusan koneksi", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type InitializeConnectionPayload struct {
	EmailID int64 `json:"email_id,string" validate:"required"`
}

func (h *EmailHandler) InitializeConnection(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[InitializeConnectionPayload](r)

	var emailAddress string
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT email FROM "%s".email WHERE id = $1
	`, tenantID), payload.EmailID).Scan(&emailAddress)
	if err != nil {
		response.Error(w, http.StatusNotFound, "Email tidak ditemukan", err)
		return
	}

	// Generate UUID in Go to bypass database gen_random_uuid() requirements
	emailAccountID := uuid.New().String()
	_, err = h.dbPool.Exec(r.Context(), `
		INSERT INTO master.email_accounts (id, tenant_id, email, provider, credentials, status)
		VALUES ($1, $2, $3, '', '', 'DISCONNECTED')
		ON CONFLICT (tenant_id, email) DO UPDATE SET updated_at = NOW()
	`, emailAccountID, tenantID, emailAddress)
	if err != nil {
		slog.Error("failed to initialize connection", "tenant", tenantID, "email", emailAddress, "err", err)
		response.Error(w, http.StatusInternalServerError, "Gagal menginisialisasi koneksi", err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{
		"email_account_id": emailAccountID,
	})
}

