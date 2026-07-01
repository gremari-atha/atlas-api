package logger

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SyslogEntry struct {
	Level     string    `json:"level"`
	Context   string    `json:"context"`
	Message   string    `json:"message"`
	TenantID  *string   `json:"tenant_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateLogPayload struct {
	Level     string     `json:"level" validate:"required"`
	Context   string     `json:"context" validate:"required"`
	Message   string     `json:"message" validate:"required"`
	CreatedAt *time.Time `json:"created_at"`
}

type LoggerHandler struct {
	dbPool *pgxpool.Pool
}

func NewLoggerHandler(dbPool *pgxpool.Pool) *LoggerHandler {
	return &LoggerHandler{dbPool: dbPool}
}

func (h *LoggerHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	r.Route("/log", func(r chi.Router) {
		r.With(auth.TenantAuth).Get("/", h.GetLogWithPagination)
		r.With(auth.SuperadminAuth).Get("/sys", h.GetSyslogWithPagination)
		r.With(auth.TenantAuth, middleware.ValidateBody[CreateLogPayload]()).Post("/", h.LogToDb)
	})
}

func (h *LoggerHandler) GetLogWithPagination(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	levelFilter := q.Get("level")
	contextFilter := q.Get("context")

	queryConditions := []string{"tenant_id = $1"}
	args := []interface{}{tenantID}
	argIdx := 2

	if levelFilter != "" {
		queryConditions = append(queryConditions, fmt.Sprintf("level = $%d", argIdx))
		args = append(args, levelFilter)
		argIdx++
	}
	if contextFilter != "" {
		queryConditions = append(queryConditions, fmt.Sprintf("context = $%d", argIdx))
		args = append(args, contextFilter)
		argIdx++
	}

	whereClause := "WHERE " + strings.Join(queryConditions, " AND ")

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM master.syslog_ts %s", whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count tenant logs", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed", err)
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT level, context, message, tenant_id, created_at
		FROM master.syslog_ts
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query tenant logs", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	var logs []SyslogEntry
	for rows.Next() {
		var entry SyslogEntry
		var tID sql.NullString
		if err := rows.Scan(&entry.Level, &entry.Context, &entry.Message, &tID, &entry.CreatedAt); err == nil {
			if tID.Valid {
				entry.TenantID = &tID.String
			}
			logs = append(logs, entry)
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(logs, total, page, limit))
}

func (h *LoggerHandler) GetSyslogWithPagination(w http.ResponseWriter, r *http.Request) {
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	levelFilter := q.Get("level")
	contextFilter := q.Get("context")

	queryConditions := []string{"tenant_id IS NULL"}
	var args []interface{}
	argIdx := 1

	if levelFilter != "" {
		queryConditions = append(queryConditions, fmt.Sprintf("level = $%d", argIdx))
		args = append(args, levelFilter)
		argIdx++
	}
	if contextFilter != "" {
		queryConditions = append(queryConditions, fmt.Sprintf("context = $%d", argIdx))
		args = append(args, contextFilter)
		argIdx++
	}

	whereClause := "WHERE " + strings.Join(queryConditions, " AND ")

	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM master.syslog_ts %s", whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count system logs", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed", err)
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT level, context, message, tenant_id, created_at
		FROM master.syslog_ts
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argIdx, argIdx+1)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query system logs", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	var logs []SyslogEntry
	for rows.Next() {
		var entry SyslogEntry
		var tID sql.NullString
		if err := rows.Scan(&entry.Level, &entry.Context, &entry.Message, &tID, &entry.CreatedAt); err == nil {
			if tID.Valid {
				entry.TenantID = &tID.String
			}
			logs = append(logs, entry)
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(logs, total, page, limit))
}

func (h *LoggerHandler) LogToDb(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateLogPayload](r)

	createdAt := time.Now()
	if payload.CreatedAt != nil {
		createdAt = *payload.CreatedAt
	}

	_, err := h.dbPool.Exec(r.Context(), `
		INSERT INTO master.syslog_ts (level, context, message, tenant_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, payload.Level, payload.Context, payload.Message, tenantID, createdAt)

	if err != nil {
		slog.Error("failed to log to DB", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert log entry", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// strings.Join helper wrapper
func stringsJoin(elems []string, sep string) string {
	return strings.Join(elems, sep)
}
